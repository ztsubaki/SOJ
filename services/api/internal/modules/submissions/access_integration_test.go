package submissions

import (
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestContestSubmissionOwnershipIntegration(t *testing.T) {
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
	const submissionID int64 = 9100
	const artifactID int64 = 9100
	if _, err := pool.Exec(ctx, "DELETE FROM submissions WHERE id=$1", submissionID); err != nil {
		t.Fatalf("clear submission: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM artifacts WHERE id=$1", artifactID); err != nil {
		t.Fatalf("clear artifact: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM submissions WHERE id=$1", submissionID)
		_, _ = pool.Exec(ctx, "DELETE FROM artifacts WHERE id=$1", artifactID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO artifacts(id,object_key,kind,state,size_bytes,sha256,content_type,available_at)
		OVERRIDING SYSTEM VALUE VALUES($1,$2,'submission_source','available',4,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','text/plain',now())`, artifactID, "9100"); err != nil {
		t.Fatalf("create source artifact: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO submissions(id,user_id,problem_id,contest_id,kind,source_artifact_id,source_filename,source_content_type)
		OVERRIDING SYSTEM VALUE VALUES($1,2,100,100,'contest',$2,'owned.go','text/plain')`, submissionID, artifactID); err != nil {
		t.Fatalf("create owned submission: %v", err)
	}

	service := NewService(pool, t.TempDir(), 1)
	if item, err := service.Get(ctx, submissionID, 2, false); err != nil || item.ID != submissionID {
		t.Fatalf("owner Get() = (%+v, %v), want owned submission", item, err)
	}
	if _, err := service.Get(ctx, submissionID, 1, false); err != ErrNotFound {
		t.Fatalf("non-owner Get() error = %v, want %v", err, ErrNotFound)
	}
	if meta, err := service.SourceMetadata(ctx, submissionID, 2, false); err != nil || meta.ID != artifactID {
		t.Fatalf("owner SourceMetadata() = (%+v, %v), want owned source", meta, err)
	}
	if _, err := service.SourceMetadata(ctx, submissionID, 1, false); err != ErrNotFound {
		t.Fatalf("non-owner SourceMetadata() error = %v, want %v", err, ErrNotFound)
	}
	items, total, err := service.ListMine(ctx, 99999, nil, nil, nil, 1, 100)
	if err != nil {
		t.Fatalf("ListMine(non-owner): %v", err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("ListMine(non-owner) = (%d items, total=%d), want empty", len(items), total)
	}
}
