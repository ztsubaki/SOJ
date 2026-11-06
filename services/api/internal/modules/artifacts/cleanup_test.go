package artifacts

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestCleanupTemporaryObjects(t *testing.T) {
	root := t.TempDir()
	cutoff := time.Now().Add(-24 * time.Hour).Unix()
	cases := []struct {
		name    string
		mtime   int64
		removed bool
	}{
		{name: ".upload-expired", mtime: cutoff - 1, removed: true},
		{name: ".upload-boundary", mtime: cutoff},
		{name: ".upload-recent", mtime: cutoff + 1},
		{name: "ordinary-object", mtime: cutoff - 1},
		{name: "ordinary.upload-object", mtime: cutoff - 1},
	}
	// More than one directory batch must be processed without sorting or
	// depending on filesystem iteration order.
	for i := range 600 {
		cases = append(cases, struct {
			name    string
			mtime   int64
			removed bool
		}{name: ".upload-old-" + strconv.Itoa(i), mtime: cutoff - 1, removed: true})
	}
	for _, tc := range cases {
		path := filepath.Join(root, tc.name)
		if err := os.WriteFile(path, []byte("upload"), 0600); err != nil {
			t.Fatal(err)
		}
		mtime := time.Unix(tc.mtime, 0)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	cleanupTemporaryObjects(root, cutoff)

	for _, tc := range cases {
		data, err := os.ReadFile(filepath.Join(root, tc.name))
		if tc.removed {
			if !os.IsNotExist(err) {
				t.Errorf("%s: read error = %v, want not exist", tc.name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: retained object read error = %v", tc.name, err)
		} else if string(data) != "upload" {
			t.Errorf("%s: retained object content = %q, want upload", tc.name, data)
		}
	}
}
