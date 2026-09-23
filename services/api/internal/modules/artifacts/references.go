package artifacts

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
)

// Retain records a durable reference held by an application row. Every
// reference is counted independently: for example, a source artifact belongs
// to both its submission and each job created from it.
func Retain(ctx context.Context, tx pgx.Tx, id int64) error {
	if id <= 0 {
		return errors.New("artifact id is required")
	}
	result, err := tx.Exec(ctx, `UPDATE artifacts SET ref_count=ref_count+1 WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("artifact not found")
	}
	return nil
}

// Release removes one durable reference for every supplied artifact ID. When
// the last reference goes away, it also removes the Artifact metadata and
// returns the object key for deletion after the surrounding transaction commits.
func Release(ctx context.Context, tx pgx.Tx, ids ...int64) ([]string, error) {
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		var count int64
		var key string
		err := tx.QueryRow(ctx, `UPDATE artifacts
SET ref_count=ref_count-1
WHERE id=$1 AND ref_count>0
RETURNING ref_count,object_key`, id).Scan(&count, &key)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, errors.New("artifact reference count is invalid")
			}
			return nil, err
		}
		if count != 0 {
			continue
		}
		var deletedKey string
		if err = tx.QueryRow(ctx, `DELETE FROM artifacts WHERE id=$1 AND ref_count=0 RETURNING object_key`, id).Scan(&deletedKey); err != nil {
			return nil, err
		}
		keys = append(keys, deletedKey)
	}
	return keys, nil
}

// RemoveObjects performs the non-transactional half of a successful release.
// Object keys are generated internally; Base keeps a corrupt database value
// from escaping the configured artifact directory.
func RemoveObjects(root string, keys []string) {
	root = filepath.Clean(root)
	for _, key := range keys {
		name := filepath.Base(key)
		if key == "" || name == "." || name == string(filepath.Separator) {
			continue
		}
		_ = os.Remove(filepath.Join(root, name))
	}
}
