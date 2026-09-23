package httpapi

import (
	"bytes"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"os"
	"testing"
)

func TestRawUploadValidation(t *testing.T) {
	for _, tc := range []struct {
		name, filename, content, body string
		max                           int64
		want                          int
	}{
		{"raw", "main.c", "application/octet-stream", "source", 10, 200},
		{"unicode", "%E6%B5%8B%E8%AF%95.c", "application/octet-stream", "source", 10, 200},
		{"multipart rejected", "main.c", "multipart/form-data", "source", 10, 415},
		{"path rejected", "..%2Fmain.c", "application/octet-stream", "source", 10, 400},
		{"bad escape", "%ZZ", "application/octet-stream", "source", 10, 400},
		{"over limit", "main.c", "application/octet-stream", "source", 2, 413},
		{"empty file", "main.c", "application/octet-stream", "", 10, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", bytes.NewBufferString(tc.body))
			r.Header.Set("Content-Type", tc.content)
			r.Header.Set("X-SOJ-Filename", tc.filename)
			r.Header.Set("X-SOJ-Content-Type", "text/plain")
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = r
			dir := t.TempDir()
			staged, code, err := streamSingleFile(c, dir, tc.max)
			if code != tc.want {
				t.Fatalf("code=%d err=%v", code, err)
			}
			if code == 200 {
				data, err := os.ReadFile(staged.Path)
				if err != nil || string(data) != tc.body || staged.Size != int64(len(tc.body)) {
					t.Fatalf("upload=%+v err=%v", staged, err)
				}
			} else {
				files, _ := os.ReadDir(dir)
				if len(files) > 0 {
					t.Fatal("failed upload left temporary files")
				}
			}
		})
	}
}
