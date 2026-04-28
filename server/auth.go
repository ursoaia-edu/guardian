package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Cost parameters for NEW hashes only. Retuning them is a normal maintenance
// action, and it must never invalidate hashes already in the database — which
// is why every hash carries the parameters it was made with.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// hashPassword returns a PHC-format argon2id hash:
//
//	$argon2id$v=19$m=65536,t=1,p=4$<salt>$<key>
//
// The cost parameters are part of the string, so verification always uses the
// values a given hash was created with. Encoding them is what makes raising the
// cost later a one-line change instead of a migration of every stored password.
func hashPassword(plain string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}
	key := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// verifyPassword recomputes the hash using the parameters stored alongside it,
// never the constants above. A hash in an unknown shape or an unsupported
// argon2 version verifies as false rather than erroring: the only caller is a
// login attempt, and every failure there means the same thing.
func verifyPassword(encoded, plain string) bool {
	// "", "argon2id", "v=19", "m=...,t=...,p=...", salt, key
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}

	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false
	}
	if memory == 0 || time == 0 || threads == 0 {
		return false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}

	got := argon2.IDKey([]byte(plain), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// newToken mints a 32-byte random token, returning the plaintext to hand out
// and the SHA-256 digest to store. The plaintext is never persisted.
func newToken() (plain, hash string) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	plain = hex.EncodeToString(raw)
	return plain, hashToken(plain)
}

func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}
