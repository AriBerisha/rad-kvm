package main

// Single-user authentication for the control plane.
//   * password hashed with PBKDF2-HMAC-SHA256 (stdlib only, 600k iterations)
//   * session = HMAC-signed cookie (stateless; survives restarts via a persisted
//     signing secret), so no server-side session store
//   * credentials live in a 0600 JSON file
// First visit (no account yet) -> setup; afterwards -> login.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	pbkdf2Iter = 600000
	cookieName = "qkvm_session"
	sessionTTL = 12 * time.Hour
	minPass    = 8
)

type authFile struct {
	Username   string `json:"username"`
	Salt       string `json:"salt"` // base64
	Hash       string `json:"hash"` // base64 PBKDF2 output
	Iter       int    `json:"iter"`
	Secret     string `json:"secret"`                // base64 cookie-signing key
	TOTPSecret string `json:"totp_secret,omitempty"` // base32; empty = 2FA off
	TOTPLast   int64  `json:"totp_last,omitempty"`   // last accepted step (replay guard)
}

var errInvalidLogin = errors.New("invalid username or password")

type Auth struct {
	mu   sync.RWMutex
	path string
	a    *authFile
}

func NewAuth(path string) (*Auth, error) {
	au := &Auth{path: path}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		var f authFile
		if json.Unmarshal(b, &f) != nil || f.Username == "" {
			// Present but unparseable: refuse to start rather than silently fall
			// back to first-run setup (which any LAN device could then claim).
			// Fail closed. Inspect or remove the file to deliberately re-run setup.
			return nil, fmt.Errorf("credential file %s is present but corrupt/empty; refusing to start (fail closed) — inspect or remove it to re-run setup", path)
		}
		au.a = &f
	case errors.Is(err, os.ErrNotExist):
		// not configured yet (first run)
	default:
		return nil, err
	}
	return au, nil
}

func (au *Auth) Configured() bool {
	au.mu.RLock()
	defer au.mu.RUnlock()
	return au.a != nil
}

// TOTPEnabled reports whether the account requires an authenticator code.
func (au *Auth) TOTPEnabled() bool {
	au.mu.RLock()
	defer au.mu.RUnlock()
	return au.a != nil && au.a.TOTPSecret != ""
}

// Setup creates the single account. If totpSecret is non-empty, two-factor auth
// is enabled — but only after totpCode validates against it, so a mis-scanned or
// mistyped secret can never lock the user out of their own appliance.
func (au *Auth) Setup(user, pass, totpSecret, totpCode string) error {
	au.mu.Lock()
	defer au.mu.Unlock()
	if au.a != nil {
		return errors.New("already configured")
	}
	if user == "" {
		return errors.New("username required")
	}
	if len(pass) < minPass {
		return errors.New("password must be at least 8 characters")
	}
	var totpLast int64
	if totpSecret != "" {
		step, ok := totpVerify(totpSecret, totpCode, time.Now().Unix(), 0)
		if !ok {
			return errors.New("authenticator code didn't match — rescan the QR and try again")
		}
		totpLast = step // burn the confirmation step so it can't be replayed at first login
	}
	salt, secret := randBytes(16), randBytes(32)
	f := &authFile{
		Username:   user,
		Salt:       b64(salt),
		Hash:       b64(pbkdf2SHA256([]byte(pass), salt, pbkdf2Iter, 32)),
		Iter:       pbkdf2Iter,
		Secret:     b64(secret),
		TOTPSecret: totpSecret,
		TOTPLast:   totpLast,
	}
	au.a = f
	if err := au.saveLocked(); err != nil {
		au.a = nil
		return err
	}
	return nil
}

// Login verifies the password and, when 2FA is enabled, the TOTP code (with
// replay protection). It returns nil only when every required factor passes; the
// error is deliberately generic so it never reveals which factor failed.
func (au *Auth) Login(user, pass, code string) error {
	au.mu.Lock()
	defer au.mu.Unlock()
	f := au.a
	if f == nil {
		return errInvalidLogin
	}
	salt, _ := base64.StdEncoding.DecodeString(f.Salt)
	want, _ := base64.StdEncoding.DecodeString(f.Hash)
	got := pbkdf2SHA256([]byte(pass), salt, f.Iter, len(want))
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(f.Username)) == 1
	passOK := subtle.ConstantTimeCompare(got, want) == 1
	if !(userOK && passOK) {
		return errInvalidLogin
	}
	if f.TOTPSecret != "" {
		step, ok := totpVerify(f.TOTPSecret, code, time.Now().Unix(), f.TOTPLast)
		if !ok {
			return errInvalidLogin
		}
		f.TOTPLast = step   // burn this step so the code can't be replayed
		_ = au.saveLocked() // best-effort persist; a failed write just allows one replay
	}
	return nil
}

// saveLocked writes the credential file atomically and durably (0600): write a
// temp file in the same dir, fsync, then rename over the target. A crash (the
// board is reboot-prone, and login rewrites this to bump TOTPLast) thus leaves
// either the old or the new file intact — never a truncated, unparseable one.
// Caller holds au.mu.
func (au *Auth) saveLocked() error {
	b, _ := json.MarshalIndent(au.a, "", "  ")
	tmp := au.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, au.path)
}

func (au *Auth) issue(w http.ResponseWriter) {
	au.mu.RLock()
	f := au.a
	au.mu.RUnlock()
	if f == nil {
		return
	}
	secret, _ := base64.StdEncoding.DecodeString(f.Secret)
	msg := f.Username + "|" + strconv.FormatInt(time.Now().Add(sessionTTL).Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(msg))
	tok := base64.RawURLEncoding.EncodeToString([]byte(msg)) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: tok, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds()),
		// Secure: true once we serve over HTTPS (see README).
	})
}

func (au *Auth) valid(r *http.Request) bool {
	au.mu.RLock()
	f := au.a
	au.mu.RUnlock()
	if f == nil {
		return false
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return false
	}
	msg, err1 := base64.RawURLEncoding.DecodeString(parts[0])
	sig, err2 := base64.RawURLEncoding.DecodeString(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	secret, _ := base64.StdEncoding.DecodeString(f.Secret)
	mac := hmac.New(sha256.New, secret)
	mac.Write(msg)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return false
	}
	fields := strings.SplitN(string(msg), "|", 2)
	if len(fields) != 2 || fields[0] != f.Username {
		return false
	}
	exp, err := strconv.ParseInt(fields[1], 10, 64)
	return err == nil && time.Now().Unix() < exp
}

func (au *Auth) clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
}

func (au *Auth) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !au.valid(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}
func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// pbkdf2SHA256 implements PBKDF2-HMAC-SHA256 (RFC 2898) using only the stdlib.
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	hashLen := sha256.Size
	blocks := (keyLen + hashLen - 1) / hashLen
	dk := make([]byte, 0, blocks*hashLen)
	idx := make([]byte, 4)
	for block := 1; block <= blocks; block++ {
		prf := hmac.New(sha256.New, password)
		prf.Write(salt)
		idx[0], idx[1], idx[2], idx[3] = byte(block>>24), byte(block>>16), byte(block>>8), byte(block)
		prf.Write(idx)
		u := prf.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for j := range t {
				t[j] ^= u[j]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLen]
}
