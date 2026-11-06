package contests

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAdminListSingleConnectionIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx := context.Background()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	resetProjectionFixture(t, ctx, pool)
	for _, statement := range []string{
		`DELETE FROM contest_registrations WHERE contest_id IN (9600,9601,9602)`,
		`DELETE FROM contest_problems WHERE contest_id IN (9600,9601,9602)`,
		`DELETE FROM contests WHERE id IN (9600,9601,9602)`,
		`INSERT INTO contests(id,title,description,starts_at,ends_at,visible,scoring_policy,created_by) OVERRIDING SYSTEM VALUE VALUES (9600,'admin-list-single-connection first','',now()+interval '3 hours',now()+interval '4 hours',true,'best',1),(9601,'admin-list-single-connection second','',now()+interval '2 hours',now()+interval '4 hours',false,'latest',1),(9602,'admin-list-single-connection empty','',now()+interval '1 hour',now()+interval '4 hours',true,'best',1)`,
		`INSERT INTO contest_problems(contest_id,problem_id,ordinal) VALUES (9600,9502,1),(9600,9501,2),(9601,9501,1)`,
		`INSERT INTO contest_registrations(contest_id,user_id) VALUES (9600,9501),(9600,9502)`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(pool, t.TempDir())
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	items, total, err := service.AdminList(callCtx, "admin-list-single-connection", "upcoming", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(items) != 2 {
		t.Fatalf("first page = total %d, items %#v", total, items)
	}
	for index, item := range items {
		if item.ID != int64(9600+index) || item.Status != "upcoming" {
			t.Fatalf("contest %d = %#v", index, item)
		}
		wantProblems := [][]int64{{9502, 9501}, {9501}}[index]
		if !reflect.DeepEqual(problemIDs(item.Problems), wantProblems) {
			t.Fatalf("contest %d problems = %#v, want %v", item.ID, item.Problems, wantProblems)
		}
		detail, err := service.AdminGet(callCtx, item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(item, detail) {
			t.Fatalf("list contest = %#v, detail = %#v", item, detail)
		}
	}
	if items[0].ParticipantCount != 2 || items[1].ParticipantCount != 0 {
		t.Fatalf("participant counts = %d, %d", items[0].ParticipantCount, items[1].ParticipantCount)
	}
	items, total, err = service.AdminList(callCtx, "admin-list-single-connection", "upcoming", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(items) != 1 || items[0].ID != 9602 || items[0].Problems == nil || len(items[0].Problems) != 0 {
		t.Fatalf("second page = total %d, items %#v", total, items)
	}
	items, total, err = service.AdminList(callCtx, "admin-list-single-connection", "upcoming", 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(items) != 0 {
		t.Fatalf("page past end = total %d, items %#v", total, items)
	}
}
