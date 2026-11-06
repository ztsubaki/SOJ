package problems

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

const slurmManifest = `{"schemaVersion":1,"problemId":1}`

func TestPackageManifestExecutor(t *testing.T) {
	docker := `{"schemaVersion":1,"problemId":1,"docker":{"image":"sha256:` + strings.Repeat("a", 64) + `","cpus":1,"memoryMb":128,"timeLimitSeconds":10}}`
	for _, tc := range []struct {
		name, manifest, script, executor string
		wantError                        bool
	}{
		{"slurm", slurmManifest, "judge.sbatch", "slurm", false},
		{"docker", docker, "judge.sh", "docker", false},
		{"docker repository", strings.Replace(docker, "sha256:", "registry.example/judge@sha256:", 1), "judge.sh", "docker", false},
		{"docker requires judge.sh", docker, "judge.sbatch", "", true},
		{"slurm requires judge.sbatch", slurmManifest, "judge.sh", "", true},
		{"nested Docker script", docker, "scripts/judge.sh", "", true},
		{"nested Slurm script", slurmManifest, "scripts/judge.sbatch", "", true},
		{"legacy config", `{"schemaVersion":1,"problemId":1,"entrypoint":"judge.sbatch"}`, "judge.sbatch", "", true},
		{"missing script", slurmManifest, "other.sh", "", true},
		{"trailing JSON", slurmManifest + `{}`, "judge.sbatch", "", true},
		{"null Docker", `{"schemaVersion":1,"problemId":1,"docker":null}`, "judge.sbatch", "", true},
		{"unknown Docker field", strings.Replace(docker, `"cpus":1`, `"cpus":1,"network":"host"`, 1), "judge.sh", "", true},
		{"mutable image", strings.Replace(docker, "sha256:"+strings.Repeat("a", 64), "judge:latest", 1), "judge.sh", "", true},
		{"resource overflow", strings.Replace(docker, `"cpus":1`, `"cpus":257`, 1), "judge.sh", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := manifestArchive(t, []manifestEntry{regularManifestEntry("manifest.json", tc.manifest), regularManifestEntry(tc.script, "#!/bin/sh\n")})
			manifest, err := readPackageManifestReader(bytes.NewReader(archive))
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v, wantError=%v", err, tc.wantError)
			}
			if !tc.wantError && (manifest.ProblemID != 1 || manifest.Executor() != tc.executor) {
				t.Fatalf("unexpected manifest: %#v, executor=%s", manifest, manifest.Executor())
			}
		})
	}
}

func TestPackageArchiveAcceptsSafeDirectories(t *testing.T) {
	entries := []manifestEntry{
		{header: tar.Header{Name: "data/", Typeflag: tar.TypeDir}},
		regularManifestEntry("data/input.txt", "input\n"),
		regularManifestEntry("judge.sbatch", "#!/bin/sh\n"),
		regularManifestEntry("manifest.json", slurmManifest),
	}
	if _, err := readPackageManifestReader(bytes.NewReader(manifestArchive(t, entries))); err != nil {
		t.Fatalf("safe directory entries or script preceding manifest rejected: %v", err)
	}
}

func TestPackageArchiveRejectsLeadingDotEntries(t *testing.T) {
	for _, entries := range [][]manifestEntry{
		{regularManifestEntry("./manifest.json", slurmManifest), regularManifestEntry("judge.sbatch", "#!/bin/sh\n")},
		{regularManifestEntry("manifest.json", slurmManifest), regularManifestEntry("./judge.sbatch", "#!/bin/sh\n")},
		{regularManifestEntry("manifest.json", slurmManifest), regularManifestEntry("judge.sbatch", "#!/bin/sh\n"), {header: tar.Header{Name: "./", Typeflag: tar.TypeDir}}},
	} {
		if _, err := readPackageManifestReader(bytes.NewReader(manifestArchive(t, entries))); err == nil {
			t.Fatal("accepted a leading-dot path that the Agent cannot unpack")
		}
	}
}

func TestPackageArchiveWorkspaceShapeBoundaries(t *testing.T) {
	base := []manifestEntry{regularManifestEntry("manifest.json", slurmManifest), regularManifestEntry("judge.sbatch", "#!/bin/sh\n")}
	for _, depth := range []int{63, 64} {
		entries := append(append([]manifestEntry(nil), base...), regularManifestEntry(strings.Repeat("d/", depth-1)+"leaf", ""))
		_, err := readPackageManifestReader(bytes.NewReader(manifestArchive(t, entries)))
		if (err != nil) != (depth == 64) {
			t.Fatalf("package path depth %d: %v", depth, err)
		}
	}
	entries := append([]manifestEntry(nil), base...)
	for i := range 317 {
		entries = append(entries, regularManifestEntry(fmt.Sprintf("branch-%d/", i)+strings.Repeat("d/", 61)+"leaf", ""))
	}
	// 19,993 materialized package entries leave seven fixed workspace entries.
	for i := range 20 {
		entries = append(entries, regularManifestEntry(fmt.Sprintf("extra-%d", i), ""))
	}
	if _, err := readPackageManifestReader(bytes.NewReader(manifestArchive(t, entries))); err != nil {
		t.Fatalf("package at initial workspace entry limit rejected: %v", err)
	}
	entries = append(entries, regularManifestEntry("overflow", ""))
	if _, err := readPackageManifestReader(bytes.NewReader(manifestArchive(t, entries))); err == nil {
		t.Fatal("accepted a package whose implicit directories overflow the initial workspace")
	}
}

