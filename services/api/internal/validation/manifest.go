package validation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// PackageManifest is the immutable execution contract stored with a package.
type PackageManifest struct {
	SchemaVersion int           `json:"schemaVersion"`
	ProblemID     int64         `json:"problemId"`
	Docker        *DockerConfig `json:"docker,omitempty"`
}

// DockerConfig selects a preloaded immutable image and bounded container resources.
type DockerConfig struct {
	Image            string `json:"image"`
	CPUs             int    `json:"cpus"`
	MemoryMb         int    `json:"memoryMb"`
	TimeLimitSeconds int    `json:"timeLimitSeconds"`
}

// Executor identifies the execution backend selected by a validated manifest.
func (m PackageManifest) Executor() string {
	if m.Docker != nil {
		return "docker"
	}
	return "slurm"
}

// ParsePackageManifest rejects missing, unknown, duplicate, null and mistyped
// fields rather than letting JSON decoding silently choose an execution backend.
func ParsePackageManifest(raw []byte) (PackageManifest, error) {
	var manifest PackageManifest
	if len(raw) > 1<<20 {
		return manifest, errors.New("manifest exceeds size limit")
	}
	fields, err := manifestObject(raw, "schemaVersion", "problemId", "docker")
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(fields["schemaVersion"], &manifest.SchemaVersion); err != nil || manifest.SchemaVersion != 1 {
		return PackageManifest{}, errors.New("manifest schemaVersion must be 1")
	}
	if err := json.Unmarshal(fields["problemId"], &manifest.ProblemID); err != nil || manifest.ProblemID <= 0 || manifest.ProblemID > 9_007_199_254_740_991 {
		return PackageManifest{}, errors.New("manifest problemId must be a positive safe integer")
	}
	if rawDocker, ok := fields["docker"]; ok {
		fields, err := manifestObject(rawDocker, "image", "cpus", "memoryMb", "timeLimitSeconds")
		if err != nil {
			return PackageManifest{}, fmt.Errorf("invalid docker configuration: %w", err)
		}
		var docker DockerConfig
		if err := json.Unmarshal(fields["image"], &docker.Image); err != nil || !immutableDockerImage(docker.Image) {
			return PackageManifest{}, errors.New("docker image must be an immutable lowercase sha256 reference")
		}
		if err := json.Unmarshal(fields["cpus"], &docker.CPUs); err != nil || docker.CPUs < 1 || docker.CPUs > 256 {
			return PackageManifest{}, errors.New("docker cpus must be an integer from 1 to 256")
		}
		if err := json.Unmarshal(fields["memoryMb"], &docker.MemoryMb); err != nil || docker.MemoryMb < 16 || docker.MemoryMb > 1048576 {
			return PackageManifest{}, errors.New("docker memoryMb must be an integer from 16 to 1048576")
		}
		if err := json.Unmarshal(fields["timeLimitSeconds"], &docker.TimeLimitSeconds); err != nil || docker.TimeLimitSeconds < 1 || docker.TimeLimitSeconds > 86400 {
			return PackageManifest{}, errors.New("docker timeLimitSeconds must be an integer from 1 to 86400")
		}
		manifest.Docker = &docker
	}
	return manifest, nil
}

func immutableDockerImage(image string) bool {
	if strings.IndexFunc(image, func(r rune) bool { return r < 0x21 || r > 0x7e }) >= 0 {
		return false
	}
	if strings.HasPrefix(image, "sha256:") && SHA256(strings.TrimPrefix(image, "sha256:")) {
		return true
	}
	repository, digest, ok := strings.Cut(image, "@sha256:")
	return ok && repository != "" && !strings.ContainsRune(repository, '@') && SHA256(digest)
}

// manifestObject enforces exact, case-sensitive field names and a single object.
// A map-only unmarshal would accept duplicate keys; struct decoding also accepts
// case-insensitive aliases, both of which make immutable snapshots ambiguous.
func manifestObject(raw []byte, allowed ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("manifest value must be a non-null object")
	}
	fields := make(map[string]json.RawMessage, len(allowed))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, errors.New("invalid manifest field")
		}
		known := false
		for _, candidate := range allowed {
			if name == candidate {
				known = true
				break
			}
		}
		if !known {
			return nil, fmt.Errorf("unknown manifest field %q", name)
		}
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("duplicate manifest field %q", name)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if bytes.Equal(value, []byte("null")) {
			return nil, fmt.Errorf("manifest field %q must not be null", name)
		}
		fields[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid manifest trailing data")
	}
	return fields, nil
}
