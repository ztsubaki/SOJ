package stats

import (
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestClusterExcludesStaleAndEndedIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx := t.Context()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	// Keep both queries on the connection owning these temporary fixture tables.
	config.MaxConns = 1
	p, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// Other packages register Agents concurrently against the same test database.
	// Shadow only the tables read by this aggregate instead of counting their rows.
	if _, err := p.Exec(ctx, `CREATE TEMP TABLE agent_sessions (LIKE public.agent_sessions INCLUDING DEFAULTS);
    CREATE TEMP TABLE judge_jobs (LIKE public.judge_jobs INCLUDING DEFAULTS)`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `INSERT INTO agent_sessions(id,partition,last_heartbeat_at,ended_at) VALUES('snapshot-1','snapshot-test-1',now(),NULL),('snapshot-2','snapshot-test-2',now()-interval '1 minute',NULL),('snapshot-3','snapshot-test-3',now(),now())`); err != nil {
		t.Fatal(err)
	}
	after, err := ReadClusterSnapshot(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if after.Sessions != 1 || after.Empty != 1 || after.NoQueue != 0 || after.Queued != 0 {
		t.Fatalf("snapshot=%+v, want exactly one fresh, empty Agent", after)
	}
}