func TestPackageArchiveRejectsUnsafeEntries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []manifestEntry
	}{
		{"absolute", []manifestEntry{regularManifestEntry("/escape", "")}},
		{"parent traversal", []manifestEntry{regularManifestEntry("../escape", "")}},
		{"internal traversal", []manifestEntry{regularManifestEntry("dir/../escape", "")}},
		{"backslash", []manifestEntry{regularManifestEntry(`dir\escape`, "")}},
		{"Windows absolute", []manifestEntry{regularManifestEntry("C:/escape", "")}},
		{"regular file directory suffix", []manifestEntry{regularManifestEntry("data/.", "")}},
		{"symlink", []manifestEntry{{header: tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/outside"}}}},
		{"hardlink", []manifestEntry{{header: tar.Header{Name: "link", Typeflag: tar.TypeLink, Linkname: "judge.sbatch"}}}},
		{"fifo", []manifestEntry{{header: tar.Header{Name: "pipe", Typeflag: tar.TypeFifo}}}},
		{"character device", []manifestEntry{{header: tar.Header{Name: "device", Typeflag: tar.TypeChar}}}},
		{"block device", []manifestEntry{{header: tar.Header{Name: "device", Typeflag: tar.TypeBlock}}}},
		{"duplicate manifest", []manifestEntry{regularManifestEntry("manifest.json", slurmManifest)}},
		{"duplicate script", []manifestEntry{regularManifestEntry("judge.sbatch", "")}},
		{"aliased duplicate", []manifestEntry{regularManifestEntry("./judge.sbatch", "")}},
		{"duplicate directory", []manifestEntry{{header: tar.Header{Name: "data/", Typeflag: tar.TypeDir}}, {header: tar.Header{Name: "data", Typeflag: tar.TypeDir}}}},
		{"file before directory child", []manifestEntry{regularManifestEntry("data", ""), regularManifestEntry("data/file", "")}},
		{"file after directory child", []manifestEntry{regularManifestEntry("data/file", ""), regularManifestEntry("data", "")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := []manifestEntry{regularManifestEntry("manifest.json", slurmManifest), regularManifestEntry("judge.sbatch", "#!/bin/sh\n")}
			entries = append(entries, tc.entries...)
			if _, err := readPackageManifestReader(bytes.NewReader(manifestArchive(t, entries))); err == nil {
				t.Fatal("accepted unsafe package entry")
			}
		})
	}
}

func TestPackageArchiveRequiresRegularRootEntries(t *testing.T) {
	docker := `{"schemaVersion":1,"problemId":1,"docker":{"image":"sha256:` + strings.Repeat("a", 64) + `","cpus":1,"memoryMb":128,"timeLimitSeconds":10}}`
	for _, tc := range []struct{ manifest, script string }{{slurmManifest, "judge.sbatch"}, {docker, "judge.sh"}} {
		for _, kind := range []byte{tar.TypeDir, tar.TypeSymlink, tar.TypeLink, tar.TypeFifo} {
			t.Run(fmt.Sprintf("%s/%d", tc.script, kind), func(t *testing.T) {
				entry := manifestEntry{header: tar.Header{Name: tc.script, Typeflag: kind}}
				if kind == tar.TypeSymlink || kind == tar.TypeLink {
					entry.header.Linkname = "actual-script"
				}
				entries := []manifestEntry{regularManifestEntry("manifest.json", tc.manifest), entry, regularManifestEntry("actual-script", "#!/bin/sh\n")}
				if _, err := readPackageManifestReader(bytes.NewReader(manifestArchive(t, entries))); err == nil {
					t.Fatal("accepted a non-regular required script")
				}
			})
		}
	}
	t.Run("manifest directory", func(t *testing.T) {
		entries := []manifestEntry{{header: tar.Header{Name: "manifest.json", Typeflag: tar.TypeDir}}, regularManifestEntry("judge.sbatch", "")}
		if _, err := readPackageManifestReader(bytes.NewReader(manifestArchive(t, entries))); err == nil {
			t.Fatal("accepted a non-regular manifest")
		}
	})
}

func TestPackageManifestSizeBoundary(t *testing.T) {
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprint(extra), func(t *testing.T) {
			raw := slurmManifest + strings.Repeat(" ", maxPackageManifestSize-len(slurmManifest)+extra)
			archive := manifestArchive(t, []manifestEntry{regularManifestEntry("manifest.json", raw), regularManifestEntry("judge.sbatch", "")})
			_, err := readPackageManifestReader(bytes.NewReader(archive))
			if (err != nil) != (extra != 0) {
				t.Fatalf("size=%d error=%v", len(raw), err)
			}
		})
	}
}

