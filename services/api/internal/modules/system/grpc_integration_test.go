package system

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
	"github.com/ztsubaki/soj/services/api/internal/modules/auth"
	"github.com/ztsubaki/soj/services/api/internal/rpcauth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestDrainIdleAgentIntegration(t *testing.T) {
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

	const agentID = "agent-drain-integration"
	if _, err := pool.Exec(ctx, "DELETE FROM agent_sessions WHERE id=$1", agentID); err != nil {
		t.Fatalf("clear agent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM agent_sessions WHERE id=$1", agentID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO agent_sessions(id,partition,executor) VALUES($1,'drain-integration','docker')`, agentID); err != nil {
		t.Fatalf("create idle agent: %v", err)
	}

	authService, err := auth.NewPostgresService(ctx, pool, t.TempDir(), time.Hour, nil)
	if err != nil {
		t.Fatalf("new PostgreSQL auth service: %v", err)
	}
	token, _, err := authService.CreateSession(ctx, auth.User{ID: 1})
	if err != nil {
		t.Fatalf("create admin session: %v", err)
	}
	t.Cleanup(func() { authService.Revoke(ctx, token) })

	server := NewGRPCServer(pool, authService)
	response, err := server.DrainAgent(systemAdminContext(t, token), &sojv1.DrainAgentRequest{Id: agentID})
	if err != nil {
		t.Fatalf("DrainAgent(): %v", err)
	}
	if response.GetState() != sojv1.AgentState_AGENT_STATE_ENDED || response.GetDrainRequestedAt() == nil || response.GetEndedAt() == nil || response.GetExecutor() != sojv1.AgentExecutor_AGENT_EXECUTOR_DOCKER {
		t.Fatalf("DrainAgent() = %+v, want immediately disabled idle agent", response)
	}
	var drain, ended bool
	if err := pool.QueryRow(ctx, `SELECT drain_requested_at IS NOT NULL,ended_at IS NOT NULL
		FROM agent_sessions WHERE id=$1`, agentID).Scan(&drain, &ended); err != nil {
		t.Fatalf("read drained agent: %v", err)
	}
	if !drain || !ended {
		t.Fatalf("drain persistence = (drain=%v, ended=%v), want all true", drain, ended)
	}
}

func TestGetStatsCountsCurrentRunningSubmissionAsPendingIntegration(t *testing.T) {
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

	const submissionID int64 = 9400
	const jobID int64 = 9400
	const agentID = "agent-system-stats-running"
	if _, err := pool.Exec(ctx, "UPDATE submissions SET current_job_id=NULL WHERE id=$1", submissionID); err != nil {
		t.Fatalf("detach existing current job: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM judge_jobs WHERE id=$1", jobID); err != nil {
		t.Fatalf("clear job: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM submissions WHERE id=$1", submissionID); err != nil {
		t.Fatalf("clear submission: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM agent_sessions WHERE id=$1", agentID); err != nil {
		t.Fatalf("clear agent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "UPDATE submissions SET current_job_id=NULL WHERE id=$1", submissionID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM judge_jobs WHERE id=$1", jobID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM submissions WHERE id=$1", submissionID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM agent_sessions WHERE id=$1", agentID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO agent_sessions(id,partition,executor) VALUES($1,'system-stats','slurm')`, agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO submissions(id,user_id,problem_id,contest_id,kind,source_artifact_id)
		OVERRIDING SYSTEM VALUE VALUES($1,2,100,100,'contest',101)`, submissionID); err != nil {
		t.Fatalf("create submission: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO judge_jobs(id,submission_id,source_artifact_id,package_artifact_id,package_sha256,manifest,eligible_partitions,state,agent_session_id,assigned_partition)
		OVERRIDING SYSTEM VALUE VALUES($1,$2,101,100,'5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03','{}',ARRAY['system-stats'],'running',$3,'system-stats')`, jobID, submissionID, agentID); err != nil {
		t.Fatalf("create running job: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE submissions SET current_job_id=$2 WHERE id=$1", submissionID, jobID); err != nil {
		t.Fatalf("set current job: %v", err)
	}

	authService, err := auth.NewPostgresService(ctx, pool, t.TempDir(), time.Hour, nil)
	if err != nil {
		t.Fatalf("new PostgreSQL auth service: %v", err)
	}
	token, _, err := authService.CreateSession(ctx, auth.User{ID: 1})
	if err != nil {
		t.Fatalf("create admin session: %v", err)
	}
	t.Cleanup(func() { authService.Revoke(ctx, token) })

	server := NewGRPCServer(pool, authService)
	response, err := server.GetStats(systemAdminContext(t, token), &sojv1.GetAdminStatsRequest{})
	if err != nil {
		t.Fatalf("GetStats(): %v", err)
	}
	var expected int64
	if err := pool.QueryRow(ctx, `SELECT count(*)
		FROM submissions s JOIN judge_jobs j ON j.id=s.current_job_id
		WHERE j.state IN ('pending','claimed','submitted','running','finishing')`).Scan(&expected); err != nil {
		t.Fatalf("count projected pending submissions: %v", err)
	}
	if response.GetPendingSubmissions() != expected {
		t.Fatalf("PendingSubmissions = %d, want %d including running current submission", response.GetPendingSubmissions(), expected)
	}
}

func systemAdminContext(t *testing.T, sessionToken string) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	t.Cleanup(cancel)
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Bearer admin-bff", "x-soj-session", sessionToken))
	interceptor := rpcauth.UnaryInterceptor("judge-agent", "web-bff", "admin-bff")
	var authenticated context.Context
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/soj.v1.AdminSystemService/DrainAgent"}, func(ctx context.Context, _ any) (any, error) {
		authenticated = ctx
		return nil, nil
	})
	if err != nil {
		t.Fatalf("authenticate admin request: %v", err)
	}
	return authenticated
}
