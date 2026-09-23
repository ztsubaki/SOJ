package submissions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ztsubaki/soj/services/api/internal/modules/artifacts"
	"github.com/ztsubaki/soj/services/api/internal/modules/auth"
	"github.com/ztsubaki/soj/services/api/internal/validation"
)

var (
	ErrNotFound     = errors.New("submission not found")
	ErrCannotCancel = errors.New("submission cannot be cancelled")
)

const DefaultMaxSourceSize = 10 << 20

type Submission struct {
	ID                   int64
	ProblemTitle         string
	UserID               int64
	ProblemID            int64
	ContestID            *int64 `json:"contestId,omitempty"`
	Kind                 string
	Status               string
	CreatedAt, UpdatedAt time.Time
	Verdict              string  `json:"verdict,omitempty"`
	Score                *string `json:"score,omitempty"`
	JudgeMessage         *string `json:"judgeMessage,omitempty"`
	CurrentJobID         *int64
	CurrentLogArtifactID *int64
}

type ArtifactMetadata struct {
	ID                            int64
	ContentType, Filename, SHA256 string
	SizeBytes                     uint64
}

// StagedSource is a source file that the HTTP data plane has already streamed
// into the local Artifact backend.  Its path is temporary and is consumed by
// CreateStaged; callers must remove it if creation is not attempted.
type StagedSource struct {
	Path, Filename, ContentType, SHA256 string
	Size                                int64
}

type Job struct {
	ID, SubmissionID, PackageArtifactID                              int64
	State, PackageSHA256                                             string
	AgentSessionID, AssignedPartition, SlurmJobID                    *string
	Manifest                                                         map[string]any
	EligiblePartitions                                               []string
	Verdict, Score, Message                                          *string
	ResultArtifactID, LogArtifactID                                  *int64
	CancelRequestedAt, ClaimedAt, SubmittedAt, StartedAt, FinishedAt *time.Time
	CreatedAt                                                        time.Time
}
type Service struct {
	pool          *pgxpool.Pool
	artifactRoot  string
	maxSourceSize int64
}

func NewService(pool *pgxpool.Pool, artifactRoot string, max int64) *Service {
	if pool == nil {
		return nil
	}
	if max <= 0 {
		max = DefaultMaxSourceSize
	}
	return &Service{pool: pool, artifactRoot: filepath.Clean(artifactRoot), maxSourceSize: max}
}

func (s *Service) MaxSourceSize() int64 { return s.maxSourceSize }

// CanCreate checks the target operation before an upload handler reads source
// bytes. CreateStaged repeats the same checks in its write transaction, so a
// contest or package change racing the upload cannot bypass authorization.
func (s *Service) CanCreate(ctx context.Context, userID int64, problemID, contestID int64) error {
	if userID <= 0 || problemID <= 0 || contestID <= 0 {
		return errors.New("invalid contest submission")
	}
	var runnable, allowed bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM problems p JOIN artifacts a ON a.id=p.package_artifact_id AND a.kind='problem_package' AND a.state='available' WHERE p.id=$1 AND p.enabled AND cardinality(p.candidate_partitions)>0)`, problemID).Scan(&runnable); err != nil || !runnable {
		return errors.New("problem has no runnable package")
	}
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contests c JOIN contest_problems cp ON cp.contest_id=c.id AND cp.problem_id=$2 JOIN contest_registrations r ON r.contest_id=c.id AND r.user_id=$3 AND r.disqualified_at IS NULL WHERE c.id=$1 AND c.visible AND now()>=c.starts_at AND now()<c.ends_at)`, contestID, problemID, userID).Scan(&allowed); err != nil || !allowed {
		return errors.New("contest submission is not allowed")
	}
	return nil
}

// CreateStaged atomically consumes an HTTP-staged source file.  The rename is
// within the Artifact backend directory, therefore no full source buffer is
// introduced between the BFF stream and durable artifact object.
func (s *Service) CreateStaged(ctx context.Context, userID int64, problemID, contestID int64, source StagedSource) (Submission, error) {
	if userID <= 0 || problemID <= 0 || contestID <= 0 {
		return Submission{}, errors.New("invalid staged contest submission")
	}
	return s.createStaged(ctx, userID, problemID, contestID, "contest", source)
}

