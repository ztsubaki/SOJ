package judge

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RunAgentOfflineScanner retires stale Agent sessions even when no replacement
// Agent registers. Its transaction also makes every active Job they owned a
// terminal internal error, so a Submission cannot remain stuck indefinitely.
func RunAgentOfflineScanner(ctx context.Context, pool *pgxpool.Pool, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	markOfflineAgents(ctx, pool)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := markOfflineAgents(ctx, pool); err != nil {
				log.Printf("WARN agent offline scan: %v", err)
			}
		}
	}
}

func markOfflineAgents(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `UPDATE judge_jobs SET state='internal_error',message='agent session offline',finished_at=COALESCE(finished_at,now()) WHERE agent_session_id IN (SELECT id FROM agent_sessions WHERE offline_at IS NULL AND ended_at IS NULL AND last_heartbeat_at<=now()-$1::interval FOR UPDATE) AND state IN ('claimed','submitted','running','finishing')`, OfflineThreshold.String())
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_sessions SET offline_at=now(),ended_at=COALESCE(ended_at,now())
WHERE offline_at IS NULL AND ended_at IS NULL AND last_heartbeat_at<=now()-$1::interval`, OfflineThreshold.String()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
