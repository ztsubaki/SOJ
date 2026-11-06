package artifacts_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	judgev1 "github.com/ztsubaki/soj/proto/soj/judge/v1"
	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
	"github.com/ztsubaki/soj/services/api/internal/modules/artifacts"
	"github.com/ztsubaki/soj/services/api/internal/modules/auth"
	"github.com/ztsubaki/soj/services/api/internal/modules/contests"
	"github.com/ztsubaki/soj/services/api/internal/modules/judge"
	"github.com/ztsubaki/soj/services/api/internal/modules/problems"
	"github.com/ztsubaki/soj/services/api/internal/modules/submissions"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestBusinessDeletionReclaimsJobUploadsIntegration(t *testing.T) {
	for _, target := range []string{"user", "contest", "private problem"} {
		t.Run(target, func(t *testing.T) {
			f := newBusinessDeletionFixture(t, target == "private problem")
			ctx := t.Context()
			if target == "user" {
				// Even after metadata deletion, rollback must preserve the Job,
				// pending uploads, published archives, counts, and object bytes.
				tx, err := f.pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				if _, err = tx.Exec(ctx, `UPDATE submissions SET current_job_id=NULL WHERE id=ANY($1)`, f.targetSubmissions); err != nil {
					t.Fatal(err)
				}
				refs, pendingKeys, err := artifacts.DeleteSubmissionJobs(ctx, tx, `SELECT id FROM submissions WHERE id=ANY($1)`, f.targetSubmissions)
				if err != nil {
					t.Fatal(err)
				}
				if len(pendingKeys) != 2 {
					t.Fatalf("pending objects selected for removal = %d, want 2", len(pendingKeys))
				}
				if _, err = tx.Exec(ctx, `DELETE FROM submissions WHERE id=ANY($1)`, f.targetSubmissions); err != nil {
					t.Fatal(err)
				}
				refs = append(refs, f.source, f.exclusiveSource)
				if _, err = artifacts.Release(ctx, tx, refs...); err != nil {
					t.Fatal(err)
				}
				if err = tx.Rollback(ctx); err != nil {
					t.Fatal(err)
				}
				var uploads int
				if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM artifacts WHERE upload_job_id IN (SELECT id FROM judge_jobs WHERE submission_id=ANY($1))`, f.targetSubmissions).Scan(&uploads); err != nil || uploads != 4 {
					t.Fatalf("uploads after rollback = %d, error = %v", uploads, err)
				}
				f.assertArtifact(t, f.source, 4)
				f.assertArtifact(t, f.exclusiveSource, 2)
				f.assertArtifact(t, f.pkg, 5)
				for i, id := range f.uploads {
					refs := int64(0)
					if i < 2 {
						refs = 1
					}
					f.assertArtifact(t, id, refs)
				}
			}

			var err error
			switch target {
			case "user":
				service, createErr := auth.NewPostgresService(ctx, f.pool, f.root, time.Hour, nil)
				if createErr != nil {
					t.Fatal(createErr)
				}
				err = service.AdminDeleteUser(ctx, auth.User{ID: f.admin, Role: auth.RoleAdmin}, f.user)
			case "contest":
				err = contests.NewService(f.pool, f.root).AdminDelete(ctx, f.admin, f.contest)
			case "private problem":
				err = problems.NewService(f.pool, f.root, 0).AdminDelete(ctx, f.admin, f.problem)
			}
			if err != nil {
				t.Fatalf("delete %s with published and pending uploads: %v", target, err)
			}
			var remaining int
			if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM submissions WHERE id=ANY($1)`, f.targetSubmissions).Scan(&remaining); err != nil || remaining != 0 {
				t.Fatalf("deleted submissions remaining = %d, error = %v", remaining, err)
			}
			if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM judge_jobs WHERE submission_id=ANY($1)`, f.targetSubmissions).Scan(&remaining); err != nil || remaining != 0 {
				t.Fatalf("deleted jobs remaining = %d, error = %v", remaining, err)
			}
			rootQuery := map[string]string{
				"user":            `SELECT count(*) FROM users WHERE id=$1`,
				"contest":         `SELECT count(*) FROM contests WHERE id=$1`,
				"private problem": `SELECT count(*) FROM problems WHERE id=$1`,
			}[target]
			rootID := map[string]int64{"user": f.user, "contest": f.contest, "private problem": f.problem}[target]
			if err = f.pool.QueryRow(ctx, rootQuery, rootID).Scan(&remaining); err != nil || remaining != 0 {
				t.Fatalf("deleted %s remaining = %d, error = %v", target, remaining, err)
			}
			for _, id := range append([]int64{f.exclusiveSource}, f.uploads...) {
				var exists bool
				if err = f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM artifacts WHERE id=$1)`, id).Scan(&exists); err != nil || exists {
					t.Fatalf("artifact %d remains after deleting %s: exists=%v error=%v", id, target, exists, err)
				}
				if _, err = os.Stat(filepath.Join(f.root, f.keys[id])); !os.IsNotExist(err) {
					t.Fatalf("artifact %d object remains after commit: %v", id, err)
				}
			}
			f.assertArtifact(t, f.source, 2)
			packageRefs := int64(3)
			if target == "private problem" {
				packageRefs--
			}
			f.assertArtifact(t, f.pkg, packageRefs)
			metadata, err := submissions.NewService(f.pool, f.root, 0).SourceMetadata(ctx, f.survivorSubmission, f.survivorUser, false)
			if err != nil || metadata.ID != f.source {
				t.Fatalf("surviving submission lost its shared source: metadata=%#v error=%v", metadata, err)
			}
		})
	}
}