// CreateAdminTestStaged consumes an Admin upload. The final transaction
// revalidates the administrator and runnable package before binding it.
func (s *Service) CreateAdminTestStaged(ctx context.Context, actorID int64, problemID int64, source StagedSource) (Submission, error) {
	if actorID <= 0 || problemID <= 0 {
		return Submission{}, errors.New("invalid staged admin test submission")
	}
	return s.createStaged(ctx, actorID, problemID, 0, "admin_test", source)
}

// createStaged owns the file handoff and removes the object if binding fails.
func (s *Service) createStaged(ctx context.Context, userID int64, problemID, contestID int64, kind string, source StagedSource) (Submission, error) {
	if source.Path == "" || source.Size <= 0 || source.Size > s.maxSourceSize || !validation.SHA256(source.SHA256) {
		return Submission{}, errors.New("invalid staged source")
	}
	if filepath.Dir(source.Path) != s.artifactRoot {
		return Submission{}, errors.New("staged source is outside artifact backend")
	}
	var aid int64
	if err := s.pool.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('artifacts','id'))`).Scan(&aid); err != nil {
		return Submission{}, err
	}
	path := filepath.Join(s.artifactRoot, strconv.FormatInt(aid, 10))
	if err := os.Rename(source.Path, path); err != nil {
		return Submission{}, err
	}
	id, err := s.create(ctx, aid, userID, problemID, contestID, kind, source)
	if err != nil {
		_ = os.Remove(path)
		return Submission{}, err
	}
	return s.Get(ctx, id, 0, true)
}

// CanCreateAdminTest performs the target checks before the Admin BFF upload
// endpoint reads source bytes. create repeats them in its transaction.
func (s *Service) CanCreateAdminTest(ctx context.Context, problemID int64) error {
	if problemID <= 0 {
		return errors.New("invalid admin test submission")
	}
	var runnable bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM problems p JOIN artifacts a ON a.id=p.package_artifact_id AND a.kind='problem_package' AND a.state='available' WHERE p.id=$1 AND p.enabled AND cardinality(p.candidate_partitions)>0)`, problemID).Scan(&runnable); err != nil || !runnable {
		return errors.New("problem has no runnable package")
	}

	return nil
}

