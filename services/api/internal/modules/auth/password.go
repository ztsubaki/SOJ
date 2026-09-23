package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 1
	argonSaltLength  = 16
	argonKeyLength   = 32
)

// Bound Argon2 work across all authentication operations. Waiting callers
// stop when their RPC deadline expires.
var passwordWorkers = make(chan struct{}, 2)

func acquirePasswordWorker(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case passwordWorkers <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func hashPassword(ctx context.Context, password string) (string, error) {
	if err := acquirePasswordWorker(ctx); err != nil {
		return "", err
	}
	defer func() { <-passwordWorkers }()
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonIterations, argonParallelism, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func verifyPassword(ctx context.Context, encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil || memory < 8 || memory > 1024*1024 || iterations < 1 || iterations > 20 || parallelism < 1 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != argonSaltLength {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) != argonKeyLength {
		return false
	}
	if err := acquirePasswordWorker(ctx); err != nil {
		return false
	}
	defer func() { <-passwordWorkers }()
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, argonKeyLength)
	return subtle.ConstantTimeCompare(want, got) == 1
}

func validPassword(password string) bool {
	if len(password) < 8 || len(password) > 64 {
		return false
	}
	for _, value := range []byte(password) {
		if value < ' ' || value > '~' {
			return false
		}
	}
	return true
}

var errInvalidPasswordHash = errors.New("invalid password hash")

func passwordNeedsRehash(encoded string) bool {
	parts := strings.Split(encoded, "$")
	return len(parts) != 6 || parts[3] != "m=65536,t=3,p=1"
}