type businessDeletionFixture struct {
	pool                                                                          *pgxpool.Pool
	root, agent                                                                   string
	admin, user, survivorUser, problem, survivorProblem, contest, survivorContest int64
	pkg, source, exclusiveSource, completedJob, pendingJob, survivorSubmission    int64
	targetSubmissions, allSubmissions, uploads                                    []int64
	keys                                                                          map[int64]string
}

func newBusinessDeletionFixture(t *testing.T, privateProblem bool) *businessDeletionFixture {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	f := &businessDeletionFixture{pool: pool, root: t.TempDir(), keys: make(map[int64]string)}
	t.Cleanup(func() { f.cleanup(t) })
	for i, id := range []*int64{&f.admin, &f.user, &f.survivorUser} {
		role := auth.RoleUser
		if i == 0 {
			role = auth.RoleAdmin
		}
		if err = pool.QueryRow(ctx, `INSERT INTO users(username,email,password_hash,role)
SELECT 'del_'||suffix,'del_'||suffix||'@soj.test','unused',$1 FROM (SELECT substr(md5(random()::text),1,20) AS suffix) u RETURNING id`, role).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	var key string
	if err = pool.QueryRow(ctx, `INSERT INTO artifacts(object_key,kind,state,size_bytes,sha256,content_type,available_at,ref_count)
VALUES('delete-package-'||md5(random()::text),'problem_package','available',7,repeat('a',64),'application/zstd',now(),2) RETURNING id,object_key`).Scan(&f.pkg, &key); err != nil {
		t.Fatal(err)
	}
	f.keys[f.pkg] = key
	if err = os.WriteFile(filepath.Join(f.root, key), []byte("package"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []*int64{&f.problem, &f.survivorProblem} {
		if err = pool.QueryRow(ctx, `INSERT INTO problems(title,content,difficulty,candidate_partitions,package_artifact_id,package_manifest)
VALUES('Deletion fixture','','Easy',ARRAY['delete-fixture'],$1,'{}') RETURNING id`, f.pkg).Scan(id); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE problems SET package_manifest=jsonb_build_object('schemaVersion',1,'problemId',id) WHERE id=$1`, *id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []*int64{&f.contest, &f.survivorContest} {
		if err = pool.QueryRow(ctx, `INSERT INTO contests(title,description,starts_at,ends_at,visible)
VALUES('Deletion fixture','',now()-interval '1 hour',now()+interval '1 hour',true) RETURNING id`).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `INSERT INTO contest_problems(contest_id,problem_id,ordinal) VALUES($1,$2,1)`, f.survivorContest, f.survivorProblem); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO contest_registrations(contest_id,user_id) VALUES($1,$2),($3,$4)`, f.contest, f.user, f.survivorContest, f.survivorUser); err != nil {
		t.Fatal(err)
	}
	if !privateProblem {
		if _, err = pool.Exec(ctx, `INSERT INTO contest_problems(contest_id,problem_id,ordinal) VALUES($1,$2,1)`, f.contest, f.problem); err != nil {
			t.Fatal(err)
		}
	}
	service := submissions.NewService(pool, f.root, 0)
	for i := range 2 {
		payload := []byte("int main(void) { return 0; }\n")
		digest := sha256.Sum256(payload)
		path := filepath.Join(f.root, fmt.Sprintf(".upload-delete-%d", i))
		if err = os.WriteFile(path, payload, 0600); err != nil {
			t.Fatal(err)
		}
		source := submissions.StagedSource{Path: path, Filename: "source.c", ContentType: "text/x-c", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(payload))}
		var created submissions.Submission
		if privateProblem {
			created, err = service.CreateAdminTestStaged(ctx, f.admin, f.problem, source)
		} else {
			created, err = service.CreateStaged(ctx, f.user, f.problem, f.contest, source)
		}
		if err != nil {
			t.Fatal(err)
		}
		f.targetSubmissions = append(f.targetSubmissions, created.ID)
		f.allSubmissions = append(f.allSubmissions, created.ID)
		metadata, err := service.SourceMetadata(ctx, created.ID, 0, true)
		if err != nil {
			t.Fatal(err)
		}
		f.keys[metadata.ID] = strconv.FormatInt(metadata.ID, 10)
		if i == 0 {
			f.source, f.completedJob = metadata.ID, *created.CurrentJobID
		} else {
			f.exclusiveSource, f.pendingJob = metadata.ID, *created.CurrentJobID
		}
	}
	// A separate submission/job keeps both the package and source alive. Each
	// durable edge is counted independently, as with rejudge's historical Jobs.
	if err = pool.QueryRow(ctx, `INSERT INTO submissions(user_id,problem_id,contest_id,kind,source_artifact_id)
VALUES($1,$2,$3,'contest',$4) RETURNING id`, f.survivorUser, f.survivorProblem, f.survivorContest, f.source).Scan(&f.survivorSubmission); err != nil {
		t.Fatal(err)
	}
	f.allSubmissions = append(f.allSubmissions, f.survivorSubmission)
	var survivorJob int64
	if err = pool.QueryRow(ctx, `INSERT INTO judge_jobs(submission_id,source_artifact_id,package_artifact_id,package_sha256,manifest,eligible_partitions)
SELECT $1,$2,$3,repeat('a',64),package_manifest,candidate_partitions FROM problems WHERE id=$4 RETURNING id`, f.survivorSubmission, f.source, f.pkg, f.survivorProblem).Scan(&survivorJob); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE submissions SET current_job_id=$2 WHERE id=$1`, f.survivorSubmission, survivorJob); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE artifacts SET ref_count=ref_count+CASE WHEN id=$1 THEN 2 ELSE 1 END WHERE id IN ($1,$2)`, f.source, f.pkg); err != nil {
		t.Fatal(err)
	}
	f.agent = fmt.Sprintf("%08x-0000-4000-8000-%012x", f.problem, f.problem)
	partition := "delete-fixture-" + strconv.FormatInt(f.problem, 10)
	if _, err = pool.Exec(ctx, `INSERT INTO agent_sessions(id,partition) VALUES($1,$2)`, f.agent, partition); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE judge_jobs SET state='finishing',agent_session_id=$2,assigned_partition=$3,execution_id='delete-fixture' WHERE id=ANY($1)`, []int64{f.completedJob, f.pendingJob}, f.agent, partition); err != nil {
		t.Fatal(err)
	}
	control := judge.NewControlServer(pool, f.root, "delete-fixture-token")
	for i, kind := range []string{"judge_result", "judge_log", "judge_result", "judge_log"} {
		job := f.completedJob
		if i >= 2 {
			job = f.pendingJob
		}
		req := httptest.NewRequest("POST", "/artifacts?kind="+kind, bytes.NewBufferString("archived "+kind))
		req.Header.Set("Authorization", "Bearer delete-fixture-token")
		req.Header.Set("X-SOJ-Agent-Session", f.agent)
		req.Header.Set("Content-Type", "application/octet-stream")
		recorder := httptest.NewRecorder()
		control.ArtifactHTTP(recorder, req, strconv.FormatInt(job, 10), "")
		if recorder.Code != 201 {
			t.Fatalf("upload %s: status=%d body=%s", kind, recorder.Code, recorder.Body.String())
		}
		var uploaded struct {
			ArtifactID int64 `json:"artifactId"`
		}
		if err = json.Unmarshal(recorder.Body.Bytes(), &uploaded); err != nil {
			t.Fatal(err)
		}
		f.uploads = append(f.uploads, uploaded.ArtifactID)
		f.keys[uploaded.ArtifactID] = strconv.FormatInt(uploaded.ArtifactID, 10)
	}
	if _, err = control.CompleteJob(ctx, &judgev1.CompleteJobRequest{
		AgentSessionId: f.agent, JobId: f.completedJob, FinishedAt: timestamppb.Now(),
		ExecutionTerminalState: sojv1.ExecutionTerminalState_EXECUTION_TERMINAL_STATE_COMPLETED,
		ResultArtifactId:       &f.uploads[0], LogArtifactId: &f.uploads[1],
		Result: &judgev1.StructuredResult{Verdict: sojv1.Verdict_VERDICT_ACCEPTED, Score: "10"},
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *businessDeletionFixture) assertArtifact(t *testing.T, id, want int64) {
	t.Helper()
	var refs, actual int64
	err := f.pool.QueryRow(t.Context(), `SELECT a.ref_count,
(SELECT count(*) FROM problems WHERE package_artifact_id=a.id)+
(SELECT count(*) FROM submissions WHERE source_artifact_id=a.id)+
(SELECT count(*) FROM judge_jobs WHERE source_artifact_id=a.id)+
(SELECT count(*) FROM judge_jobs WHERE package_artifact_id=a.id)+
(SELECT count(*) FROM judge_jobs WHERE result_artifact_id=a.id)+
(SELECT count(*) FROM judge_jobs WHERE log_artifact_id=a.id)
FROM artifacts a WHERE a.id=$1`, id).Scan(&refs, &actual)
	if err != nil || refs != want || refs != actual {
		t.Fatalf("artifact %d references: stored=%d actual=%d want=%d error=%v", id, refs, actual, want, err)
	}
	if _, err = os.Stat(filepath.Join(f.root, f.keys[id])); err != nil {
		t.Fatalf("referenced artifact %d object missing: %v", id, err)
	}
}

func (f *businessDeletionFixture) cleanup(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	artifactIDs := make([]int64, 0, len(f.keys))
	for id := range f.keys {
		artifactIDs = append(artifactIDs, id)
	}
	for _, statement := range []struct {
		query string
		arg   any
	}{
		{`UPDATE submissions SET current_job_id=NULL WHERE id=ANY($1::bigint[])`, f.allSubmissions},
		{`UPDATE judge_jobs SET result_artifact_id=NULL,log_artifact_id=NULL WHERE submission_id=ANY($1::bigint[])`, f.allSubmissions},
		{`DELETE FROM artifacts WHERE upload_job_id IN (SELECT id FROM judge_jobs WHERE submission_id=ANY($1::bigint[]))`, f.allSubmissions},
		{`DELETE FROM judge_jobs WHERE submission_id=ANY($1::bigint[])`, f.allSubmissions},
		{`DELETE FROM submissions WHERE id=ANY($1::bigint[])`, f.allSubmissions},
		{`DELETE FROM contest_problems WHERE contest_id=ANY($1::bigint[])`, []int64{f.contest, f.survivorContest}},
		{`DELETE FROM contest_registrations WHERE contest_id=ANY($1::bigint[])`, []int64{f.contest, f.survivorContest}},
		{`DELETE FROM contests WHERE id=ANY($1::bigint[])`, []int64{f.contest, f.survivorContest}},
		{`DELETE FROM problems WHERE id=ANY($1::bigint[])`, []int64{f.problem, f.survivorProblem}},
		{`DELETE FROM artifacts WHERE id=ANY($1::bigint[])`, artifactIDs},
		{`DELETE FROM users WHERE id=ANY($1::bigint[])`, []int64{f.admin, f.user, f.survivorUser}},
		{`DELETE FROM agent_sessions WHERE id=$1`, f.agent},
	} {
		if _, err := f.pool.Exec(ctx, statement.query, statement.arg); err != nil {
			t.Errorf("cleanup deletion fixture: %v", err)
		}
	}
}
