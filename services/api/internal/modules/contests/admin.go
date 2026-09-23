package contests

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ztsubaki/soj/services/api/internal/modules/artifacts"
	"github.com/ztsubaki/soj/services/api/internal/modules/auth"
)

type AdminContest struct {
	ID               int64
	Title            string
	Description      string
	Status           string
	StartsAt         time.Time
	EndsAt           time.Time
	ParticipantCount int64
	Visible          bool
	ScoringPolicy    string
	Problems         []ProblemSummary
}

type ProblemSummary struct {
	ID         int64
	Title      string
	Difficulty string
	Tags       []string
	Runnable   bool
}

type AdminInput struct {
	Title         string
	StartsAt      time.Time
	EndsAt        time.Time
	Description   string
	Problems      []int64
	Visible       bool
	ScoringPolicy string
}

type AdminPatch struct {
	Title         *string
	StartsAt      *time.Time
	EndsAt        *time.Time
	Description   *string
	Problems      *[]int64
	Visible       *bool
	ScoringPolicy *string
}

func (s *Service) AdminList(ctx context.Context, query, desiredStatus string, page, size int) ([]AdminContest, int, error) {
	where := []string{"1=1"}
	args := []any{}
	if query != "" {
		args = append(args, "%"+query+"%")
		where = append(where, fmt.Sprintf("(c.id::text ILIKE $%d OR c.title ILIKE $%d)", len(args), len(args)))
	}
	statusSQL := derivedStatusSQL("c")
	if desiredStatus != "" {
		args = append(args, desiredStatus)
		where = append(where, fmt.Sprintf("%s=$%d", statusSQL, len(args)))
	}
	clause := strings.Join(where, " AND ")
	var total int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM contests c WHERE "+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, size, (page-1)*size)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT c.id,c.title,c.description,%s,c.starts_at,c.ends_at,c.visible,c.scoring_policy,count(cr.user_id)
FROM contests c LEFT JOIN contest_registrations cr ON cr.contest_id=c.id WHERE %s
GROUP BY c.id ORDER BY c.starts_at DESC,c.id ASC LIMIT $%d OFFSET $%d`, statusSQL, clause, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []AdminContest{}
	for rows.Next() {
		item, err := scanContest(rows)
		if err != nil {
			return nil, 0, err
		}
		item.Problems, err = s.problemSummaries(ctx, item.ID)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (s *Service) AdminGet(ctx context.Context, id int64) (AdminContest, error) {
	row := s.pool.QueryRow(ctx, fmt.Sprintf(`SELECT c.id,c.title,c.description,%s,c.starts_at,c.ends_at,c.visible,c.scoring_policy,count(cr.user_id)
FROM contests c LEFT JOIN contest_registrations cr ON cr.contest_id=c.id WHERE c.id=$1 GROUP BY c.id`, derivedStatusSQL("c")), id)
	item, err := scanContest(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminContest{}, ErrNotFound
	}
	if err != nil {
		return AdminContest{}, err
	}
	item.Problems, err = s.problemSummaries(ctx, id)
	return item, err
}

func (s *Service) AdminCreate(ctx context.Context, actorID int64, input AdminInput) (AdminContest, error) {
	if err := validateInput(input); err != nil {
		return AdminContest{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AdminContest{}, err
	}
	defer tx.Rollback(ctx)
	if err = validateProblems(ctx, tx, input.Problems, input.EndsAt.After(time.Now())); err != nil {
		return AdminContest{}, err
	}
	var id int64
	if err = tx.QueryRow(ctx, `INSERT INTO contests(title,description,starts_at,ends_at,visible,scoring_policy,created_by) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, input.Title, input.Description, input.StartsAt, input.EndsAt, input.Visible, input.ScoringPolicy, actorID).Scan(&id); err != nil {
		return AdminContest{}, err
	}
	if err = replaceProblems(ctx, tx, id, input.Problems); err != nil {
		return AdminContest{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return AdminContest{}, err
	}
	return s.AdminGet(ctx, id)
}

