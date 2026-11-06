package judge

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	judgev1 "github.com/ztsubaki/soj/proto/soj/judge/v1"
	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
)

func TestPartitionClaimFIFOAndCountsIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const agent = "44444444-4444-4444-8444-444444444444"
	const partition = "partition-claim-fifo"
	var submissions, jobs []int64
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = pool.Exec(cleanup, `UPDATE submissions SET current_job_id=NULL WHERE id=ANY($1::bigint[])`, submissions)
		_, _ = pool.Exec(cleanup, `DELETE FROM judge_jobs WHERE id=ANY($1::bigint[])`, jobs)
		_, _ = pool.Exec(cleanup, `DELETE FROM submissions WHERE id=ANY($1::bigint[])`, submissions)
		_, _ = pool.Exec(cleanup, `DELETE FROM agent_sessions WHERE id=$1`, agent)
	}()
	server := NewControlServer(pool, t.TempDir(), "service-token")
	if _, err := server.RegisterAgent(ctx, &judgev1.RegisterAgentRequest{AgentSessionId: agent, Partition: partition, Executor: sojv1.AgentExecutor_AGENT_EXECUTOR_SLURM}); err != nil {
		t.Fatal(err)
	}
	for i, partitions := range [][]string{{"other-partition"}, {partition, "other-partition"}, {partition}, {partition}} {
		var submissionID, jobID int64
		if err := pool.QueryRow(ctx, `INSERT INTO submissions(user_id,problem_id,contest_id,kind,source_artifact_id) VALUES(2,100,100,'contest',101) RETURNING id`).Scan(&submissionID); err != nil {
			t.Fatal(err)
		}
		submissions = append(submissions, submissionID)
		if err := pool.QueryRow(ctx, `INSERT INTO judge_jobs(submission_id,source_artifact_id,package_artifact_id,package_sha256,manifest,eligible_partitions,created_at) VALUES($1,101,100,repeat('a',64),'{"schemaVersion":1,"problemId":100}',$2,$3) RETURNING id`, submissionID, partitions, time.Now().Add(time.Duration(i-3)*time.Minute)).Scan(&jobID); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, jobID)
		if _, err := pool.Exec(ctx, `UPDATE submissions SET current_job_id=$2 WHERE id=$1`, submissionID, jobID); err != nil {
			t.Fatal(err)
		}
	}
	var unrelatedSubmissions, unrelatedJobs []int64
	if err := pool.QueryRow(ctx, `WITH added AS (
 INSERT INTO submissions(user_id,problem_id,contest_id,kind,source_artifact_id)
 SELECT 2,100,100,'contest',101 FROM generate_series(1,200) RETURNING id
), queued AS (
 INSERT INTO judge_jobs(submission_id,source_artifact_id,package_artifact_id,package_sha256,manifest,eligible_partitions,created_at)
 SELECT id,101,100,repeat('a',64),'{"schemaVersion":1,"problemId":100}',ARRAY['unrelated-backlog'],now()-interval '1 day' FROM added
 RETURNING id,submission_id
)
SELECT array_agg(submission_id),array_agg(id) FROM queued`).Scan(&unrelatedSubmissions, &unrelatedJobs); err != nil {
		t.Fatal(err)
	}
	submissions = append(submissions, unrelatedSubmissions...)
	jobs = append(jobs, unrelatedJobs...)
	if _, err := pool.Exec(ctx, `UPDATE submissions s SET current_job_id=j.id FROM judge_jobs j WHERE j.submission_id=s.id AND s.id=ANY($1::bigint[])`, unrelatedSubmissions); err != nil {
		t.Fatal(err)
	}
	locked, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Rollback(context.Background())
	var lockedID int64
	if err := locked.QueryRow(ctx, `SELECT id FROM judge_jobs WHERE id=$1 FOR UPDATE`, jobs[1]).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	for index, expectedJob := range []int64{jobs[2], jobs[1], jobs[3]} {
		claimed, err := server.ClaimJobs(ctx, &judgev1.ClaimJobsRequest{AgentSessionId: agent})
		if err != nil {
			t.Fatal(err)
		}
		if claimed.GetAssignment().GetJobId() != expectedJob || claimed.GetAssignment().GetAssignedPartition() != partition {
			t.Fatalf("claim must skip older ineligible work and preserve eligible FIFO: %v", claimed)
		}
		if index == 0 {
			if err := locked.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
		}
		executionID := "12345"
		if _, err := server.ReportJob(ctx, &judgev1.ReportJobRequest{AgentSessionId: agent, JobId: expectedJob, State: sojv1.JobState_JOB_STATE_RUNNING, ExecutionId: &executionID}); err != nil {
			t.Fatal(err)
		}
	}
	var pending int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM judge_jobs WHERE state='pending'`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	empty, err := server.ClaimJobs(ctx, &judgev1.ClaimJobsRequest{AgentSessionId: agent})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Assignment != nil || int64(empty.GetPendingJobCount()) != pending || empty.GetEligiblePendingJobCount() != 0 {
		t.Fatalf("partition-empty diagnostics must retain exact pending counts: %v, want total %d", empty, pending)
	}
}