func TestPackageArchiveFileCountBoundary(t *testing.T) {
	entries := []manifestEntry{regularManifestEntry("manifest.json", slurmManifest), regularManifestEntry("judge.sbatch", "")}
	for i := len(entries); i < maxPackageFiles; i++ {
		entries = append(entries, regularManifestEntry(fmt.Sprintf("data/%d", i), ""))
	}
	if _, err := readPackageManifestReader(bytes.NewReader(manifestArchive(t, entries))); err != nil {
		t.Fatalf("archive at file count limit rejected: %v", err)
	}
	entries = append(entries, regularManifestEntry("data/overflow", ""))
	if _, err := readPackageManifestReader(bytes.NewReader(manifestArchive(t, entries))); err == nil {
		t.Fatal("accepted too many package entries")
	}
}

func TestPackageArchiveRejectsTruncatedFileAfterRequiredEntries(t *testing.T) {
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, entry := range []manifestEntry{regularManifestEntry("manifest.json", slurmManifest), regularManifestEntry("judge.sbatch", "#!/bin/sh\n")} {
		header := entry.header
		if err := tw.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: "truncated-file", Mode: 0644, Typeflag: tar.TypeReg, Size: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := readPackageManifestReader(bytes.NewReader(compressManifestArchive(t, raw.Bytes()))); err == nil {
		t.Fatal("accepted truncated file data after the manifest and script")
	}
}

func TestPackageArchiveConsumesCompressedStream(t *testing.T) {
	entries := []manifestEntry{regularManifestEntry("manifest.json", slurmManifest), regularManifestEntry("judge.sbatch", "")}
	archive := manifestArchive(t, entries)
	corrupt := append([]byte(nil), archive...)
	corrupt[len(corrupt)-1] ^= 0xff
	for name, raw := range map[string][]byte{
		"not zstd":         []byte("not an archive"),
		"truncated zstd":   archive[:len(archive)-1],
		"corrupt checksum": corrupt,
		"trailing garbage": append(append([]byte(nil), archive...), []byte("garbage")...),
		"second archive":   append(append([]byte(nil), archive...), archive...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := readPackageManifestReader(bytes.NewReader(raw)); err == nil {
				t.Fatal("accepted invalid compressed stream or unchecked archive data")
			}
		})
	}
	var consumed bytes.Buffer
	if _, err := readPackageManifestReader(io.TeeReader(bytes.NewReader(archive), &consumed)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(consumed.Bytes(), archive) {
		t.Fatal("package validation did not consume all compressed bytes for hashing")
	}
}

func TestStagedPackageRetainsInputChecks(t *testing.T) {
	payload := manifestArchive(t, []manifestEntry{regularManifestEntry("manifest.json", slurmManifest), regularManifestEntry("judge.sbatch", "")})
	dir := t.TempDir()
	staged := filepath.Join(dir, ".upload-package")
	if err := os.WriteFile(staged, payload, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	for _, tc := range []struct {
		name, sha string
		id, size  int64
		maximum   int64
	}{
		{"digest changed", strings.Repeat("0", 64), 1, int64(len(payload)), DefaultMaxJudgeProgramSize},
		{"size changed", digest, 1, int64(len(payload)) + 1, DefaultMaxJudgeProgramSize},
		{"problem mismatch", digest, 2, int64(len(payload)), DefaultMaxJudgeProgramSize},
		{"compressed size limit", digest, 1, int64(len(payload)), int64(len(payload)) - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &Service{maxJudgeProgramSize: tc.maximum}
			if _, err := service.AdminSetPackageStaged(context.Background(), 1, tc.id, dir, staged, tc.sha, tc.size); err == nil {
				t.Fatal("accepted changed or oversized staged package")
			}
		})
	}
}

type manifestEntry struct {
	header tar.Header
	body   string
}

func regularManifestEntry(name, body string) manifestEntry {
	return manifestEntry{header: tar.Header{Name: name, Mode: 0644, Typeflag: tar.TypeReg, Size: int64(len(body))}, body: body}
}

func manifestArchive(t *testing.T, entries []manifestEntry) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, entry := range entries {
		header := entry.header
		if err := tw.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return compressManifestArchive(t, raw.Bytes())
}

func compressManifestArchive(t *testing.T, raw []byte) []byte {
	t.Helper()
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	return encoder.EncodeAll(raw, nil)
}
