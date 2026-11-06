package contests

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
	"github.com/ztsubaki/soj/services/api/internal/rpcauth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestPublicLeaderboardPaginationIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	resetProjectionFixture(t, ctx, pool)
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = pool.Exec(cleanup, `DELETE FROM contest_registrations WHERE user_id IN (SELECT id FROM users WHERE username LIKE 'public-page-%')`)
		_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE username LIKE 'public-page-%'`)
	}()
	for _, query := range []string{
		`INSERT INTO users(username,email,password_hash) SELECT 'public-page-'||n,'public-page-'||n||'@soj.local','not-for-login' FROM generate_series(1,120) n`,
		`INSERT INTO contest_registrations(contest_id,user_id) SELECT 9500,id FROM users WHERE username LIKE 'public-page-%'`,
	} {
		if _, err := pool.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	server := NewPublicGRPCServer(NewService(pool, t.TempDir()), nil)
	read := func(page *sojv1.PageRequest, credential string) (*sojv1.GetLeaderboardResponse, error) {
		incoming := metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Bearer "+credential))
		response, err := rpcauth.UnaryInterceptor("agent", "web", "admin")(
			incoming, nil, &grpc.UnaryServerInfo{FullMethod: "/soj.v1.ContestService/GetLeaderboard"},
			func(authenticated context.Context, _ any) (any, error) {
				return server.GetLeaderboard(authenticated, &sojv1.GetLeaderboardRequest{ContestId: 9500, Page: page})
			},
		)
		if err != nil {
			return nil, err
		}
		return response.(*sojv1.GetLeaderboardResponse), nil
	}
	first, err := read(nil, "web")
	if err != nil {
		t.Fatal(err)
	}
	if first.GetPage().GetTotal() != 122 || first.GetPage().GetPageSize() != 20 || len(first.Items) != 20 || first.Items[0].GetUser().GetId() != 9502 {
		t.Fatalf("default page = %v", first)
	}
	second, err := read(&sojv1.PageRequest{Page: proto.Int32(2), PageSize: proto.Int32(1)}, "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].GetUser().GetId() != 9501 || second.Items[0].GetRank() != 2 || second.Items[0].GetScore() != "7" {
		t.Fatalf("second page must retain global rank and score: %v", second)
	}
	last, err := read(&sojv1.PageRequest{Page: proto.Int32(2), PageSize: proto.Int32(100)}, "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Items) != 22 || last.GetPage().GetTotal() != 122 {
		t.Fatalf("last page = %v", last)
	}
	if err := server.service.AdminDisqualify(ctx, 1, 9500, 9502, true); err != nil {
		t.Fatal(err)
	}
	filtered, err := read(&sojv1.PageRequest{Page: proto.Int32(1), PageSize: proto.Int32(1)}, "web")
	if err != nil {
		t.Fatal(err)
	}
	if filtered.GetPage().GetTotal() != 121 || len(filtered.Items) != 1 || filtered.Items[0].GetUser().GetId() != 9501 || filtered.Items[0].GetRank() != 1 {
		t.Fatalf("public filtering must precede ranking and counting: %v", filtered)
	}
	if _, err := read(nil, "admin"); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("admin BFF caller error = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE contests SET visible=false WHERE id=9500`); err != nil {
		t.Fatal(err)
	}
	if _, err := read(nil, "web"); status.Code(err) != codes.NotFound {
		t.Fatalf("hidden contest error = %v", err)
	}
}
