// Package auth holds the small cryptographic primitives for Deep Reader's
// single-user authentication: password hashing (bcrypt) and opaque session
// tokens (random value + SHA-256 lookup hash).
//
// Design notes:
//   - Passwords are hashed with bcrypt at the default cost — strong, salted, and
//     pragmatic ("криптостойкий, без паранойи"). The bcrypt hash is the only
//     password material persisted; the plaintext is never stored.
//   - A session token is a 256-bit random value handed to the client as a
//     Bearer token. Only its SHA-256 hash is persisted, so a leak of the DB does
//     not expose usable tokens (the same reason we hash passwords). Lookups are
//     by the hash; the comparison is a plain map/row lookup since the stored
//     value is itself a hash of a high-entropy secret.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// sessionTokenBytes is the number of random bytes in a session token before
// base64url encoding. 32 bytes = 256 bits of entropy.
const sessionTokenBytes = 32

// HashPassword returns the bcrypt hash of plain at the default cost. The result
// is safe to persist; it embeds the salt and cost parameters.
func HashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("auth: hash password: %w", err)
	}
	return string(h), nil
}

// VerifyPassword reports whether plain matches the bcrypt hash. It returns false
// for any mismatch or malformed hash; bcrypt's comparison is constant-time with
// respect to the password.
func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// NewSessionToken generates a fresh, URL-safe random session token to hand to a
// client. The caller stores only HashToken(token); the plaintext is returned to
// the client exactly once.
func NewSessionToken() (string, error) {
	buf := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken returns the hex-encoded SHA-256 of a session token. This is the
// value persisted and looked up server-side, so a stolen database row cannot be
// replayed as a Bearer token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
