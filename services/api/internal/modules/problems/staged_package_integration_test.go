package problems

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/klauspost/compress/zstd"
)

func TestAdminSetPackageStagedIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `INSERT INTO problems(id,title,content,difficulty,enabled,candidate_partitions,created_by) OVERRIDING SYSTEM VALUE VALUES (9001,'Package upload problem','','Easy',true,ARRAY['package-upload'],1) ON CONFLICT (id) DO UPDATE SET enabled=true,candidate_partitions=EXCLUDED.candidate_partitions`); err != nil {
		t.Fatal(err)
	}
	artifactDir := filepath.Join(t.TempDir(), "custom-store")
	if err := os.MkdirAll(artifactDir, 0700); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, artifactDir, DefaultMaxJudgeProgramSize)
	payload := packageArchive(t, 9001)
	staged := writeStagedPackage(t, artifactDir, ".upload-package-ok", payload)
	updated, err := service.AdminSetPackageStaged(ctx, 1, 9001, artifactDir, staged.path, staged.sha, staged.size)
	if err != nil || updated.Package == nil || updated.Package.SHA256 != staged.sha || updated.Package.SizeBytes != uint64(staged.size) {
		t.Fatalf("AdminSetPackageStaged() = (%#v, %v)", updated, err)
	}
	if _, err := os.Stat(staged.path); !os.IsNotExist(err) {
		t.Fatalf("staged package remains after commit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(artifactDir, strconv.FormatInt(updated.Package.ArtifactID, 10))); err != nil {
		t.Fatalf("durable package object missing: %v", err)
	}

	before, err := os.ReadDir(artifactDir)
	if err != nil {
		t.Fatal(err)
	}
	failed := writeStagedPackage(t, artifactDir, ".upload-package-fail", payload)
	if _, err := service.AdminSetPackageStaged(ctx, 0, 9001, artifactDir, failed.path, failed.sha, failed.size); err == nil {
		t.Fatal("AdminSetPackageStaged() succeeded with a missing administrator")
	}
	if _, err := os.Stat(failed.path); !os.IsNotExist(err) {
		t.Fatalf("failed staged package remains: %v", err)
	}
	after, err := os.ReadDir(artifactDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("artifact directory after failed bind has %d entries, want %d", len(after), len(before))
	}
}

type stagedPackage struct {
	path, sha string
	size      int64
}

func writeStagedPackage(t *testing.T, dir, name string, payload []byte) stagedPackage {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	return stagedPackage{path: path, sha: hex.EncodeToString(sum[:]), size: int64(len(payload))}
}

func packageArchive(t *testing.T, problemID int64) []byte {
	t.Helper()
	var compressed bytes.Buffer
	zw, err := zstd.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	manifest := []byte(fmt.Sprintf(`{"schemaVersion":1,"problemId":%d}`, problemID))
	for _, file := range []struct {
		name string
		body []byte
		mode int64
	}{{"manifest.json", manifest, 0644}, {"judge.sbatch", []byte("#!/bin/sh\nexit 0\n"), 0755}} {
		if err := tw.WriteHeader(&tar.Header{Name: file.name, Mode: file.mode, Size: int64(len(file.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(file.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}