func (s *Service) AdminUpdate(ctx context.Context, actorID int64, id int64, patch AdminPatch) (AdminContest, error) {
	if patch.Title == nil && patch.StartsAt == nil && patch.EndsAt == nil && patch.Description == nil && patch.Problems == nil && patch.Visible == nil && patch.ScoringPolicy == nil {
		return AdminContest{}, errors.New("empty contest patch")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AdminContest{}, err
	}
	defer tx.Rollback(ctx)
	before, err := getContestForUpdate(ctx, tx, id)
	if err != nil {
		return AdminContest{}, err
	}
	after := before
	if patch.Title != nil {
		after.Title = *patch.Title
	}
	if patch.StartsAt != nil {
		after.StartsAt = *patch.StartsAt
	}
	if patch.EndsAt != nil {
		after.EndsAt = *patch.EndsAt
	}
	if patch.Description != nil {
		after.Description = *patch.Description
	}
	if patch.Problems != nil {
		after.Problems = summariesForIDs(*patch.Problems)
	}
	if patch.Visible != nil {
		after.Visible = *patch.Visible
	}
	if patch.ScoringPolicy != nil {
		after.ScoringPolicy = *patch.ScoringPolicy
	}
	if err = validateUpdateWindow(before, after, time.Now()); err != nil {
		return AdminContest{}, err
	}
	problemsChanged := !slices.Equal(problemIDs(before.Problems), problemIDs(after.Problems))
	input := AdminInput{Title: after.Title, StartsAt: after.StartsAt, EndsAt: after.EndsAt, Description: after.Description, Problems: problemIDs(after.Problems), Visible: after.Visible, ScoringPolicy: after.ScoringPolicy}
	if patch.Problems != nil {
		input.Problems = *patch.Problems
	}
	if err = validateInput(input); err != nil {
		return AdminContest{}, err
	}
	if problemsChanged {
		if err = validateProblems(ctx, tx, input.Problems, input.EndsAt.After(time.Now())); err != nil {
			return AdminContest{}, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE contests SET title=$2,description=$3,starts_at=$4,ends_at=$5,visible=$6,scoring_policy=$7,updated_at=now() WHERE id=$1`, id, after.Title, after.Description, after.StartsAt, after.EndsAt, after.Visible, after.ScoringPolicy); err != nil {
		return AdminContest{}, err
	}
	if problemsChanged {
		if err = replaceProblems(ctx, tx, id, input.Problems); err != nil {
			return AdminContest{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return AdminContest{}, err
	}
	return s.AdminGet(ctx, id)
}

// AdminDelete permanently removes a contest, its registrations, and all
// submissions made in that contest.
func (s *Service) AdminDelete(ctx context.Context, actorID int64, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = auth.RevalidateMutation(ctx, tx, actorID, true); err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contests WHERE id=$1 FOR UPDATE)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `UPDATE submissions SET current_job_id=NULL WHERE contest_id=$1`, id); err != nil {
		return err
	}
	refs, err := deleteJobs(ctx, tx, `DELETE FROM judge_jobs WHERE submission_id IN (SELECT id FROM submissions WHERE contest_id=$1)`, id)
	if err != nil {
		return err
	}
	moreRefs, err := deleteSubmissions(ctx, tx, `DELETE FROM submissions WHERE contest_id=$1`, id)
	if err != nil {
		return err
	}
	refs = append(refs, moreRefs...)
	if _, err = tx.Exec(ctx, `DELETE FROM contest_registrations WHERE contest_id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM contest_problems WHERE contest_id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM contests WHERE id=$1`, id); err != nil {
		return err
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

func (s *Service) AdminDisqualify(ctx context.Context, actorID int64, contestID int64, userID int64, disqualified bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var previous *time.Time
	err = tx.QueryRow(ctx, `SELECT disqualified_at FROM contest_registrations WHERE contest_id=$1 AND user_id=$2 FOR UPDATE`, contestID, userID).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if disqualified {
		_, err = tx.Exec(ctx, `UPDATE contest_registrations SET disqualified_at=COALESCE(disqualified_at,now()),disqualified_reason=COALESCE(disqualified_reason,'admin action') WHERE contest_id=$1 AND user_id=$2`, contestID, userID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE contest_registrations SET disqualified_at=NULL,disqualified_reason=NULL WHERE contest_id=$1 AND user_id=$2`, contestID, userID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) problemSummaries(ctx context.Context, contestID int64) ([]ProblemSummary, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.id,p.title,p.difficulty,p.tags
FROM contest_problems cp JOIN problems p ON p.id=cp.problem_id
WHERE cp.contest_id=$1 GROUP BY p.id,cp.ordinal ORDER BY cp.ordinal`, contestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ProblemSummary{}
	for rows.Next() {
		var item ProblemSummary
		if err = rows.Scan(&item.ID, &item.Title, &item.Difficulty, &item.Tags); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func derivedStatusSQL(alias string) string {
	return fmt.Sprintf("CASE WHEN now()>=%s.ends_at THEN 'ended' WHEN now()<%s.starts_at THEN 'upcoming' ELSE 'running' END", alias, alias)
}

type scanner interface{ Scan(...any) error }

func scanContest(row scanner) (AdminContest, error) {
	var c AdminContest
	err := row.Scan(&c.ID, &c.Title, &c.Description, &c.Status, &c.StartsAt, &c.EndsAt, &c.Visible, &c.ScoringPolicy, &c.ParticipantCount)
	return c, err
}
func getContestForUpdate(ctx context.Context, tx pgx.Tx, id int64) (AdminContest, error) {
	var c AdminContest
	err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT id,title,description,%s,starts_at,ends_at,visible,scoring_policy,0 FROM contests c WHERE id=$1 FOR UPDATE`, derivedStatusSQL("c")), id).Scan(&c.ID, &c.Title, &c.Description, &c.Status, &c.StartsAt, &c.EndsAt, &c.Visible, &c.ScoringPolicy, &c.ParticipantCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	rows, err := tx.Query(ctx, `SELECT p.id,p.title,p.difficulty,p.tags FROM contest_problems cp JOIN problems p ON p.id=cp.problem_id WHERE cp.contest_id=$1 GROUP BY p.id,cp.ordinal ORDER BY cp.ordinal`, id)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var p ProblemSummary
		if err = rows.Scan(&p.ID, &p.Title, &p.Difficulty, &p.Tags); err != nil {
			return c, err
		}
		c.Problems = append(c.Problems, p)
	}
	return c, rows.Err()
}
func validateInput(i AdminInput) error {
	if strings.TrimSpace(i.Title) == "" {
		return fmt.Errorf("%w: title is required", ErrInvalidContestInput)
	}
	if !i.StartsAt.Before(i.EndsAt) {
		return fmt.Errorf("%w: start time must be before end time", ErrInvalidContestInput)
	}
	if !unique(i.Problems) {
		return fmt.Errorf("%w: problem IDs must be unique positive integers", ErrInvalidContestInput)
	}
	if i.ScoringPolicy != "best" && i.ScoringPolicy != "latest" {
		return fmt.Errorf("%w: invalid scoring policy", ErrInvalidContestInput)
	}
	return nil
}
func validateProblems(ctx context.Context, tx pgx.Tx, ids []int64, confidential bool) error {
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM problems WHERE id=ANY($1)`, ids).Scan(&count); err != nil {
		return err
	}
	if count != len(ids) {
		return ErrContestProblemNotFound
	}
	if !confidential {
		return nil
	}
	var reused bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contest_problems cp JOIN contests c ON c.id=cp.contest_id WHERE cp.problem_id=ANY($1) AND now()>=c.ends_at)`, ids).Scan(&reused); err != nil {
		return err
	}
	if reused {
		return ErrPublishedProblemReused
	}
	return nil
}
func replaceProblems(ctx context.Context, tx pgx.Tx, contestID int64, ids []int64) error {
	if _, err := tx.Exec(ctx, `DELETE FROM contest_problems WHERE contest_id=$1`, contestID); err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.Exec(ctx, `INSERT INTO contest_problems(contest_id,problem_id,ordinal) VALUES($1,$2,$3)`, contestID, id, i+1); err != nil {
			return err
		}
	}
	return nil
}
func unique(values []int64) bool {
	seen := map[int64]struct{}{}
	for _, v := range values {
		if v <= 0 {
			return false
		}
		if _, ok := seen[v]; ok {
			return false
		}
		seen[v] = struct{}{}
	}
	return true
}
func problemIDs(items []ProblemSummary) []int64 {
	ids := make([]int64, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}
func summariesForIDs(ids []int64) []ProblemSummary {
	items := make([]ProblemSummary, len(ids))
	for i, id := range ids {
		items[i].ID = id
	}
	return items
}

var ErrContestFixedFields = errors.New("contest fields are fixed")

func validateUpdateWindow(before, after AdminContest, now time.Time) error {
	if now.Before(before.EndsAt) {
		return nil
	}
	if !before.StartsAt.Equal(after.StartsAt) || !before.EndsAt.Equal(after.EndsAt) || before.ScoringPolicy != after.ScoringPolicy || !slices.Equal(problemIDs(before.Problems), problemIDs(after.Problems)) {
		return ErrContestFixedFields
	}
	return nil
}
