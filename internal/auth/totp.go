package auth

// RFC 6238 TOTP on the standard library — 40 lines beats a dependency
// (principle 10). SHA-1, 6 digits, 30-second steps: the parameters every
// authenticator app supports.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"time"
)

const totpStep = 30 * time.Second

// GenerateTOTPSecret returns a new base32 secret (RFC 4648, no padding).
func GenerateTOTPSecret() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

// TOTPAuthURL renders the otpauth:// URL authenticator apps import.
func TOTPAuthURL(secret, account string) string {
	return "otpauth://totp/Windlass:" + url.PathEscape(account) +
		"?secret=" + secret + "&issuer=Windlass&algorithm=SHA1&digits=6&period=30"
}

func totpCode(secret string, t time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return "", fmt.Errorf("malformed TOTP secret: %w", err)
	}
	counter := uint64(t.Unix()) / uint64(totpStep.Seconds())

	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", code%1_000_000), nil
}

// TOTPCodeForTesting exposes code generation so tests can act as an
// authenticator app. Not used in production paths.
func TOTPCodeForTesting(secret string, t time.Time) (string, error) {
	return totpCode(secret, t)
}

// VerifyTOTP checks a code against the current step ±1 (clock skew).
func VerifyTOTP(secret, code string, now time.Time) bool {
	if len(code) != 6 {
		return false
	}
	for _, skew := range []time.Duration{0, -totpStep, totpStep} {
		want, err := totpCode(secret, now.Add(skew))
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return true
		}
	}
	return false
}
