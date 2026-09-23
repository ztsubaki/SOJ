package artifacts

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCleanupExpiredArtifactsIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const id int64 = 9300
	if _, err = pool.Exec(ctx, `INSERT INTO agent_sessions(id,partition,offline_at,ended_at) VALUES('agent-cleanup','cleanup',now(),now()) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM artifacts WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO artifacts(id,object_key,kind,state,size_bytes,sha256,content_type,created_at,upload_agent_session_id,upload_job_id) OVERRIDING SYSTEM VALUE VALUES($1,'9300','judge_log','pending',3,repeat('a',64),'text/plain',now()-interval '2 hours','agent-cleanup',100)`, id); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "9300")
	if err := os.WriteFile(path, []byte("log"), 0600); err != nil {
		t.Fatal(err)
	}
	cleanupExpiredArtifacts(ctx, pool, root)
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM artifacts WHERE id=$1)`, id).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("expired pending artifact still exists in database")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expired artifact file stat error = %v, want not exist", err)
	}
}
