package problems

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/klauspost/compress/zstd"
	"github.com/ztsubaki/soj/services/api/internal/modules/artifacts"
	"github.com/ztsubaki/soj/services/api/internal/modules/auth"
	"github.com/ztsubaki/soj/services/api/internal/validation"
)

var ErrNotFound = errors.New("problem not found")

type Service struct {
	pool                *pgxpool.Pool
	artifactRoot        string
	maxJudgeProgramSize int64
}

const DefaultMaxJudgeProgramSize = 10 << 30
const maxPackageFiles = 10000
const maxPackageUnpackedSize = 10 << 30

func NewService(pool *pgxpool.Pool, artifactRoot string, maxJudgeProgramSize int64) *Service {
	if pool == nil {
		return nil
	}
	if maxJudgeProgramSize <= 0 {
		maxJudgeProgramSize = DefaultMaxJudgeProgramSize
	}
	return &Service{pool: pool, artifactRoot: filepath.Clean(artifactRoot), maxJudgeProgramSize: maxJudgeProgramSize}
}

func (s *Service) MaxJudgeProgramSize() int64 { return s.maxJudgeProgramSize }

// AdminSetPackageStaged validates and consumes a package streamed by the
// internal HTTP data plane. The temporary file and final Artifact object live
// in the same backend directory, making the handoff an atomic rename.
func (s *Service) AdminSetPackageStaged(ctx context.Context, actorID int64, id int64, artifactRoot, stagedPath, sha string, size int64) (AdminProblem, error) {
	artifactDir := filepath.Clean(artifactRoot)
	if stagedPath == "" || filepath.Dir(stagedPath) != artifactDir || size <= 0 || size > s.maxJudgeProgramSize || !validation.SHA256(sha) {
		return AdminProblem{}, errors.New("invalid staged package")
	}
	src, err := os.Open(stagedPath)
	if err != nil {
		return AdminProblem{}, err
	}
	info, statErr := src.Stat()
	if statErr != nil {
		src.Close()
		return AdminProblem{}, statErr
	}
	if info.Size() != size {
		src.Close()
		return AdminProblem{}, errors.New("staged package size changed")
	}
	hash := sha256.New()
	manifest, readErr := readPackageManifestReader(io.TeeReader(src, hash))
	closeErr := src.Close()
	if readErr != nil || closeErr != nil {
		if readErr != nil {
			return AdminProblem{}, readErr
		}
		return AdminProblem{}, closeErr
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); actual != sha {
		return AdminProblem{}, errors.New("staged package digest changed")
	}
	if manifest.ProblemID != id {
		return AdminProblem{}, errors.New("manifest problemId does not match problem")
	}
	var artifactID int64
	if err = s.pool.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('artifacts','id'))`).Scan(&artifactID); err != nil {
		return AdminProblem{}, err
	}
	key := strconv.FormatInt(artifactID, 10)
	path := filepath.Join(artifactDir, key)
	if err = os.Rename(stagedPath, path); err != nil {
		return AdminProblem{}, err
	}
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(path)
		}
	}()
	raw, err := json.Marshal(manifest)
	if err != nil {
		return AdminProblem{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AdminProblem{}, err
	}
	defer tx.Rollback(ctx)
	if err = auth.RevalidateMutation(ctx, tx, actorID, true); err != nil {
		return AdminProblem{}, err
	}
	before, err := adminGetForUpdate(ctx, tx, id)
	if err != nil {
		return AdminProblem{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO artifacts(id,object_key,kind,state,size_bytes,sha256,content_type,available_at) OVERRIDING SYSTEM VALUE VALUES($1,$2,'problem_package','available',$3,$4,'application/zstd',now())`, artifactID, key, size, sha); err != nil {
		return AdminProblem{}, err
	}
	if err = artifacts.Retain(ctx, tx, artifactID); err != nil {
		return AdminProblem{}, err
	}
	r, err := tx.Exec(ctx, `UPDATE problems SET package_artifact_id=$2,package_manifest=$3,updated_at=now() WHERE id=$1`, id, artifactID, raw)
	if err != nil || r.RowsAffected() != 1 {
		if err != nil {
			return AdminProblem{}, err
		}
		return AdminProblem{}, ErrNotFound
	}
	removed, err := artifacts.Release(ctx, tx, packageID(before))
	if err != nil {
		return AdminProblem{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return AdminProblem{}, err
	}
	artifacts.RemoveObjects(artifactDir, removed)
	remove = false
	return s.AdminGet(ctx, id)
}

func packageID(problem AdminProblem) int64 {
	if problem.Package == nil {
		return 0
	}
	return problem.Package.ArtifactID
}

// CanAdminSetPackage is the data-plane preflight used before the upload
// handler reads package bytes. The mutation rechecks existence while holding
// its transaction lock, so this only narrows the unauthorised-read window.
func (s *Service) CanAdminSetPackage(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrNotFound
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM problems WHERE id=$1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

type packageManifest struct {
	SchemaVersion int   `json:"schemaVersion"`
	ProblemID     int64 `json:"problemId"`
}

// readPackageManifestReader validates the package manifest and fixed batch script.
func readPackageManifestReader(src io.Reader) (packageManifest, error) {
	zr, err := zstd.NewReader(src)
	if err != nil {
		return packageManifest{}, errors.New("package must be tar.zst")
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	var manifest packageManifest
	var found, batch bool
	var total int64
	files := 0
	names := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return manifest, err
		}
		files++
		total += h.Size
		if files > maxPackageFiles || total > maxPackageUnpackedSize || strings.HasPrefix(h.Name, "/") || filepath.Clean(h.Name) == ".." || strings.HasPrefix(filepath.Clean(h.Name), "../") || h.Typeflag == tar.TypeSymlink || h.Typeflag == tar.TypeLink {
			return manifest, errors.New("unsafe package archive")
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if names[h.Name] {
			return manifest, errors.New("duplicate package file")
		}
		names[h.Name] = true
		if h.Name == "judge.sbatch" {
			batch = true
		}
		if h.Name == "manifest.json" {
			d := json.NewDecoder(io.LimitReader(tr, 1<<20))
			d.DisallowUnknownFields()
			if err := d.Decode(&manifest); err != nil {
				return manifest, errors.New("invalid manifest")
			}
			if d.Decode(new(any)) != io.EOF {
				return manifest, errors.New("invalid manifest trailing data")
			}
			found = true
		}

	}
	if !found || manifest.SchemaVersion != 1 || manifest.ProblemID <= 0 || !batch {
		return manifest, errors.New("invalid manifest")
	}
	return manifest, nil
}
