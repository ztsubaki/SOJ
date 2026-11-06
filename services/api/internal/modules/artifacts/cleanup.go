package artifacts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RunCleanup removes expired, unbound Agent uploads.  It is separate
// from the database schema lifecycle: deployments still apply schema SQL
// manually, while this loop only reclaims expired pending artifacts and abandoned temporary uploads.
func RunCleanup(ctx context.Context, pool *pgxpool.Pool, artifactRoot string, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	cleanupExpiredArtifacts(ctx, pool, artifactRoot)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanupExpiredArtifacts(ctx, pool, artifactRoot)
		}
	}
}

func cleanupExpiredArtifacts(ctx context.Context, pool *pgxpool.Pool, artifactRoot string) {
	cleanupTemporaryObjects(artifactRoot, time.Now().Add(-24*time.Hour).Unix())
	rows, err := pool.Query(ctx, `DELETE FROM artifacts WHERE state='pending' AND created_at < now()-interval '1 hour' RETURNING object_key`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if rows.Scan(&key) == nil {
			_ = os.Remove(filepath.Join(artifactRoot, filepath.Base(key)))
		}
	}
}

// Temporary objects are never business references and can be removed after a
// day, independently of the shorter TTL for completed unbound uploads.
func cleanupTemporaryObjects(root string, olderThan int64) {
	dir, err := os.Open(root)
	if err != nil {
		return
	}
	defer dir.Close()
	for {
		entries, readErr := dir.ReadDir(256)
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".upload-") {
				info, err := entry.Info()
				if err == nil && info.ModTime().Unix() < olderThan {
					_ = os.Remove(filepath.Join(root, entry.Name()))
				}
			}
		}
		if readErr != nil {
			return
		}
	}
}
