package validation

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestParsePackageManifest(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, raw, executor string
	}{
		{"slurm", `{"schemaVersion":1,"problemId":1}`, "slurm"},
		{"maximum problem ID", `{"schemaVersion":1,"problemId":9007199254740991}`, "slurm"},
		{"local image minimum resources", dockerManifestJSON("sha256:"+digest, 1, 16, 1), "docker"},
		{"repository maximum resources", dockerManifestJSON("registry.example:5000/team/judge@sha256:"+digest, 256, 1048576, 86400), "docker"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest, err := ParsePackageManifest([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if manifest.SchemaVersion != 1 || manifest.ProblemID <= 0 || manifest.Executor() != tc.executor {
				t.Fatalf("unexpected manifest: %#v, executor=%q", manifest, manifest.Executor())
			}
			if tc.executor == "docker" && (manifest.Docker == nil || manifest.Docker.Image == "") {
				t.Fatalf("missing Docker configuration: %#v", manifest)
			}
			raw, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			roundTrip, err := ParsePackageManifest(raw)
			if err != nil || roundTrip.Executor() != tc.executor || roundTrip.ProblemID != manifest.ProblemID {
				t.Fatalf("round trip = (%#v, %v)", roundTrip, err)
			}
			if tc.executor == "slurm" && strings.Contains(string(raw), "docker") {
				t.Fatalf("Slurm manifest acquired a docker field: %s", raw)
			}
		})
	}
}

func TestParsePackageManifestRejectsInvalidJSON(t *testing.T) {
	valid := dockerManifestJSON("sha256:"+strings.Repeat("a", 64), 1, 128, 10)
	cases := map[string]string{
		"empty":                   "",
		"top level null":          "null",
		"top level array":         "[]",
		"missing schema":          `{"problemId":1}`,
		"missing ID":              `{"schemaVersion":1}`,
		"schema null":             `{"schemaVersion":null,"problemId":1}`,
		"schema wrong version":    `{"schemaVersion":2,"problemId":1}`,
		"schema string":           `{"schemaVersion":"1","problemId":1}`,
		"schema decimal":          `{"schemaVersion":1.0,"problemId":1}`,
		"ID null":                 `{"schemaVersion":1,"problemId":null}`,
		"ID zero":                 `{"schemaVersion":1,"problemId":0}`,
		"ID negative":             `{"schemaVersion":1,"problemId":-1}`,
		"ID fractional":           `{"schemaVersion":1,"problemId":1.5}`,
		"ID string":               `{"schemaVersion":1,"problemId":"1"}`,
		"ID overflow":             `{"schemaVersion":1,"problemId":9223372036854775808}`,
		"ID exceeds safe integer": `{"schemaVersion":1,"problemId":9007199254740992}`,
		"unknown top field":       `{"schemaVersion":1,"problemId":1,"executor":"docker"}`,
		"case alias":              `{"SchemaVersion":1,"problemId":1}`,
		"duplicate top field":     `{"schemaVersion":1,"problemId":1,"problemId":2}`,
		"docker null":             `{"schemaVersion":1,"problemId":1,"docker":null}`,
		"docker array":            `{"schemaVersion":1,"problemId":1,"docker":[]}`,
		"docker boolean":          `{"schemaVersion":1,"problemId":1,"docker":true}`,
		"docker empty":            `{"schemaVersion":1,"problemId":1,"docker":{}}`,
		"docker unknown field":    strings.Replace(valid, `"cpus":1`, `"cpus":1,"privileged":false`, 1),
		"docker duplicate field":  strings.Replace(valid, `"cpus":1`, `"cpus":1,"cpus":2`, 1),
		"docker case alias":       strings.Replace(valid, `"cpus":1`, `"CPUs":1`, 1),
		"trailing object":         valid + `{}`,
		"trailing null":           valid + `null`,
		"trailing garbage":        valid + `!`,
		"truncated":               valid[:len(valid)-1],
		"oversized":               valid + strings.Repeat(" ", (1<<20)-len(valid)+1),
	}
	for _, field := range []string{"image", "cpus", "memoryMb", "timeLimitSeconds"} {
		var object map[string]json.RawMessage
		if err := json.Unmarshal([]byte(valid), &object); err != nil {
			t.Fatal(err)
		}
		var docker map[string]json.RawMessage
		if err := json.Unmarshal(object["docker"], &docker); err != nil {
			t.Fatal(err)
		}
		for _, missing := range []bool{false, true} {
			name := field + " null"
			docker[field] = json.RawMessage("null")
			if missing {
				name = field + " missing"
				delete(docker, field)
			}
			rawDocker, err := json.Marshal(docker)
			if err != nil {
				t.Fatal(err)
			}
			object["docker"] = rawDocker
			raw, err := json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
			cases[name] = string(raw)
		}
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if manifest, err := ParsePackageManifest([]byte(raw)); err == nil {
				t.Fatalf("accepted invalid manifest: %#v", manifest)
			}
		})
	}
}

