// Password hashing with argon2id, encoded in PHC string format so parameters
// can evolve without breaking stored hashes.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// Parameters are OWASP's argon2id option of 19 MiB and two passes. Each
// hash holds its memory until it finishes, and the earlier 64 MiB took an
// idle panel from about 21 MiB to 86 MiB on every login. Hashes made with
// other parameters still verify, and NeedsRehash finds them.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024 // 19 MiB
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// hashSlots caps how many hashes run at once. Login is open to anyone, so
// without a cap N attempts from N addresses would hold N hashes' memory at
// the same time. Further attempts wait for a slot, until their request ends
// or hashWait passes.
var hashSlots = make(chan struct{}, 2)

var hashWait = 10 * time.Second

// ErrHashBusy means every hash slot stayed taken for hashWait.
var ErrHashBusy = errors.New("too many password checks at once")

func idKey(ctx context.Context, password, salt []byte, passes, memory uint32, threads uint8) ([]byte, error) {
	wait := time.NewTimer(hashWait)
	defer wait.Stop()
	select {
	case hashSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-wait.C:
		return nil, ErrHashBusy
	}
	defer func() { <-hashSlots }()
	return argon2.IDKey(password, salt, passes, memory, threads, argonKeyLen), nil
}

// dummyHash has the current parameters, so verifying against it for an
// account that does not exist takes as long as a real login.
var dummyHash = fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
	argon2.Version, argonMemory, argonTime, argonThreads,
	base64.RawStdEncoding.EncodeToString(make([]byte, argonSaltLen)),
	base64.RawStdEncoding.EncodeToString(make([]byte, argonKeyLen)))

func HashPassword(ctx context.Context, password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := idKey(ctx, []byte(password), salt, argonTime, argonMemory, argonThreads)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

var ErrPasswordMismatch = errors.New("password does not match")

type argonParams struct {
	version      int
	memory, time uint32
	threads      uint8
	salt, key    []byte
}

func parseHash(encoded string) (argonParams, error) {
	var p argonParams
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return p, errors.New("malformed password hash")
	}
	if _, err := fmt.Sscanf(parts[2], "v=%d", &p.version); err != nil || parts[2] != fmt.Sprintf("v=%d", p.version) {
		return p, errors.New("malformed version")
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil ||
		parts[3] != fmt.Sprintf("m=%d,t=%d,p=%d", p.memory, p.time, p.threads) {
		return p, errors.New("malformed parameters")
	}
	// A stored hash sets the cost of checking it, so out-of-range values are
	// refused rather than run. 256 MiB and 10 passes are well above anything
	// Windlass has written.
	if p.time < 1 || p.time > 10 || p.threads < 1 || p.threads > 16 ||
		p.memory < 8*uint32(p.threads) || p.memory > 256*1024 {
		return p, errors.New("password hash parameters out of range")
	}
	var err error
	if p.salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return p, fmt.Errorf("malformed salt: %w", err)
	}
	if p.key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil {
		return p, fmt.Errorf("malformed key: %w", err)
	}
	return p, nil
}

// NeedsRehash reports whether encoded was made with parameters other than
// the current ones, so a login that verified it should store a new hash.
func NeedsRehash(encoded string) bool {
	p, err := parseHash(encoded)
	if err != nil {
		return false
	}
	return p.version != argon2.Version || p.memory != argonMemory || p.time != argonTime ||
		p.threads != argonThreads || len(p.salt) != argonSaltLen || len(p.key) != argonKeyLen
}

// VerifyPassword checks password against an encoded hash in constant time.
func VerifyPassword(ctx context.Context, encoded, password string) error {
	p, err := parseHash(encoded)
	if err != nil {
		return err
	}
	want := p.key
	if len(want) != argonKeyLen {
		return errors.New("malformed key length")
	}
	got, err := idKey(ctx, []byte(password), p.salt, p.time, p.memory, p.threads)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}
