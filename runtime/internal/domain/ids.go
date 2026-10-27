// Package domain holds identifiers, enums, error codes and small value types
// shared by every runtime layer. It must not import adapter or transport code.
package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
)

// NewID returns a server-generated opaque identifier. The prefix only helps
// humans read logs; callers must never parse it (contract §1.1).
func NewID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("samsara: crypto/rand failed: " + err.Error())
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

// NewSecret returns a high-entropy random token suitable for one-time codes,
// launch tickets and UI bearer credentials.
func NewSecret() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("samsara: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// HashSecret is the only form in which secrets are persisted.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Digest returns the canonical sha256 digest of a JSON-serialisable value.
// encoding/json sorts map keys, which makes map inputs canonical.
func Digest(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic("samsara: digest marshal: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// DigestBytes hashes raw bytes with the same prefix convention as Digest.
func DigestBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Dec renders an int64 counter as the decimal string required by the
// contract so that browsers never coerce it into a JS Number.
func Dec(n int64) string { return strconv.FormatInt(n, 10) }

// ParseDec parses a decimal counter string. Empty means zero.
func ParseDec(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.ParseInt(s, 10, 64)
}
