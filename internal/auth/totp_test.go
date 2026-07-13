package auth

import (
	"strings"
	"testing"
	"time"
)

// RFC 6238 Appendix B test vectors (SHA-1, 8 digits truncated to 6 here we
// use our own known-answer instead: verify determinism + skew behavior).
func TestTOTPRoundTrip(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)

	code, err := totpCode(secret, now)
	if err != nil || len(code) != 6 {
		t.Fatalf("code = %q, %v", code, err)
	}

	if !VerifyTOTP(secret, code, now) {
		t.Error("current code rejected")
	}
	// Within one step of skew both ways.
	if !VerifyTOTP(secret, code, now.Add(25*time.Second)) {
		t.Error("code rejected within skew window")
	}
	// Far outside the window.
	if VerifyTOTP(secret, code, now.Add(5*time.Minute)) {
		t.Error("stale code accepted")
	}
	if VerifyTOTP(secret, "000000", now) && code != "000000" {
		t.Error("wrong code accepted")
	}
	if VerifyTOTP(secret, "12345", now) {
		t.Error("short code accepted")
	}
}

// Known-answer test against the RFC 6238 SHA-1 test key at T=59s
// (counter=1): full HOTP value is 94287082 → 6-digit TOTP "287082".
func TestTOTPKnownAnswer(t *testing.T) {
	// "12345678901234567890" in base32.
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	code, err := totpCode(secret, time.Unix(59, 0))
	if err != nil {
		t.Fatal(err)
	}
	if code != "287082" {
		t.Errorf("code = %s, want 287082", code)
	}
}

func TestTOTPAuthURL(t *testing.T) {
	url := TOTPAuthURL("ABC234", "admin@example.com")
	for _, want := range []string{"otpauth://totp/", "secret=ABC234", "issuer=Windlass"} {
		if !strings.Contains(url, want) {
			t.Errorf("url %q missing %q", url, want)
		}
	}
}
