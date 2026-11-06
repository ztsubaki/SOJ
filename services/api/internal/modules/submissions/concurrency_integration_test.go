package submissions

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConcurrentSubmissionPackageReferencesIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	const workers = 12
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = workers + 2
	config.ConnConfig.RuntimeParams["application_name"] = fmt.Sprintf("soj-submission-lock-%d", time.Now().UnixNano())
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var admin, problem, pkg int64
	root := t.TempDir()
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Error(err)
		}
		keys := make([]string, 0, len(entries))
		for _, entry := range entries {
			keys = append(keys, entry.Name())
		}
		for _, statement := range []struct {
			query string
			arg   any
		}{
			{`UPDATE submissions SET current_job_id=NULL WHERE problem_id=$1`, problem},
			{`DELETE FROM judge_jobs WHERE submission_id IN (SELECT id FROM submissions WHERE problem_id=$1)`, problem},
			{`DELETE FROM submissions WHERE problem_id=$1`, problem},
			{`DELETE FROM problems WHERE id=$1`, problem},
			{`DELETE FROM artifacts WHERE id=$1`, pkg},
			{`DELETE FROM artifacts WHERE object_key=ANY($1::text[])`, keys},
			{`DELETE FROM users WHERE id=$1`, admin},
		} {
			if _, err := pool.Exec(cleanup, statement.query, statement.arg); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
	})
	if err = pool.QueryRow(ctx, `INSERT INTO users(username,email,password_hash,role)
SELECT 'lock_'||suffix,'lock_'||suffix||'@soj.test','unused','admin' FROM (SELECT substr(md5(random()::text),1,20) AS suffix) u RETURNING id`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO artifacts(object_key,kind,state,size_bytes,sha256,content_type,available_at,ref_count)
VALUES('lock-package-'||md5(random()::text),'problem_package','available',1,repeat('a',64),'application/zstd',now(),1) RETURNING id`).Scan(&pkg); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO problems(title,content,difficulty,candidate_partitions,package_artifact_id,package_manifest)
VALUES('Concurrent submission package','','Easy',ARRAY['concurrent-submission'],$1,'{}') RETURNING id`, pkg).Scan(&problem); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE problems SET package_manifest=jsonb_build_object('schemaVersion',1,'problemId',id) WHERE id=$1`, problem); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, root, DefaultMaxSourceSize)
	created := make([]Submission, workers)
	staged := make([]StagedSource, workers)
	for i := range staged {
		staged[i] = writeStagedSource(t, root, "concurrent-"+strconv.Itoa(i)+".c", []byte("int main(void) { return 0; }\n"))
	}
	t.Run("create", func(t *testing.T) {
		runConcurrentJobInserts(t, ctx, pool, pkg, workers, func(i int) error {
			var err error
			created[i], err = service.CreateAdminTestStaged(ctx, admin, problem, staged[i])
			return err
		})
	})
	if t.Failed() {
		return
	}
	if _, err = pool.Exec(ctx, `UPDATE judge_jobs SET state='cancelled',finished_at=now() WHERE submission_id IN (SELECT id FROM submissions WHERE problem_id=$1)`, problem); err != nil {
		t.Fatal(err)
	}
	t.Run("rejudge different submissions", func(t *testing.T) {
		runConcurrentJobInserts(t, ctx, pool, pkg, workers, func(i int) error {
			result, job, err := service.RejudgeAsAdmin(ctx, admin, created[i].ID)
			if err == nil && (result.CurrentJobID == nil || *result.CurrentJobID != job || job == *created[i].CurrentJobID) {
				return fmt.Errorf("rejudge did not switch submission %d to its new job", created[i].ID)
			}
			return err
		})
	})
	var jobs, refs, validSources int64
	if err = pool.QueryRow(ctx, `SELECT
(SELECT count(*) FROM judge_jobs WHERE package_artifact_id=$1),
(SELECT ref_count FROM artifacts WHERE id=$1),
(SELECT count(*) FROM submissions s JOIN artifacts a ON a.id=s.source_artifact_id WHERE s.problem_id=$2 AND a.ref_count=3)`, pkg, problem).Scan(&jobs, &refs, &validSources); err != nil {
		t.Fatal(err)
	}
	if jobs != 2*workers || refs != 1+2*workers || validSources != workers {
		t.Fatalf("after concurrent create/rejudge: jobs=%d package refs=%d retained sources=%d", jobs, refs, validSources)
	}
}

// Pause all writers immediately before their Job insert. At this point they
// have read the shared package and acquired their row locks, so the old broad
// FOR SHARE query deterministically deadlocks when writers retain that package.
func runConcurrentJobInserts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, pkg int64, workers int, operation func(int) error) {
	t.Helper()
	gate, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	locked, functionCreated := false, false
	name := "test_submission_job_barrier_" + strconv.FormatInt(pkg, 10)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if locked {
			_, _ = gate.Exec(cleanup, `SELECT pg_advisory_unlock(-$1::bigint)`, pkg)
		}
		gate.Release()
		if functionCreated {
			if _, err := pool.Exec(cleanup, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON judge_jobs; DROP FUNCTION %s()`, name, name)); err != nil {
				t.Errorf("removing concurrency barrier: %v", err)
			}
		}
	}()
	if _, err = gate.Exec(ctx, `SELECT pg_advisory_lock(-$1::bigint)`, pkg); err != nil {
		t.Fatal(err)
	}
	locked = true
	if _, err = pool.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock_shared(-%d::bigint);
    RETURN NEW;
END;
$$`, name, pkg)); err != nil {
		t.Fatal(err)
	}
	functionCreated = true
	if _, err = pool.Exec(ctx, fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON judge_jobs FOR EACH ROW WHEN (NEW.package_artifact_id=%d) EXECUTE FUNCTION %s()`, name, pkg, name)); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, workers)
	for i := range workers {
		go func() { results <- operation(i) }()
	}
	barrier, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		var waiting int
		err = pool.QueryRow(barrier, `SELECT count(*) FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock' AND wait_event='advisory'`, pool.Config().ConnConfig.RuntimeParams["application_name"]).Scan(&waiting)
		if err != nil {
			t.Errorf("waiting for all %d writers at the package barrier: %v", workers, err)
			break
		}
		if waiting == workers {
			break
		}
		select {
		case <-barrier.Done():
			t.Errorf("only %d/%d writers reached the package barrier", waiting, workers)
		case <-time.After(10 * time.Millisecond):
			continue
		}
		break
	}
	if _, err = gate.Exec(ctx, `SELECT pg_advisory_unlock(-$1::bigint)`, pkg); err != nil {
		t.Fatal(err)
	}
	locked = false
	for range workers {
		if err := <-results; err != nil {
			t.Errorf("concurrent submission mutation: %v", err)
		}
	}
}
