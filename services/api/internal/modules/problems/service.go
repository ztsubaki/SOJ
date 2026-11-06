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
	"path"
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
const maxPackageManifestSize = 1 << 20
const maxPackageArchiveSize = maxPackageUnpackedSize + (maxPackageFiles+1)*1024
const maxPackagePathDepth = 64

// Reserve package/, submission/, source, output/, tmp/, run-output/ and job.json.
const maxPackageMaterializedEntries = 20000 - 7

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

// readPackageManifestReader validates the manifest, selected root script and all
// archive entries before a package can become an immutable Artifact.
func readPackageManifestReader(src io.Reader) (validation.PackageManifest, error) {
	var manifest validation.PackageManifest
	zr, err := zstd.NewReader(src)
	if err != nil {
		return manifest, errors.New("package must be tar.zst")
	}
	defer zr.Close()
	// Bound decompressed metadata and padding as well as declared file contents.
	archive := &io.LimitedReader{R: zr, N: maxPackageArchiveSize + 1}
	tr := tar.NewReader(archive)
	var found bool
	var total int64
	files := 0
	materialized := 0
	names := map[string]byte{}
	directories := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return manifest, err
		}
		files++
		if files > maxPackageFiles || h.Size < 0 || h.Size > maxPackageUnpackedSize-total {
			return manifest, errors.New("unsafe package archive size")
		}
		total += h.Size
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA && h.Typeflag != tar.TypeDir {
			return manifest, errors.New("unsafe package archive entry type")
		}
		if h.Typeflag == tar.TypeDir && h.Size != 0 {
			return manifest, errors.New("unsafe package directory size")
		}
		if h.Name == "" || strings.HasPrefix(h.Name, "/") || strings.ContainsAny(h.Name, "\\\x00") {
			return manifest, errors.New("unsafe package archive path")
		}
		for i, component := range strings.Split(h.Name, "/") {
			if component == ".." || (i == 0 && (component == "." || strings.Contains(component, ":"))) {
				return manifest, errors.New("unsafe package archive path")
			}
		}
		name := path.Clean(h.Name)
		if strings.Count(name, "/")+1 >= maxPackagePathDepth {
			return manifest, errors.New("package path exceeds workspace depth limit")
		}
		if h.Typeflag != tar.TypeDir && (strings.HasSuffix(h.Name, "/") || strings.HasSuffix(h.Name, "/.")) {
			return manifest, errors.New("unsafe package archive path")
		}
		if _, exists := names[name]; exists {
			return manifest, errors.New("duplicate package entry")
		}
		if !directories[name] {
			materialized++
		}
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			kind, explicit := names[parent]
			if explicit && kind != tar.TypeDir {
				return manifest, errors.New("package file conflicts with directory")
			}
			if !explicit && !directories[parent] {
				materialized++
			}
			directories[parent] = true
		}
		if materialized > maxPackageMaterializedEntries {
			return manifest, errors.New("package entries exceed initial workspace limit")
		}
		if h.Typeflag != tar.TypeDir && directories[name] {
			return manifest, errors.New("package file conflicts with directory")
		}
		names[name] = h.Typeflag
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if name == "manifest.json" {
			if h.Size > maxPackageManifestSize {
				return manifest, errors.New("manifest exceeds size limit")
			}
			raw, err := io.ReadAll(tr)
			if err != nil {
				return manifest, err
			}
			manifest, err = validation.ParsePackageManifest(raw)
			if err != nil {
				return manifest, err
			}
			found = true
		}
	}
	// tar stops at its end marker. Consume the full zstd stream to validate its
	// checksum and ensure the upload hash covers every byte, allowing only tar
	// zero padding after the marker rather than an unchecked second archive.
	var padding [32 << 10]byte
	for {
		n, err := archive.Read(padding[:])
		for _, value := range padding[:n] {
			if value != 0 {
				return manifest, errors.New("unexpected data after package archive")
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return manifest, err
		}
	}
	if archive.N == 0 {
		return manifest, errors.New("unsafe package archive size")
	}
	script := "judge.sbatch"
	if manifest.Executor() == "docker" {
		script = "judge.sh"
	}
	kind, exists := names[script]
	if !found || !exists || (kind != tar.TypeReg && kind != tar.TypeRegA) {
		return manifest, errors.New("package requires manifest.json and a regular root " + script)
	}
	return manifest, nil
}
