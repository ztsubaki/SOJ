package problems

import (
	"archive/tar"
	"bytes"
	"github.com/klauspost/compress/zstd"
	"testing"
)

func TestMinimalPackageManifest(t *testing.T) {
	for _, tc := range []struct {
		name, manifest, script string
		wantError              bool
	}{
		{"minimal", `{"schemaVersion":1,"problemId":1}`, "judge.sbatch", false},
		{"legacy config", `{"schemaVersion":1,"problemId":"p","entrypoint":"judge.sbatch"}`, "judge.sbatch", true},
		{"missing script", `{"schemaVersion":1,"problemId":"p"}`, "other.sh", true},
		{"trailing JSON", `{"schemaVersion":1,"problemId":"p"}{}`, "judge.sbatch", true},
		{"invalid ID", `{"schemaVersion":1,"problemId":"../p"}`, "judge.sbatch", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			zw, err := zstd.NewWriter(&buf)
			if err != nil {
				t.Fatal(err)
			}
			tw := tar.NewWriter(zw)
			for _, f := range []struct{ name, body string }{{"manifest.json", tc.manifest}, {tc.script, "#!/bin/sh\n"}} {
				if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0644, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
					t.Fatal(err)
				}
				if _, err := tw.Write([]byte(f.body)); err != nil {
					t.Fatal(err)
				}
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			_, err = readPackageManifestReader(&buf)
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v, wantError=%v", err, tc.wantError)
			}
		})
	}
}