func TestParsePackageManifestImageBoundaries(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, image := range []string{
		"", "judge:latest", "judge", "sha256:", "sha256:" + digest[:63],
		"sha256:" + digest + "a", "sha256:" + strings.Repeat("A", 64),
		"sha256:" + strings.Repeat("g", 64), "@sha256:" + digest,
		"judge@sha512:" + digest, "judge@sha256:" + digest + ":tag",
		"judge @sha256:" + digest, "judge\t@sha256:" + digest,
		"judge\n@sha256:" + digest, "judge\u00a0@sha256:" + digest,
		"sha256:" + digest + "\n", "judge@sha256:" + strings.Repeat("A", 64),
		"judge@@sha256:" + digest, "judge@sha256:other@sha256:" + digest,
		"judge\u0085@sha256:" + digest, "judge\u3000@sha256:" + digest,
		"judge\x00@sha256:" + digest, "judge\x1b@sha256:" + digest,
		"judge\x7f@sha256:" + digest, "镜像@sha256:" + digest,
	} {
		t.Run(image, func(t *testing.T) {
			if _, err := ParsePackageManifest([]byte(dockerManifestJSON(image, 1, 128, 10))); err == nil {
				t.Fatalf("accepted mutable or malformed image %q", image)
			}
		})
	}
}

func TestParsePackageManifestResourceBoundaries(t *testing.T) {
	valid := dockerManifestJSON("sha256:"+strings.Repeat("a", 64), 1, 128, 10)
	for _, tc := range []struct {
		field, original string
		invalid         []string
	}{
		{"cpus", "1", []string{"0", "-1", "257", "1.5", "1.0", "1e0", `"1"`, "true", "9223372036854775808"}},
		{"memoryMb", "128", []string{"0", "15", "1048577", "128.5", `"128"`, "false"}},
		{"timeLimitSeconds", "10", []string{"0", "-1", "86401", "10.5", `"10"`, "[]"}},
	} {
		for _, value := range tc.invalid {
			t.Run(tc.field+"="+value, func(t *testing.T) {
				raw := strings.Replace(valid, `"`+tc.field+`":`+tc.original, `"`+tc.field+`":`+value, 1)
				if _, err := ParsePackageManifest([]byte(raw)); err == nil {
					t.Fatalf("accepted invalid resource: %s", raw)
				}
			})
		}
	}
}

func TestParsePackageManifestSizeBoundary(t *testing.T) {
	raw := `{"schemaVersion":1,"problemId":1}`
	raw += strings.Repeat(" ", (1<<20)-len(raw))
	if _, err := ParsePackageManifest([]byte(raw)); err != nil {
		t.Fatalf("manifest at size limit rejected: %v", err)
	}
}

func dockerManifestJSON(image string, cpus, memoryMb, timeLimitSeconds int) string {
	return fmt.Sprintf(`{"schemaVersion":1,"problemId":1,"docker":{"image":%q,"cpus":%d,"memoryMb":%d,"timeLimitSeconds":%d}}`, image, cpus, memoryMb, timeLimitSeconds)
}
