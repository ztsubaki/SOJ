package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/ztsubaki/soj/services/api/internal/modules/artifacts"
)

var (
	ErrNotFound     = errors.New("user not found")
	ErrNotPermitted = errors.New("user operation is not permitted")
)

type AdminUserPatch struct {
	Email  *string
	Status *string
}

func (s *Service) AdminListUsers(ctx context.Context, query, userStatus, role string, page, size int) ([]User, int, error) {
	where := []string{"1=1"}
	args := []any{}
	if query != "" {
		args = append(args, "%"+query+"%")
		where = append(where, fmt.Sprintf("(username ILIKE $%d OR email ILIKE $%d)", len(args), len(args)))
	}
	if userStatus != "" {
		args = append(args, userStatus)
		where = append(where, fmt.Sprintf("status=$%d", len(args)))
	}
	if role != "" {
		args = append(args, role)
		where = append(where, fmt.Sprintf("role=$%d", len(args)))
	}
	clause := strings.Join(where, " AND ")
	var total int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE "+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, size, (page-1)*size)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT id,username,email,role,status,created_at FROM users WHERE %s ORDER BY created_at DESC,id ASC LIMIT $%d OFFSET $%d`, clause, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.ID, &user.Username, &user.Email, &user.Role, &user.Status, &user.CreatedAt); err != nil {
			return nil, 0, err
		}
		users = append(users, user)
	}
	return users, total, rows.Err()
}

func (s *Service) AdminCreateUser(ctx context.Context, actor User, username, email string) (User, error) {
	username, email = normalizeUsername(username), strings.ToLower(strings.TrimSpace(email))
	if !validUsername(username) || !validEmail(email) {
		return User{}, ErrInvalidRegistration
	}
	password, err := randomPassword()
	if err != nil {
		return User{}, err
	}
	hash, err := hashPassword(ctx, password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)
	u := User{Username: username, Email: email, Role: RoleUser, Status: "active"}
	err = tx.QueryRow(ctx, `INSERT INTO users(username,email,password_hash) VALUES($1,$2,$3) RETURNING id,created_at`, u.Username, u.Email, hash).Scan(&u.ID, &u.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return User{}, ErrUsernameTaken
		}
		return User{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return u, nil
}

// AdminResetUserPassword replaces a target user's password with a new cryptographically
// random value. The caller is responsible for returning the plaintext exactly once;
// it is never persisted.
func (s *Service) AdminResetUserPassword(ctx context.Context, actor User, id int64) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	target, err := getUserForUpdate(ctx, tx, id)
	if err != nil {
		return "", err
	}
	if err = allowAdminSecurityMutation(target); err != nil {
		return "", err
	}
	password, err := randomPassword()
	if err != nil {
		return "", err
	}
	hash, err := hashPassword(ctx, password)
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET password_hash=$2,updated_at=now() WHERE id=$1`, id, hash); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE user_id=$1`, id); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return password, nil
}

func (s *Service) AdminUpdateUser(ctx context.Context, actor User, id int64, patch AdminUserPatch) (User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)
	before, err := getUserForUpdate(ctx, tx, id)
	if err != nil {
		return User{}, err
	}
	if actor.ID == id && patch.Status != nil && *patch.Status == "disabled" {
		return User{}, ErrNotPermitted
	}
	if patch.Status != nil {
		if err = allowAdminSecurityMutation(before); err != nil {
			return User{}, err
		}
	}
	after := before
	if patch.Email != nil {
		after.Email = strings.ToLower(strings.TrimSpace(*patch.Email))
		if !validEmail(after.Email) {
			return User{}, ErrInvalidRegistration
		}
	}
	if patch.Status != nil {
		if *patch.Status != "active" && *patch.Status != "disabled" {
			return User{}, ErrInvalidRegistration
		}
		after.Status = *patch.Status
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET email=$2,status=$3,updated_at=now() WHERE id=$1`, id, after.Email, after.Status); err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return User{}, ErrUsernameTaken
		}
		return User{}, err
	}
	if after.Status == "disabled" {
		if _, err = tx.Exec(ctx, `UPDATE sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE user_id=$1`, id); err != nil {
			return User{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return s.GetUser(ctx, id)
}

func allowAdminSecurityMutation(target User) error {
	if target.Role == RoleAdmin {
		return ErrNotPermitted
	}
	return nil
}

// AdminDeleteUser permanently removes the account, its sessions, contest
// registrations, and submissions. Creator references are set NULL by schema.
func (s *Service) AdminDeleteUser(ctx context.Context, actor User, id int64) error {
	if actor.ID == id {
		return ErrNotPermitted
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = RevalidateMutation(ctx, tx, actor.ID, true); err != nil {
		return err
	}
	target, err := getUserForUpdate(ctx, tx, id)
	if err != nil {
		return err
	}
	if err = allowAdminSecurityMutation(target); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE submissions SET current_job_id=NULL WHERE user_id=$1`, id); err != nil {
		return err
	}
	refs, err := deleteJobs(ctx, tx, `DELETE FROM judge_jobs WHERE submission_id IN (SELECT id FROM submissions WHERE user_id=$1)`, id)
	if err != nil {
		return err
	}
	moreRefs, err := deleteSubmissions(ctx, tx, `DELETE FROM submissions WHERE user_id=$1`, id)
	if err != nil {
		return err
	}
	refs = append(refs, moreRefs...)
	if _, err = tx.Exec(ctx, `DELETE FROM contest_registrations WHERE user_id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM users WHERE id=$1`, id); err != nil {
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

func getUserForUpdate(ctx context.Context, tx pgx.Tx, id int64) (User, error) {
	var user User
	err := tx.QueryRow(ctx, `SELECT id,username,email,role,status,created_at FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&user.ID, &user.Username, &user.Email, &user.Role, &user.Status, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func randomPassword() (string, error) {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
