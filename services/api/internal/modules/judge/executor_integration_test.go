package judge

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	judgev1 "github.com/ztsubaki/soj/proto/soj/judge/v1"
	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRegisterRequiresExplicitExecutor(t *testing.T) {
	server := NewControlServer(nil, t.TempDir(), "token")
	for _, executor := range []sojv1.AgentExecutor{sojv1.AgentExecutor_AGENT_EXECUTOR_UNSPECIFIED, sojv1.AgentExecutor(99)} {
		_, err := server.RegisterAgent(t.Context(), &judgev1.RegisterAgentRequest{AgentSessionId: "55555555-5555-4555-8555-555555555555", Partition: "explicit-executor", Executor: executor})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("executor %v: got %v, want InvalidArgument", executor, err)
		}
	}
}

func TestExecutorClaimsAndTerminalResultsIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const slurmAgent = "55555555-5555-4555-8555-555555555555"
	const dockerAgent = "66666666-6666-4666-8666-666666666666"
	const slurmPartition = "executor-claim-slurm"
	const dockerPartition = "executor-claim-docker"
	var submissions, jobs []int64
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = pool.Exec(cleanup, `UPDATE submissions SET current_job_id=NULL WHERE id=ANY($1::bigint[])`, submissions)
		_, _ = pool.Exec(cleanup, `DELETE FROM judge_jobs WHERE id=ANY($1::bigint[])`, jobs)
		_, _ = pool.Exec(cleanup, `DELETE FROM submissions WHERE id=ANY($1::bigint[])`, submissions)
		_, _ = pool.Exec(cleanup, `DELETE FROM agent_sessions WHERE id=ANY($1::text[])`, []string{slurmAgent, dockerAgent})
	}()
	server := NewControlServer(pool, t.TempDir(), "token")
	for _, registration := range []*judgev1.RegisterAgentRequest{
		{AgentSessionId: slurmAgent, Partition: slurmPartition, Executor: sojv1.AgentExecutor_AGENT_EXECUTOR_SLURM},
		{AgentSessionId: dockerAgent, Partition: dockerPartition, Executor: sojv1.AgentExecutor_AGENT_EXECUTOR_DOCKER},
	} {
		registered, err := server.RegisterAgent(ctx, registration)
		if err != nil || registered.GetSession().GetExecutor() != registration.GetExecutor() {
			t.Fatalf("register = %v, %v", registered, err)
		}
		changed := proto.Clone(registration).(*judgev1.RegisterAgentRequest)
		if registration.GetExecutor() == sojv1.AgentExecutor_AGENT_EXECUTOR_SLURM {
			changed.Executor = sojv1.AgentExecutor_AGENT_EXECUTOR_DOCKER
		} else {
			changed.Executor = sojv1.AgentExecutor_AGENT_EXECUTOR_SLURM
		}
		if _, err := server.RegisterAgent(ctx, changed); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("changing executor: %v", err)
		}
	}
	addJob := func(manifest string, partitions []string, created time.Time) int64 {
		t.Helper()
		var submissionID, jobID int64
		if err := pool.QueryRow(ctx, `INSERT INTO submissions(user_id,problem_id,contest_id,kind,source_artifact_id) VALUES(2,100,100,'contest',101) RETURNING id`).Scan(&submissionID); err != nil {
			t.Fatal(err)
		}
		submissions = append(submissions, submissionID)
		if err := pool.QueryRow(ctx, `INSERT INTO judge_jobs(submission_id,source_artifact_id,package_artifact_id,package_sha256,manifest,eligible_partitions,created_at) VALUES($1,101,100,repeat('a',64),$2,$3,$4) RETURNING id`, submissionID, manifest, partitions, created).Scan(&jobID); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, jobID)
		if _, err := pool.Exec(ctx, `UPDATE submissions SET current_job_id=$2 WHERE id=$1`, submissionID, jobID); err != nil {
			t.Fatal(err)
		}
		return jobID
	}
	dockerManifest := `{"schemaVersion":1,"problemId":100,"docker":{"image":"sha256:` + strings.Repeat("a", 64) + `","cpus":1,"memoryMb":128,"timeLimitSeconds":10}}`
	partitions := []string{slurmPartition, dockerPartition}
	addJob(dockerManifest, []string{slurmPartition}, time.Now().Add(-24*time.Hour))
	// The FIFO head contains only the opposite executor for the Slurm Agent.
	// Its matching job must be found through the sparse-partition fallback.
	var firstDocker int64
	for i := range 70 {
		jobID := addJob(dockerManifest, partitions, time.Now().Add(-time.Duration(100-i)*time.Minute))
		if i == 0 {
			firstDocker = jobID
		}
	}
	slurmJob := addJob(`{"schemaVersion":1,"problemId":100}`, partitions, time.Now())
	for _, expected := range []struct {
		agent    string
		job      int64
		executor sojv1.AgentExecutor
	}{{slurmAgent, slurmJob, sojv1.AgentExecutor_AGENT_EXECUTOR_SLURM}, {dockerAgent, firstDocker, sojv1.AgentExecutor_AGENT_EXECUTOR_DOCKER}} {
		claim, err := server.ClaimJobs(ctx, &judgev1.ClaimJobsRequest{AgentSessionId: expected.agent})
		if err != nil || claim.GetAssignment().GetJobId() != expected.job || claim.GetAssignment().GetExecutor() != expected.executor {
			t.Fatalf("claim %s = %v, %v", expected.agent, claim, err)
		}
		replay, err := server.ClaimJobs(ctx, &judgev1.ClaimJobsRequest{AgentSessionId: expected.agent})
		if err != nil || !proto.Equal(claim.GetAssignment(), replay.GetAssignment()) {
			t.Fatalf("executor-preserving replay = %v, %v", replay, err)
		}
		executionID := "execution-" + expected.agent
		reported, err := server.ReportJob(ctx, &judgev1.ReportJobRequest{AgentSessionId: expected.agent, JobId: expected.job, State: sojv1.JobState_JOB_STATE_FINISHING, ExecutionId: &executionID})
		if err != nil || reported.GetExecutionId() != executionID {
			t.Fatalf("report generic ID = %v, %v", reported, err)
		}
		changedID := "changed"
		if _, err := server.ReportJob(ctx, &judgev1.ReportJobRequest{AgentSessionId: expected.agent, JobId: expected.job, State: sojv1.JobState_JOB_STATE_FINISHING, ExecutionId: &changedID}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("changed execution ID: %v", err)
		}
	}
	// Exercise both Docker resource-limit terminal forms without result archives.
	for index, terminal := range []sojv1.ExecutionTerminalState{sojv1.ExecutionTerminalState_EXECUTION_TERMINAL_STATE_TIMEOUT, sojv1.ExecutionTerminalState_EXECUTION_TERMINAL_STATE_OUT_OF_MEMORY} {
		jobID := firstDocker
		if index > 0 {
			claim, err := server.ClaimJobs(ctx, &judgev1.ClaimJobsRequest{AgentSessionId: dockerAgent})
			if err != nil {
				t.Fatal(err)
			}
			jobID = claim.GetAssignment().GetJobId()
			id := strings.Repeat("b", 64)
			if _, err := server.ReportJob(ctx, &judgev1.ReportJobRequest{AgentSessionId: dockerAgent, JobId: jobID, State: sojv1.JobState_JOB_STATE_FINISHING, ExecutionId: &id}); err != nil {
				t.Fatal(err)
			}
		}
		verdict := sojv1.Verdict_VERDICT_TIME_LIMIT
		if index > 0 {
			verdict = sojv1.Verdict_VERDICT_MEMORY_LIMIT
		}
		request := &judgev1.CompleteJobRequest{AgentSessionId: dockerAgent, JobId: jobID, FinishedAt: timestamppb.Now(), ExecutionTerminalState: terminal, Result: &judgev1.StructuredResult{Verdict: verdict, Score: "0"}}
		completed, err := server.CompleteJob(ctx, request)
		if err != nil || completed.GetState() != sojv1.JobState_JOB_STATE_COMPLETED {
			t.Fatalf("Docker limit completion = %v, %v", completed, err)
		}
		var storedID, storedTerminal string
		if err := pool.QueryRow(ctx, `SELECT execution_id,execution_terminal_state FROM judge_jobs WHERE id=$1`, jobID).Scan(&storedID, &storedTerminal); err != nil || storedID == "" || storedTerminal != terminalStateValue(terminal) {
			t.Fatalf("stored generic execution = %q, %q, %v", storedID, storedTerminal, err)
		}
		request.AgentSessionId = slurmAgent
		if _, err := server.CompleteJob(ctx, request); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("nonowner terminal request: %v", err)
		}
	}
	// Lock all remaining Docker candidates: diagnostic eligibility still counts
	// only the requested executor, including skipped locked rows.
	locked, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Rollback(context.Background())
	rows, err := locked.Query(ctx, `SELECT id FROM judge_jobs WHERE id=ANY($1::bigint[]) AND state='pending' FOR UPDATE`, jobs)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, expected := range []struct {
		agent    string
		eligible uint32
	}{{slurmAgent, 0}, {dockerAgent, 68}} {
		empty, err := server.ClaimJobs(ctx, &judgev1.ClaimJobsRequest{AgentSessionId: expected.agent})
		if err != nil || empty.GetAssignment() != nil || empty.GetEligiblePendingJobCount() != expected.eligible || empty.GetPendingJobCount() < 68 {
			t.Fatalf("executor diagnostic = %v, %v; want eligible %d", empty, err, expected.eligible)
		}
	}
}
