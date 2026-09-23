package stats

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

func TestClusterExcludesStaleAndEndedIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx := t.Context()
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	before, err := ReadClusterSnapshot(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DELETE FROM agent_sessions WHERE partition LIKE 'snapshot-test-%'`, `INSERT INTO agent_sessions(id,partition,last_heartbeat_at,ended_at) VALUES('snapshot-1','snapshot-test-1',now(),NULL),('snapshot-2','snapshot-test-2',now()-interval '1 minute',NULL),('snapshot-3','snapshot-test-3',now(),now())`} {
		if _, err = p.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	after, err := ReadClusterSnapshot(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if after.Sessions != before.Sessions+1 || after.Empty != before.Empty+1 {
		t.Fatalf("before=%+v after=%+v", before, after)
	}
	_, _ = p.Exec(ctx, `DELETE FROM agent_sessions WHERE partition LIKE 'snapshot-test-%'`)
}
