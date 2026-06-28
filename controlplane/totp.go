package main

// TOTP (RFC 6238) two-factor auth, stdlib only. Interoperates with Google
// Authenticator and any other RFC 6238 app: HMAC-SHA1, 6 digits, 30s period,
// 160-bit secret. The secret is generated per device with crypto/rand at setup
// time — it is never embedded in the image, so two boards never share a seed.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
)

const (
	totpDigits = 6
	totpPeriod = 30 // seconds per step
	totpSkew   = 1  // accept ±1 step to tolerate clock drift
	totpIssuer = "RAD-KVM"
)

// b32 is RFC 4648 base32, uppercase, no padding — the form authenticator apps
// expect when you type a key by hand.
var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret returns a fresh 160-bit secret, base32-encoded. Per device,
// from crypto/rand — never baked into the image.
func newTOTPSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return b32.EncodeToString(b)
}

// totpCode computes the RFC 4226/6238 code for a secret and step counter.
func totpCode(secret string, counter uint64) (string, bool) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil || len(key) == 0 {
		return "", false
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := uint32(sum[off]&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	return fmt.Sprintf("%0*d", totpDigits, bin%pow10(totpDigits)), true
}

func pow10(n int) uint32 {
	p := uint32(1)
	for i := 0; i < n; i++ {
		p *= 10
	}
	return p
}

// totpVerify checks code against secret at unixTime, accepting ±totpSkew steps
// for clock drift, but only a step strictly greater than lastStep — so a code
// (or any earlier code still inside the window) cannot be replayed. Returns the
// matched step counter, which the caller persists as the new lastStep.
func totpVerify(secret, code string, unixTime, lastStep int64) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return 0, false
	}
	now := unixTime / totpPeriod
	for d := int64(-totpSkew); d <= totpSkew; d++ {
		c := now + d
		if c < 0 || c <= lastStep {
			continue
		}
		want, ok := totpCode(secret, uint64(c))
		if !ok {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return c, true
		}
	}
	return 0, false
}

// otpauthURI builds the otpauth:// provisioning URI encoded into the QR code and
// shown for manual entry.
func otpauthURI(user, secret string) string {
	if user == "" {
		user = "admin"
	}
	label := url.PathEscape(totpIssuer + ":" + user)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", totpIssuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// groupSecret formats a base32 secret in 4-char groups for readable manual entry.
func groupSecret(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}
