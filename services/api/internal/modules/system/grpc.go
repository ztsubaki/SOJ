package system

import (
	"context"
	"errors"
	"fmt"
	"github.com/ztsubaki/soj/services/api/internal/modules/judge"
	"github.com/ztsubaki/soj/services/api/internal/rpcutil"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
	"github.com/ztsubaki/soj/services/api/internal/modules/auth"
	"github.com/ztsubaki/soj/services/api/internal/modules/stats"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type GRPCServer struct {
	sojv1.UnimplementedAdminSystemServiceServer
	pool *pgxpool.Pool
	auth *auth.Service
}

func NewGRPCServer(pool *pgxpool.Pool, authService *auth.Service) *GRPCServer {
	return &GRPCServer{pool: pool, auth: authService}
}

func (s *GRPCServer) GetStats(ctx context.Context, _ *sojv1.GetAdminStatsRequest) (*sojv1.AdminStats, error) {
	if _, err := auth.RequireAdmin(ctx, s.auth); err != nil {
		return nil, err
	}
	var users, today, pending int64
	err := s.pool.QueryRow(ctx, `SELECT
  (SELECT count(*) FROM users WHERE role='user'),
	  (SELECT count(*) FROM submissions WHERE created_at >= (date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')),
	  (SELECT count(*)
	     FROM submissions s
	     JOIN judge_jobs j ON j.id=s.current_job_id
	    WHERE j.state IN ('pending','claimed','submitted','running','finishing'))`).Scan(&users, &today, &pending)
	if err != nil {
		return nil, status.Error(codes.Internal, "get admin stats")
	}
	snapshot, err := stats.ReadClusterSnapshot(ctx, s.pool)
	if err != nil {
		return nil, status.Error(codes.Internal, "get agent workload")
	}
	result := &sojv1.AdminStats{AgentWorkload: snapshot.Workload(), TotalUsers: users, TodaySubmissions: today, PendingSubmissions: pending}
	return result, nil
}

func (s *GRPCServer) GetStatus(ctx context.Context, _ *sojv1.GetSystemStatusRequest) (*sojv1.SystemStatus, error) {
	if _, err := auth.RequireAdmin(ctx, s.auth); err != nil {
		return nil, err
	}
	var pending, running int64
	err := s.pool.QueryRow(ctx, `SELECT
  (SELECT count(*) FROM judge_jobs WHERE state='pending'),
	  (SELECT count(*) FROM judge_jobs WHERE state IN ('claimed','submitted','running','finishing'))`).Scan(&pending, &running)
	if err != nil {
		return nil, status.Error(codes.Internal, "get system status")
	}
	snapshot, err := stats.ReadClusterSnapshot(ctx, s.pool)
	if err != nil {
		return nil, status.Error(codes.Internal, "get active agents")
	}
	return &sojv1.SystemStatus{AgentWorkload: snapshot.Workload(), ActiveAgents: snapshot.Sessions, PendingJobs: pending, RunningJobs: running, UpdatedAt: timestamppb.Now()}, nil
}

func (s *GRPCServer) ListAgents(ctx context.Context, req *sojv1.ListAgentsRequest) (*sojv1.ListAgentsResponse, error) {
	if _, err := auth.RequireAdmin(ctx, s.auth); err != nil {
		return nil, err
	}
	want, err := agentStateText(req.State)
	if err != nil {
		return nil, err
	}
	p, n := rpcutil.Pagination(req.GetPage())
	items, total, err := s.agents(ctx, req.Partition, want, p, n)
	if err != nil {
		return nil, status.Error(codes.Internal, "list agents")
	}
	return &sojv1.ListAgentsResponse{Items: items, Page: &sojv1.PageMeta{Page: int32(p), PageSize: int32(n), Total: total}}, nil
}

func (s *GRPCServer) DrainAgent(ctx context.Context, req *sojv1.DrainAgentRequest) (*sojv1.AgentSession, error) {
	if _, err := auth.RequireAdmin(ctx, s.auth); err != nil {
		return nil, err
	}
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "agent id required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "database unavailable")
	}
	defer tx.Rollback(ctx)
	var offline bool
	err = tx.QueryRow(ctx, `SELECT offline_at IS NOT NULL OR (ended_at IS NULL AND last_heartbeat_at<=now()-$2::interval) FROM agent_sessions WHERE id=$1 FOR UPDATE`, req.GetId(), judge.OfflineThreshold.String()).Scan(&offline)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "agent session not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "read agent session")
	}
	if offline {
		return nil, status.Error(codes.FailedPrecondition, "agent session is offline")
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_sessions SET
  drain_requested_at=COALESCE(drain_requested_at,now()),
  ended_at=CASE WHEN NOT EXISTS (SELECT 1 FROM judge_jobs WHERE agent_session_id=$1 AND state IN ('claimed','submitted','running','finishing')) THEN COALESCE(ended_at,now()) ELSE ended_at END
  WHERE id=$1`, req.GetId()); err != nil {
		return nil, status.Error(codes.Internal, "request agent drain")
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, status.Error(codes.Internal, "commit agent drain")
	}
	items, _, err := s.agents(ctx, nil, "", 1, 1, req.GetId())
	if err != nil || len(items) != 1 {
		return nil, status.Error(codes.Internal, "read drained agent")
	}
	return items[0], nil
}

// extraID is used only by DrainAgent after the mutation; regular listing uses
// database-side state filtering and pagination.
func (s *GRPCServer) agents(ctx context.Context, partition *string, want string, page, size int, extraID ...string) ([]*sojv1.AgentSession, int64, error) {
	where, args := "", []any{}
	if len(extraID) == 1 {
		args = append(args, extraID[0])
		where = " WHERE id=$2"
	} else if partition != nil {
		args = append(args, *partition)
		where = " WHERE partition=$2"
	}
	query := `WITH listed AS (
 SELECT a.id,a.partition,a.last_heartbeat_at,a.registered_at,a.drain_requested_at,a.ended_at,
   (SELECT count(*) FROM judge_jobs j WHERE j.agent_session_id=a.id AND j.state IN ('claimed','submitted','running','finishing'))::int AS active_jobs,
 (SELECT count(*) FROM judge_jobs j WHERE j.agent_session_id=a.id AND j.state IN ('claimed','submitted'))::int AS waiting_jobs,
   CASE WHEN a.offline_at IS NOT NULL OR (a.ended_at IS NULL AND a.last_heartbeat_at<=now()-$%d::interval) THEN 'offline'
        WHEN a.ended_at IS NOT NULL THEN 'ended'
        WHEN a.drain_requested_at IS NOT NULL THEN 'draining'
        ELSE 'online' END AS derived_state
 FROM agent_sessions a` + where + `)
 SELECT id,partition,last_heartbeat_at,registered_at,drain_requested_at,ended_at,active_jobs,waiting_jobs,derived_state FROM listed`
	query = fmt.Sprintf(query, 1)
	args = append([]any{judge.OfflineThreshold.String()}, args...)
	if want != "" {
		args = append(args, want)
		query += fmt.Sprintf(" WHERE derived_state=$%d", len(args))
	}
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM (`+query+`) counted`, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, size, (page-1)*size)
	query += fmt.Sprintf(" ORDER BY registered_at DESC,id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []*sojv1.AgentSession{}
	for rows.Next() {
		var id, part, derived string
		var active, waiting int32
		var heartbeat, created time.Time
		var drain, ended *time.Time
		if err = rows.Scan(&id, &part, &heartbeat, &created, &drain, &ended, &active, &waiting, &derived); err != nil {
			return nil, 0, err
		}
		state, _ := agentStateProto(derived)
		item := &sojv1.AgentSession{Id: id, Partition: part, State: state, LastHeartbeatAt: timestamppb.New(heartbeat), CreatedAt: timestamppb.New(created), ActiveJobCount: uint32(active), WaitingJobCount: uint32(waiting), RunningJobCount: uint32(active - waiting), Workload: sojv1.AgentWorkload_AGENT_WORKLOAD_EMPTY}
		if waiting > 0 {
			item.Workload = sojv1.AgentWorkload_AGENT_WORKLOAD_QUEUED
		} else if active > 0 {
			item.Workload = sojv1.AgentWorkload_AGENT_WORKLOAD_NO_QUEUE
		}
		if drain != nil {
			item.DrainRequestedAt = timestamppb.New(*drain)
		}
		if ended != nil {
			item.EndedAt = timestamppb.New(*ended)
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func agentStateText(value *sojv1.AgentState) (string, error) {
	if value == nil {
		return "", nil
	}
	for _, name := range []string{"online", "draining", "ended", "offline"} {
		p, _ := agentStateProto(name)
		if p == *value {
			return name, nil
		}
	}
	return "", status.Error(codes.InvalidArgument, "invalid agent state")
}
func agentStateProto(value string) (sojv1.AgentState, error) {
	switch value {
	case "online":
		return sojv1.AgentState_AGENT_STATE_ONLINE, nil
	case "draining":
		return sojv1.AgentState_AGENT_STATE_DRAINING, nil
	case "ended":
		return sojv1.AgentState_AGENT_STATE_ENDED, nil
	case "offline":
		return sojv1.AgentState_AGENT_STATE_OFFLINE, nil
	}
	return 0, errors.New("invalid state")
}
