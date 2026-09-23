package stats

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
	"github.com/ztsubaki/soj/services/api/internal/modules/judge"
	"time"
)

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

type ClusterSnapshot struct {
	Sessions, Empty, NoQueue, Queued int64
	UpdatedAt                        *time.Time
}

func ReadClusterSnapshot(ctx context.Context, pool *pgxpool.Pool) (ClusterSnapshot, error) {
	var s ClusterSnapshot
	err := pool.QueryRow(ctx, `WITH fresh AS (
 SELECT a.last_heartbeat_at,
 EXISTS(SELECT 1 FROM judge_jobs j WHERE j.agent_session_id=a.id AND j.state IN ('claimed','submitted')) AS waiting,
 EXISTS(SELECT 1 FROM judge_jobs j WHERE j.agent_session_id=a.id AND j.state IN ('running','finishing')) AS running
 FROM agent_sessions a WHERE a.offline_at IS NULL AND a.ended_at IS NULL AND a.last_heartbeat_at>now()-$1::interval
 ) SELECT count(*),count(*) FILTER(WHERE NOT waiting AND NOT running),count(*) FILTER(WHERE NOT waiting AND running),count(*) FILTER(WHERE waiting),min(last_heartbeat_at) FROM fresh`, judge.OfflineThreshold.String()).Scan(&s.Sessions, &s.Empty, &s.NoQueue, &s.Queued, &s.UpdatedAt)
	return s, err
}

func (s ClusterSnapshot) Workload() *sojv1.AgentWorkloadCounts {
	return &sojv1.AgentWorkloadCounts{Empty: uint32(s.Empty), NoQueue: uint32(s.NoQueue), Queued: uint32(s.Queued)}
}
