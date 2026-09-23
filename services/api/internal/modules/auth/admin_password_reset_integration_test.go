package auth

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAdminResetUserPasswordIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	const (
		adminID    int64 = 92001
		targetName       = "password-reset-target"
	)
	for _, id := range []int64{adminID} {
		if _, err := pool.Exec(ctx, "DELETE FROM sessions WHERE user_id=$1", id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, id := range []int64{adminID} {
			_, _ = pool.Exec(context.Background(), "DELETE FROM sessions WHERE user_id=$1", id)
			_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", id)
		}
	})
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,username,email,password_hash,role,status)
		OVERRIDING SYSTEM VALUE VALUES($1,'password-reset-admin','password-reset-admin@example.invalid','not-for-login','admin','active')`, adminID); err != nil {
		t.Fatal(err)
	}
	service, err := NewPostgresService(ctx, pool, t.TempDir(), time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	actor := User{ID: adminID, Role: RoleAdmin}
	target, err := service.AdminCreateUser(ctx, actor, targetName, "password-reset-target@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if target.ID <= 0 {
		t.Fatalf("target id = %d, want positive ID", target.ID)
	}
	first, err := service.AdminResetUserPassword(ctx, actor, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 8 {
		t.Fatalf("generated password length = %d, want >= 8", len(first))
	}
	_, token, _, err := service.LoginSession(ctx, targetName, first)
	if err != nil {
		t.Fatalf("login with reset password: %v", err)
	}
	second, err := service.AdminResetUserPassword(ctx, actor, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("password reset reused a password")
	}
	if _, ok := service.CurrentUser(ctx, token); ok {
		t.Fatal("password reset did not revoke the target's sessions")
	}
	if _, _, _, err := service.LoginSession(ctx, targetName, first); err == nil {
		t.Fatal("previous reset password was accepted")
	}
	if _, _, _, err := service.LoginSession(ctx, targetName, second); err != nil {
		t.Fatalf("login with latest reset password: %v", err)
	}
}
