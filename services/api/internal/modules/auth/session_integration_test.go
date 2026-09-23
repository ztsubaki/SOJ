package auth

import (
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"sync"
	"testing"
	"time"
)

func TestSessionRotationIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	service, err := NewPostgresService(ctx, pool, t.TempDir(), time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	username := "rotation-test"
	if _, err = pool.Exec(ctx, `DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE username=$1)`, username); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, username); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := service.SendRegistrationEmailVerification(ctx, username, "rotation@example.invalid"); err != nil {
		t.Fatal(err)
	}
	code := service.emailVerifications["rotation@example.invalid"].code
	u, err := service.Register(ctx, username, "rotation@example.invalid", "old-password", code)
	if err != nil {
		t.Fatal(err)
	}
	const alternateEmail = "rotation-secondary@example.invalid"
	if _, err := service.issueRegistrationEmailVerification(alternateEmail); err != nil {
		t.Fatal(err)
	}
	secondaryCode := service.emailVerifications[alternateEmail].code
	if _, err := service.Register(ctx, username, alternateEmail, "old-password", secondaryCode); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("duplicate username registration = %v, want ErrUsernameTaken", err)
	}
	if verification := service.emailVerifications[alternateEmail]; verification.code != "" {
		t.Fatal("duplicate username registration did not consume the verification code")
	}
	usernameAvailable, emailAvailable, resendAfter, err := service.SendRegistrationEmailVerification(ctx, username, "rotation@example.invalid")
	if err != nil || usernameAvailable || emailAvailable || resendAfter != 0 {
		t.Fatalf("occupied identifiers = (%t, %t, %d, %v), want (false, false, 0, nil)", usernameAvailable, emailAvailable, resendAfter, err)
	}
	_, old, _, err := service.LoginSession(ctx, username, "old-password")
	if err != nil {
		t.Fatal(err)
	}
	_, other, _, err := service.LoginSession(ctx, username, "old-password")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	tokens := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next, _, err := service.RefreshSession(ctx, old)
			if err == nil {
				tokens <- next
			}
		}()
	}
	wg.Wait()
	close(tokens)
	var rotated string
	count := 0
	for token := range tokens {
		rotated = token
		count++
	}
	if count != 1 {
		t.Fatalf("successful rotations=%d, want 1", count)
	}
	if _, ok := service.CurrentUser(ctx, old); ok {
		t.Fatal("old token still valid")
	}
	if _, ok := service.CurrentUser(ctx, other); !ok {
		t.Fatal("independent login revoked")
	}
	if err = service.ChangePassword(ctx, rotated, "old-password", "new-password"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{other, rotated} {
		if _, ok := service.CurrentUser(ctx, token); ok {
			t.Fatal("password change did not revoke all sessions")
		}
	}
	if _, _, _, err = service.LoginSession(ctx, username, "old-password"); err == nil {
		t.Fatal("old password accepted")
	}
	_, next, _, err := service.LoginSession(ctx, username, "new-password")
	if err != nil {
		t.Fatal(err)
	}
	hash, ok := sessionTokenHash(next)
	if !ok {
		t.Fatal("invalid generated session token")
	}
	if _, err = pool.Exec(ctx, `UPDATE sessions SET expires_at=now()-interval '1 second' WHERE token_hash=$1`, hash); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.RefreshSession(ctx, next); err == nil {
		t.Fatal("expired session renewed")
	}
	_, next, _, err = service.LoginSession(ctx, username, "new-password")
	if err != nil {
		t.Fatal(err)
	}
	disabled := "disabled"
	if _, err = service.AdminUpdateUser(ctx, User{ID: 1}, u.ID, AdminUserPatch{Status: &disabled}); err != nil {
		t.Fatal(err)
	}
	active := "active"
	if _, err = service.AdminUpdateUser(ctx, User{ID: 1}, u.ID, AdminUserPatch{Status: &active}); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.CurrentUser(ctx, next); ok {
		t.Fatal("reenabling restored revoked session")
	}
	if _, err = service.AdminUpdateUser(ctx, u, u.ID, AdminUserPatch{Status: &disabled}); err != ErrNotPermitted {
		t.Fatalf("self-disable=%v", err)
	}
}
