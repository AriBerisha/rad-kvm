package main

import "testing"

// RFC 4226 §D and RFC 6238 §B use the ASCII seed "12345678901234567890".
func rfcSecret() string { return b32.EncodeToString([]byte("12345678901234567890")) }

// TestHOTPVectors checks the RFC 4226 Appendix D 6-digit HOTP values.
func TestHOTPVectors(t *testing.T) {
	want := []string{"755224", "287082", "359152", "969429", "338314",
		"254676", "287922", "162583", "399871", "520489"}
	s := rfcSecret()
	for i, w := range want {
		got, ok := totpCode(s, uint64(i))
		if !ok || got != w {
			t.Errorf("HOTP counter %d: got %q ok=%v, want %q", i, got, ok, w)
		}
	}
}

// TestTOTPVectors checks RFC 6238 Appendix B (SHA1), reduced to 6 digits (the
// last 6 of the published 8-digit codes — n%1e8 then %1e6 == n%1e6).
func TestTOTPVectors(t *testing.T) {
	s := rfcSecret()
	cases := []struct {
		unix int64
		code string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	for _, c := range cases {
		got, _ := totpCode(s, uint64(c.unix/totpPeriod))
		if got != c.code {
			t.Errorf("TOTP t=%d: got %q want %q", c.unix, got, c.code)
		}
	}
}

func TestVerifyWindowAndReplay(t *testing.T) {
	s := newTOTPSecret()
	now := int64(1700000000)
	cur, _ := totpCode(s, uint64(now/totpPeriod))

	step, ok := totpVerify(s, cur, now, 0)
	if !ok {
		t.Fatal("current code should verify")
	}
	if _, ok := totpVerify(s, cur, now, step); ok {
		t.Error("replay of the same code must be rejected once lastStep is set")
	}
	prev, _ := totpCode(s, uint64(now/totpPeriod)-1)
	if _, ok := totpVerify(s, prev, now, 0); !ok {
		t.Error("previous-step code should be accepted within ±1 skew")
	}
	future2, _ := totpCode(s, uint64(now/totpPeriod)+2)
	if _, ok := totpVerify(s, future2, now, 0); ok {
		t.Error("a code two steps ahead is outside the window and must be rejected")
	}
	if _, ok := totpVerify(s, "12345", now, 0); ok {
		t.Error("malformed (non-6-digit) code must be rejected")
	}
}

func TestSecretUniqueness(t *testing.T) {
	a, b := newTOTPSecret(), newTOTPSecret()
	if a == b {
		t.Fatal("two generated secrets must differ (per-device requirement)")
	}
	if len(a) != 32 { // 20 bytes -> 32 base32 chars (no padding)
		t.Errorf("secret length = %d, want 32", len(a))
	}
}
