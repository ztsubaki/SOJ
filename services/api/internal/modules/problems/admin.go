package problems

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ztsubaki/soj/services/api/internal/modules/artifacts"
	"github.com/ztsubaki/soj/services/api/internal/modules/auth"
)

var ErrUsedByContest = errors.New("problem has been used by a contest")

type AdminProblem struct {
	ID         int64
	Title      string
	Difficulty string
	Tags       []string
	Content    string
	Enabled    bool
	Partitions []string
	Package    *Package
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Package struct {
	ArtifactID int64
	SHA256     string
	SizeBytes  uint64
	Manifest   map[string]any
	UploadedAt time.Time
}

type AdminInput struct {
	Title      string
	Difficulty string
	Tags       []string
	Partitions []string
	Enabled    bool
	Content    string
}

type AdminPatch struct {
	Title      *string
	Difficulty *string
	Tags       *[]string
	Partitions *[]string
	Enabled    *bool
	Content    *string
}

func (s *Service) AdminList(ctx context.Context, query string, enabled *bool, page, size int) ([]AdminProblem, int, error) {
	where := []string{"1=1"}
	args := []any{}
	if query != "" {
		args = append(args, "%"+query+"%")
		where = append(where, fmt.Sprintf("(p.id::text ILIKE $%d OR p.title ILIKE $%d)", len(args), len(args)))
	}
	if enabled != nil {
		args = append(args, *enabled)
		where = append(where, fmt.Sprintf("p.enabled=$%d", len(args)))
	}
	clause := strings.Join(where, " AND ")
	var total int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM problems p WHERE "+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, size, (page-1)*size)
	rows, err := s.pool.Query(ctx, adminSelectSQL+" WHERE "+clause+fmt.Sprintf(" ORDER BY p.created_at DESC,p.id ASC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []AdminProblem{}
	for rows.Next() {
		item, err := scanAdminProblem(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (s *Service) AdminGet(ctx context.Context, id int64) (AdminProblem, error) {
	row := s.pool.QueryRow(ctx, adminSelectSQL+" WHERE p.id=$1", id)
	item, err := scanAdminProblem(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminProblem{}, ErrNotFound
	}
	return item, err
}

func (s *Service) AdminCreate(ctx context.Context, actorID int64, input AdminInput) (AdminProblem, error) {
	if err := validateAdminInput(input); err != nil {
		return AdminProblem{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AdminProblem{}, err
	}
	defer tx.Rollback(ctx)
	var id int64
	if err = tx.QueryRow(ctx, `INSERT INTO problems(title,content,difficulty,enabled,candidate_partitions,created_by,tags) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, input.Title, input.Content, input.Difficulty, input.Enabled, input.Partitions, actorID, normalizedTags(input.Tags)).Scan(&id); err != nil {
		return AdminProblem{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return AdminProblem{}, err
	}
	return s.AdminGet(ctx, id)
}

func (s *Service) AdminUpdate(ctx context.Context, actorID int64, id int64, patch AdminPatch) (AdminProblem, error) {
	if patch.Title == nil && patch.Difficulty == nil && patch.Tags == nil && patch.Partitions == nil && patch.Enabled == nil && patch.Content == nil {
		return AdminProblem{}, errors.New("empty problem patch")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AdminProblem{}, err
	}
	defer tx.Rollback(ctx)
	before, err := adminGetForUpdate(ctx, tx, id)
	if err != nil {
		return AdminProblem{}, err
	}
	after := before
	if patch.Title != nil {
		after.Title = *patch.Title
	}
	if patch.Difficulty != nil {
		after.Difficulty = *patch.Difficulty
	}
	if patch.Tags != nil {
		after.Tags = *patch.Tags
	}
	if patch.Partitions != nil {
		after.Partitions = *patch.Partitions
	}
	if patch.Enabled != nil {
		after.Enabled = *patch.Enabled
	}
	if patch.Content != nil {
		after.Content = *patch.Content
	}
	if err = validateAdminInput(AdminInput{Title: after.Title, Difficulty: after.Difficulty, Tags: after.Tags, Partitions: after.Partitions, Enabled: after.Enabled, Content: after.Content}); err != nil {
		return AdminProblem{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE problems SET title=$2,content=$3,difficulty=$4,enabled=$5,candidate_partitions=$6,tags=$7,updated_at=now() WHERE id=$1`, id, after.Title, after.Content, after.Difficulty, after.Enabled, after.Partitions, normalizedTags(after.Tags)); err != nil {
		return AdminProblem{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return AdminProblem{}, err
	}
	return s.AdminGet(ctx, id)
}

// AdminDelete permanently removes a problem and its private test submissions.
// A problem that has ever been added to a contest is retained permanently.
func (s *Service) AdminDelete(ctx context.Context, actorID int64, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = auth.RevalidateMutation(ctx, tx, actorID, true); err != nil {
		return err
	}
	var used bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contest_problems WHERE problem_id=$1)`, id).Scan(&used); err != nil {
		return err
	}
	if used {
		return ErrUsedByContest
	}
	if _, err = tx.Exec(ctx, `UPDATE submissions SET current_job_id=NULL WHERE problem_id=$1`, id); err != nil {
		return err
	}
	refs, err := deleteJobs(ctx, tx, `DELETE FROM judge_jobs WHERE submission_id IN (SELECT id FROM submissions WHERE problem_id=$1)`, id)
	if err != nil {
		return err
	}
	moreRefs, err := deleteSubmissions(ctx, tx, `DELETE FROM submissions WHERE problem_id=$1`, id)
	if err != nil {
		return err
	}
	refs = append(refs, moreRefs...)
	var packageID *int64
	if err = tx.QueryRow(ctx, `DELETE FROM problems WHERE id=$1 RETURNING package_artifact_id`, id).Scan(&packageID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if packageID != nil {
		refs = append(refs, *packageID)
	}
	removed, err := artifacts.Release(ctx, tx, refs...)
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	artifacts.RemoveObjects(s.artifactRoot, removed)
	return nil
}

func deleteJobs(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]int64, error) {
	rows, err := tx.Query(ctx, query+` RETURNING source_artifact_id,package_artifact_id,result_artifact_id,log_artifact_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []int64{}
	for rows.Next() {
		var source, pkg int64
		var result, log *int64
		if err := rows.Scan(&source, &pkg, &result, &log); err != nil {
			return nil, err
		}
		refs = append(refs, source, pkg)
		if result != nil {
			refs = append(refs, *result)
		}
		if log != nil {
			refs = append(refs, *log)
		}
	}
	return refs, rows.Err()
}

func deleteSubmissions(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]int64, error) {
	rows, err := tx.Query(ctx, query+` RETURNING source_artifact_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []int64{}
	for rows.Next() {
		var source int64
		if err := rows.Scan(&source); err != nil {
			return nil, err
		}
		refs = append(refs, source)
	}
	return refs, rows.Err()
}

const adminSelectSQL = `SELECT p.id,p.title,p.difficulty,p.content,p.enabled,p.candidate_partitions,p.created_at,p.updated_at,
 a.id,a.sha256,a.size_bytes,p.package_manifest,a.available_at,
 p.tags
 FROM problems p
 LEFT JOIN artifacts a ON a.id=p.package_artifact_id AND a.kind='problem_package' AND a.state='available'

`

type rowScanner interface{ Scan(...any) error }

func scanAdminProblem(row rowScanner) (AdminProblem, error) {
	var item AdminProblem
	var packageID *int64
	var sha *string
	var packageSize *int64
	var manifest []byte
	var uploadedAt *time.Time
	if err := row.Scan(&item.ID, &item.Title, &item.Difficulty, &item.Content, &item.Enabled, &item.Partitions, &item.CreatedAt, &item.UpdatedAt, &packageID, &sha, &packageSize, &manifest, &uploadedAt, &item.Tags); err != nil {
		return AdminProblem{}, err
	}
	if packageID != nil {
		pkg := Package{ArtifactID: *packageID, SHA256: *sha, SizeBytes: uint64(*packageSize), UploadedAt: *uploadedAt}
		if err := json.Unmarshal(manifest, &pkg.Manifest); err != nil {
			return AdminProblem{}, err
		}
		item.Package = &pkg
	}
	return item, nil
}

func adminGetForUpdate(ctx context.Context, tx pgx.Tx, id int64) (AdminProblem, error) {
	var lockedID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM problems WHERE id=$1 FOR UPDATE`, id).Scan(&lockedID); errors.Is(err, pgx.ErrNoRows) {
		return AdminProblem{}, ErrNotFound
	} else if err != nil {
		return AdminProblem{}, err
	}
	row := tx.QueryRow(ctx, adminSelectSQL+" WHERE p.id=$1", id)
	item, err := scanAdminProblem(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminProblem{}, ErrNotFound
	}
	return item, err
}

func normalizedTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}

func validateAdminInput(input AdminInput) error {
	if strings.TrimSpace(input.Title) == "" || strings.TrimSpace(input.Content) == "" {
		return errors.New("problem fields are invalid")
	}
	if input.Difficulty != "Easy" && input.Difficulty != "Medium" && input.Difficulty != "Hard" {
		return errors.New("problem difficulty is invalid")
	}
	if len(input.Partitions) == 0 || !distinctASCII(input.Partitions, 255) || !distinctASCII(input.Tags, 64) {
		return errors.New("problem partitions or tags are invalid")
	}
	return nil
}

func distinctASCII(values []string, max int) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if len(value) == 0 || len(value) > max {
			return false
		}
		for _, r := range value {
			if r < 0x21 || r > 0x7e {
				return false
			}
		}
		if _, ok := seen[value]; ok {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}
