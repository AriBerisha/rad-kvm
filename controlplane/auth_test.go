package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestAuth(t *testing.T) (*Auth, string) {
	p := filepath.Join(t.TempDir(), "auth.json")
	au, err := NewAuth(p)
	if err != nil {
		t.Fatal(err)
	}
	return au, p
}

func codeNow(secret string) string {
	c, _ := totpCode(secret, uint64(time.Now().Unix()/totpPeriod))
	return c
}

// freshCode returns a code from the NEXT step (still inside the +1 skew window),
// i.e. one that differs from the step just consumed at setup — what a real login
// in a later moment would use.
func freshCode(secret string) string {
	c, _ := totpCode(secret, uint64(time.Now().Unix()/totpPeriod+1))
	return c
}

func TestSetupAndLoginNoTOTP(t *testing.T) {
	au, _ := newTestAuth(t)
	if err := au.Setup("admin", "password1", "", ""); err != nil {
		t.Fatal(err)
	}
	if au.TOTPEnabled() {
		t.Error("TOTP should be off")
	}
	if err := au.Login("admin", "password1", ""); err != nil {
		t.Errorf("valid login failed: %v", err)
	}
	if err := au.Login("admin", "wrong", ""); err == nil {
		t.Error("wrong password should fail")
	}
}

func TestSetupRequiresValidCodeToEnable2FA(t *testing.T) {
	au, _ := newTestAuth(t)
	s := newTOTPSecret()
	if err := au.Setup("admin", "password1", s, "000000"); err == nil {
		t.Fatal("setup with a wrong confirm code must fail — the lockout guard")
	}
	if au.Configured() {
		t.Fatal("no account should exist after a failed 2FA confirm")
	}
	if err := au.Setup("admin", "password1", s, codeNow(s)); err != nil {
		t.Fatalf("setup with correct code failed: %v", err)
	}
	if !au.TOTPEnabled() {
		t.Error("TOTP should be enabled")
	}
}

func TestLoginWithTOTP(t *testing.T) {
	au, _ := newTestAuth(t)
	s := newTOTPSecret()
	if err := au.Setup("admin", "password1", s, codeNow(s)); err != nil {
		t.Fatal(err)
	}
	if err := au.Login("admin", "password1", ""); err == nil {
		t.Error("login without a code must fail when 2FA is on")
	}
	code := freshCode(s) // a code not already consumed by setup
	if err := au.Login("admin", "password1", code); err != nil {
		t.Fatalf("valid 2FA login failed: %v", err)
	}
	if err := au.Login("admin", "password1", code); err == nil {
		t.Error("replaying the same code must be rejected")
	}
	if err := au.Login("admin", "wrong", codeNow(s)); err == nil {
		t.Error("wrong password must fail even with a valid code")
	}
}

// The code typed to confirm 2FA at setup is an acceptance; it must not also work
// to log in (review finding: the setup step must be burned into TOTPLast).
func TestSetupConfirmCodeNotReplayableAtLogin(t *testing.T) {
	au, _ := newTestAuth(t)
	s := newTOTPSecret()
	code := codeNow(s)
	if err := au.Setup("admin", "password1", s, code); err != nil {
		t.Fatal(err)
	}
	if err := au.Login("admin", "password1", code); err == nil {
		t.Error("the setup confirmation code must not be reusable to log in")
	}
}

// A present-but-corrupt credential file must fail closed (error), never silently
// revert to first-run setup, which any LAN device could then claim.
func TestCorruptAuthFileFailsClosed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(p, []byte("{ truncated json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewAuth(p); err == nil {
		t.Error("corrupt credential file must fail closed, not revert to unconfigured")
	}
	if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewAuth(p); err == nil {
		t.Error("credential file with no username must fail closed")
	}
	au, err := NewAuth(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil || au.Configured() {
		t.Error("an absent file is first-run: no error, not configured")
	}
}

func TestTOTPReplayGuardSurvivesReload(t *testing.T) {
	au, p := newTestAuth(t)
	s := newTOTPSecret()
	if err := au.Setup("admin", "password1", s, codeNow(s)); err != nil {
		t.Fatal(err)
	}
	code := freshCode(s)
	if err := au.Login("admin", "password1", code); err != nil {
		t.Fatal(err)
	}
	au2, err := NewAuth(p) // simulate a service restart
	if err != nil {
		t.Fatal(err)
	}
	if !au2.TOTPEnabled() {
		t.Fatal("2FA state should persist across restart")
	}
	if err := au2.Login("admin", "password1", code); err == nil {
		t.Error("replay must still be rejected after restart (TOTPLast persisted)")
	}
}