func (s *Service) create(ctx context.Context, aid, user int64, problem, contest int64, kind string, source StagedSource) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if err = auth.RevalidateMutation(ctx, tx, user, kind == "admin_test"); err != nil {
		return 0, err
	}
	var pkg int64
	var sha string
	var manifest []byte
	var parts []string
	err = tx.QueryRow(ctx, `SELECT p.package_artifact_id,a.sha256,p.package_manifest,p.candidate_partitions FROM problems p JOIN artifacts a ON a.id=p.package_artifact_id AND a.kind='problem_package' AND a.state='available' WHERE p.id=$1 AND p.enabled FOR SHARE`, problem).Scan(&pkg, &sha, &manifest, &parts)
	if err != nil || len(parts) == 0 {
		return 0, errors.New("problem has no runnable package")
	}
	if kind == "contest" {
		var allowed bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contests c JOIN contest_problems cp ON cp.contest_id=c.id AND cp.problem_id=$2 JOIN contest_registrations r ON r.contest_id=c.id AND r.user_id=$3 AND r.disqualified_at IS NULL WHERE c.id=$1 AND c.visible AND now()>=c.starts_at AND now()<c.ends_at)`, contest, problem, user).Scan(&allowed); err != nil || !allowed {
			return 0, errors.New("contest submission is not allowed")
		}
	}
	if source.ContentType == "" {
		source.ContentType = "application/octet-stream"
	}
	if source.Filename == "" {
		source.Filename = "submission"
	}
	if _, err = tx.Exec(ctx, `INSERT INTO artifacts(id,object_key,kind,state,size_bytes,sha256,content_type,available_at) OVERRIDING SYSTEM VALUE VALUES($1,$2,'submission_source','available',$3,$4,$5,now())`, aid, strconv.FormatInt(aid, 10), source.Size, source.SHA256, source.ContentType); err != nil {
		return 0, err
	}
	var id int64
	if err = tx.QueryRow(ctx, `INSERT INTO submissions(user_id,problem_id,contest_id,kind,source_artifact_id,source_filename,source_content_type) VALUES($1,$2,NULLIF($3,0),$4,$5,$6,$7) RETURNING id`, user, problem, contest, kind, aid, source.Filename, source.ContentType).Scan(&id); err != nil {
		return 0, err
	}
	if err = artifacts.Retain(ctx, tx, aid); err != nil {
		return 0, err
	}
	var job int64
	if err = tx.QueryRow(ctx, `INSERT INTO judge_jobs(submission_id,source_artifact_id,package_artifact_id,package_sha256,manifest,eligible_partitions) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, id, aid, pkg, sha, manifest, parts).Scan(&job); err != nil {
		return 0, err
	}
	if err = artifacts.Retain(ctx, tx, aid); err != nil {
		return 0, err
	}
	if err = artifacts.Retain(ctx, tx, pkg); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE submissions SET current_job_id=$2 WHERE id=$1`, id, job); err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
}

func (s *Service) Get(ctx context.Context, id int64, user int64, admin bool) (Submission, error) {
	q := submissionSelect + ` WHERE s.id=$1`
	args := []any{id}
	if !admin {
		q += ` AND s.user_id=$2 AND s.kind='contest'`
		args = append(args, user)
	}
	var v Submission
	err := s.pool.QueryRow(ctx, q, args...).Scan(submissionScanArgs(&v)...)
	if err != nil {
		return Submission{}, ErrNotFound
	}
	return v, nil
}

func (s *Service) SourceMetadata(ctx context.Context, id int64, user int64, admin bool) (ArtifactMetadata, error) {
	where := "s.id=$1"
	args := []any{id}
	if !admin {
		where += " AND s.user_id=$2 AND s.kind='contest'"
		args = append(args, user)
	}
	return s.artifactMetadata(ctx, `SELECT a.id,a.content_type,s.source_filename,a.sha256,a.size_bytes FROM submissions s JOIN artifacts a ON a.id=s.source_artifact_id AND a.kind='submission_source' AND a.state='available' WHERE `+where, args...)
}

func (s *Service) CurrentLogMetadata(ctx context.Context, id int64, user int64, admin bool) (ArtifactMetadata, error) {
	where := "s.id=$1"
	args := []any{id}
	if !admin {
		where += " AND s.user_id=$2 AND s.kind='contest'"
		args = append(args, user)
	}
	return s.artifactMetadata(ctx, `SELECT a.id,a.content_type,'judge.log',a.sha256,a.size_bytes FROM submissions s JOIN judge_jobs j ON j.id=s.current_job_id JOIN artifacts a ON a.id=j.log_artifact_id AND a.kind='judge_log' AND a.state='available' WHERE `+where, args...)
}

func (s *Service) artifactMetadata(ctx context.Context, query string, args ...any) (ArtifactMetadata, error) {
	var value ArtifactMetadata
	err := s.pool.QueryRow(ctx, query, args...).Scan(&value.ID, &value.ContentType, &value.Filename, &value.SHA256, &value.SizeBytes)
	if err != nil {
		return ArtifactMetadata{}, ErrNotFound
	}
	return value, nil
}

func (s *Service) ListMine(ctx context.Context, user int64, contestID, problemID *int64, wantedStatus *string, page, size int) ([]Submission, int, error) {
	where := "s.kind='contest' AND s.user_id=$1"
	args := []any{user}
	if contestID != nil {
		args = append(args, *contestID)
		where += fmt.Sprintf(" AND s.contest_id=$%d", len(args))
	}
	if problemID != nil {
		args = append(args, *problemID)
		where += fmt.Sprintf(" AND s.problem_id=$%d", len(args))
	}
	if wantedStatus != nil {
		args = append(args, *wantedStatus)
		where += fmt.Sprintf(" AND "+submissionStatusSQL+"=$%d", len(args))
	}
	return s.listWhere(ctx, where, args, page, size)
}
func (s *Service) ListAdmin(ctx context.Context, query, verdict, user, problem, kind string, page, size int) ([]Submission, int, error) {
	where := "1=1"
	args := []any{}
	if query != "" {
		args = append(args, "%"+query+"%")
		where += fmt.Sprintf(" AND (s.id::text ILIKE $%d OR u.username ILIKE $%d OR p.title ILIKE $%d)", len(args), len(args), len(args))
	}
	if verdict != "" {
		args = append(args, verdict)
		where += fmt.Sprintf(" AND j.verdict=$%d", len(args))
	}
	if user != "" {
		args = append(args, user)
		where += fmt.Sprintf(" AND s.user_id=$%d", len(args))
	}
	if problem != "" {
		args = append(args, problem)
		where += fmt.Sprintf(" AND s.problem_id=$%d", len(args))
	}
	if kind != "" {
		if kind != "contest" && kind != "admin_test" {
			return nil, 0, errors.New("invalid submission kind")
		}
		args = append(args, kind)
		where += fmt.Sprintf(" AND s.kind=$%d", len(args))
	}
	return s.listWhere(ctx, where, args, page, size)
}

func (s *Service) ListJobs(ctx context.Context, submissionID int64, page, size int) ([]Job, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM judge_jobs WHERE submission_id=$1`, submissionID).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM submissions WHERE id=$1)`, submissionID).Scan(&exists); err != nil || !exists {
			return nil, 0, ErrNotFound
		}
	}
	rows, err := s.pool.Query(ctx, jobSelect+` WHERE j.submission_id=$1 ORDER BY j.created_at ASC,j.id ASC LIMIT $2 OFFSET $3`, submissionID, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []Job{}
	for rows.Next() {
		item, err := scanJob(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (s *Service) JobArtifactMetadata(ctx context.Context, submissionID, jobID int64, kind string) (ArtifactMetadata, error) {
	column, filename := "", ""
	switch kind {
	case "judge_result":
		column, filename = "j.result_artifact_id", "judge.result"
	case "judge_log":
		column, filename = "j.log_artifact_id", "judge.log"
	default:
		return ArtifactMetadata{}, ErrNotFound
	}
	return s.artifactMetadata(ctx, `SELECT a.id,a.content_type,'`+filename+`',a.sha256,a.size_bytes FROM judge_jobs j JOIN artifacts a ON a.id=`+column+` AND a.kind='`+kind+`' AND a.state='available' WHERE j.submission_id=$1 AND j.id=$2`, submissionID, jobID)
}

func (s *Service) RejudgeAsAdmin(ctx context.Context, actorID int64, id int64) (Submission, int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Submission{}, 0, err
	}
	defer tx.Rollback(ctx)
	var source, pkg int64
	var sha, state string
	var manifest []byte
	var parts []string
	err = tx.QueryRow(ctx, `SELECT j.source_artifact_id,j.state FROM submissions s JOIN judge_jobs j ON j.id=s.current_job_id WHERE s.id=$1 FOR UPDATE`, id).Scan(&source, &state)
	if err != nil {
		return Submission{}, 0, ErrNotFound
	}
	if state != "completed" && state != "cancelled" && state != "internal_error" {
		return Submission{}, 0, errors.New("current job is not terminal")
	}
	if err = tx.QueryRow(ctx, `SELECT p.package_artifact_id,a.sha256,p.package_manifest,p.candidate_partitions FROM submissions s JOIN problems p ON p.id=s.problem_id JOIN artifacts a ON a.id=p.package_artifact_id AND a.kind='problem_package' AND a.state='available' WHERE s.id=$1 AND p.enabled FOR SHARE`, id).Scan(&pkg, &sha, &manifest, &parts); err != nil || len(parts) == 0 {
		return Submission{}, 0, errors.New("problem has no runnable package")
	}
	var job int64
	if err = tx.QueryRow(ctx, `INSERT INTO judge_jobs(submission_id,source_artifact_id,package_artifact_id,package_sha256,manifest,eligible_partitions) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, id, source, pkg, sha, manifest, parts).Scan(&job); err != nil {
		return Submission{}, 0, err
	}
	if err = artifacts.Retain(ctx, tx, source); err != nil {
		return Submission{}, 0, err
	}
	if err = artifacts.Retain(ctx, tx, pkg); err != nil {
		return Submission{}, 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE submissions SET current_job_id=$2,updated_at=now() WHERE id=$1`, id, job); err != nil {
		return Submission{}, 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Submission{}, 0, err
	}
	result, err := s.Get(ctx, id, 0, true)
	return result, job, err
}

// AdminCancel marks the current active job for cancellation exactly once.  It
// does not rewrite the state: the Agent remains the authority that reaches a
// terminal cancelled state.
func (s *Service) AdminCancel(ctx context.Context, actorID int64, id int64) (Submission, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Submission{}, err
	}
	defer tx.Rollback(ctx)
	var state string
	var requested *time.Time
	err = tx.QueryRow(ctx, `SELECT j.state,j.cancel_requested_at FROM submissions s JOIN judge_jobs j ON j.id=s.current_job_id WHERE s.id=$1 FOR UPDATE`, id).Scan(&state, &requested)
	if err != nil {
		return Submission{}, ErrNotFound
	}
	if state == "pending" {
		if _, err = tx.Exec(ctx, `UPDATE judge_jobs SET state='cancelled',cancel_requested_at=COALESCE(cancel_requested_at,now()),finished_at=COALESCE(finished_at,now()) WHERE id=(SELECT current_job_id FROM submissions WHERE id=$1)`, id); err != nil {
			return Submission{}, err
		}
	} else if requested == nil && (state == "completed" || state == "internal_error") {
		return Submission{}, ErrCannotCancel
	} else if requested == nil && state != "cancelled" {
		if _, err = tx.Exec(ctx, `UPDATE judge_jobs SET cancel_requested_at=now() WHERE submission_id=$1 AND id=(SELECT current_job_id FROM submissions WHERE id=$1)`, id); err != nil {
			return Submission{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Submission{}, err
	}
	return s.Get(ctx, id, 0, true)
}

const submissionStatusSQL = `COALESCE(CASE j.state WHEN 'completed' THEN j.verdict WHEN 'cancelled' THEN 'cancelled' WHEN 'internal_error' THEN 'system_error' WHEN 'claimed' THEN 'pending' WHEN 'submitted' THEN 'pending' WHEN 'running' THEN 'judging' WHEN 'finishing' THEN 'judging' ELSE 'pending' END,'pending')`

const submissionSelect = `SELECT s.id,s.user_id,s.problem_id,p.title,s.contest_id,s.kind,
 ` + submissionStatusSQL + `,
 s.created_at,GREATEST(s.updated_at,j.updated_at),COALESCE(j.verdict,''),CASE WHEN j.state='completed' THEN j.score::text ELSE NULL END,CASE WHEN j.state='completed' THEN j.message ELSE NULL END,s.current_job_id,j.log_artifact_id
 FROM submissions s JOIN problems p ON p.id=s.problem_id LEFT JOIN judge_jobs j ON j.id=s.current_job_id`

func submissionScanArgs(item *Submission) []any {
	return []any{&item.ID, &item.UserID, &item.ProblemID, &item.ProblemTitle, &item.ContestID, &item.Kind, &item.Status, &item.CreatedAt, &item.UpdatedAt, &item.Verdict, &item.Score, &item.JudgeMessage, &item.CurrentJobID, &item.CurrentLogArtifactID}
}

func (s *Service) listWhere(ctx context.Context, where string, args []any, page, size int) ([]Submission, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM submissions s JOIN problems p ON p.id=s.problem_id LEFT JOIN users u ON u.id=s.user_id LEFT JOIN judge_jobs j ON j.id=s.current_job_id WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, size, (page-1)*size)
	query := submissionSelect + " LEFT JOIN users u ON u.id=s.user_id" + fmt.Sprintf(" WHERE %s ORDER BY s.created_at DESC,s.id DESC LIMIT $%d OFFSET $%d", where, len(args)-1, len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []Submission{}
	for rows.Next() {
		var item Submission
		if err = rows.Scan(submissionScanArgs(&item)...); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

const jobSelect = `SELECT j.id,j.submission_id,j.state,j.agent_session_id,j.assigned_partition,j.slurm_job_id,j.package_artifact_id,j.package_sha256,j.manifest,j.eligible_partitions,j.verdict,j.score::text,j.message,j.result_artifact_id,j.log_artifact_id,j.cancel_requested_at,j.created_at,j.claimed_at,j.submitted_at,j.started_at,j.finished_at FROM judge_jobs j`

type jobScanner interface{ Scan(...any) error }

func scanJob(row jobScanner) (Job, error) {
	var item Job
	var manifest []byte
	if err := row.Scan(&item.ID, &item.SubmissionID, &item.State, &item.AgentSessionID, &item.AssignedPartition, &item.SlurmJobID, &item.PackageArtifactID, &item.PackageSHA256, &manifest, &item.EligiblePartitions, &item.Verdict, &item.Score, &item.Message, &item.ResultArtifactID, &item.LogArtifactID, &item.CancelRequestedAt, &item.CreatedAt, &item.ClaimedAt, &item.SubmittedAt, &item.StartedAt, &item.FinishedAt); err != nil {
		return Job{}, err
	}
	if err := json.Unmarshal(manifest, &item.Manifest); err != nil {
		return Job{}, err
	}
	return item, nil
}
