package auth

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ztsubaki/soj/services/api/internal/rpcauth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestProtectedAccessIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer pool.Close()

	const (
		adminID   int64 = 91001
		userID    int64 = 91002
		expiredID int64 = 91003
	)
	for _, id := range []int64{adminID, userID, expiredID} {
		if _, err := pool.Exec(ctx, "DELETE FROM sessions WHERE user_id=$1", id); err != nil {
			t.Fatalf("clear sessions for %d: %v", id, err)
		}
		if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id=$1", id); err != nil {
			t.Fatalf("clear user %d: %v", id, err)
		}
	}
	t.Cleanup(func() {
		for _, id := range []int64{adminID, userID, expiredID} {
			_, _ = pool.Exec(context.Background(), "DELETE FROM sessions WHERE user_id=$1", id)
			_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", id)
		}
	})

	for _, user := range []struct {
		id             int64
		username, role string
	}{
		{adminID, "accessintegrationadmin", RoleAdmin},
		{userID, "accessintegrationchange", RoleUser},
		{expiredID, "accessintegrationexpired", RoleUser},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,username,email,password_hash,role,status)
			OVERRIDING SYSTEM VALUE VALUES($1,$2,$2 || '@example.invalid','$2a$10$unusedunusedunusedunusedunusedunusedunusedunusedunused', $3, 'active')`, user.id, user.username, user.role); err != nil {
			t.Fatalf("create %d: %v", user.id, err)
		}
	}

	service, err := NewPostgresService(ctx, pool, t.TempDir(), time.Hour, nil)
	if err != nil {
		t.Fatalf("new PostgreSQL auth service: %v", err)
	}
	adminToken := createAccessSession(t, service, User{ID: adminID})
	userToken := createAccessSession(t, service, User{ID: userID})
	expiredToken := createAccessSession(t, service, User{ID: expiredID})

	adminContext := accessRPCContext(t, "admin-bff", adminToken)
	if user, err := RequireAdmin(adminContext, service); err != nil || user.ID != adminID {
		t.Fatalf("RequireAdmin(admin BFF) = (%+v, %v), want active admin", user, err)
	}
	if _, err := RequireAdmin(accessRPCContext(t, "web-bff", adminToken), service); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("RequireAdmin(web BFF) code = %v, want %v", status.Code(err), codes.PermissionDenied)
	}
	if _, err := RequireCurrentUser(accessRPCContext(t, "web-bff", ""), service); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("RequireCurrentUser(without session) code = %v, want %v", status.Code(err), codes.Unauthenticated)
	}

	if user, err := RequireCurrentUser(accessRPCContext(t, "web-bff", userToken), service); err != nil || user.ID != userID {
		t.Fatalf("RequireCurrentUser(active user) = (%+v, %v), want active user", user, err)
	}

	if _, err := pool.Exec(ctx, "UPDATE users SET status='disabled' WHERE id=$1", userID); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	if _, err := RequireCurrentUser(accessRPCContext(t, "web-bff", userToken), service); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("RequireCurrentUser(disabled user) code = %v, want %v", status.Code(err), codes.Unauthenticated)
	}
	hash, ok := sessionTokenHash(expiredToken)
	if !ok {
		t.Fatal("invalid generated session token")
	}
	if _, err := pool.Exec(ctx, "UPDATE sessions SET expires_at=now()-interval '1 minute' WHERE token_hash=$1", hash); err != nil {
		t.Fatalf("expire session: %v", err)
	}
	if _, err := RequireCurrentUser(accessRPCContext(t, "web-bff", expiredToken), service); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("RequireCurrentUser(expired session) code = %v, want %v", status.Code(err), codes.Unauthenticated)
	}
}

func createAccessSession(t *testing.T, service *Service, user User) string {
	t.Helper()
	token, _, err := service.CreateSession(t.Context(), user)
	if err != nil {
		t.Fatalf("create session for %d: %v", user.ID, err)
	}
	return token
}

func accessRPCContext(t *testing.T, bffToken, sessionToken string) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	t.Cleanup(cancel)
	pairs := []string{"authorization", "Bearer " + bffToken}
	if sessionToken != "" {
		pairs = append(pairs, "x-soj-session", sessionToken)
	}
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(pairs...))
	interceptor := rpcauth.UnaryInterceptor("judge-agent", "web-bff", "admin-bff")
	var authenticated context.Context
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/soj.v1.SubmissionService/ListMySubmissions"}, func(ctx context.Context, _ any) (any, error) {
		authenticated = ctx
		return nil, nil
	})
	if err != nil {
		t.Fatalf("authenticate BFF request: %v", err)
	}
	return authenticated
}
