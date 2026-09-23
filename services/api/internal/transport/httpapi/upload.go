package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/ztsubaki/soj/services/api/internal/modules/submissions"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"unicode/utf8"
)

func uploadErrorCode(status int) int {
	switch status {
	case 413:
		return 4130
	case 415:
		return 4150
	case 500:
		return 5000
	default:
		return 4001
	}
}
func uploadMetadata(h http.Header) (string, string, error) {
	if len(h.Values("X-SOJ-Filename")) != 1 || len(h.Values("X-SOJ-Content-Type")) != 1 {
		return "", "", errors.New("upload metadata required")
	}
	encoded := h.Get("X-SOJ-Filename")
	if len(encoded) > 765 {
		return "", "", errors.New("filename too long")
	}
	filename, err := url.PathUnescape(encoded)
	if err != nil || !utf8.ValidString(filename) || len(filename) == 0 || len(filename) > 255 || strings.ContainsAny(filename, "/\\") || strings.IndexFunc(filename, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		return "", "", errors.New("invalid filename")
	}
	// RFC3986: all bytes except unreserved must be percent-encoded.
	for i := 0; i < len(encoded); i++ {
		b := encoded[i]
		if b == '%' {
			i += 2
			continue
		}
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("-._~", rune(b))) {
			return "", "", errors.New("invalid filename encoding")
		}
	}
	content := h.Get("X-SOJ-Content-Type")
	media, params, err := mime.ParseMediaType(content)
	if err != nil || len(params) != 0 || len(content) > 127 || media != content || !strings.Contains(media, "/") || strings.IndexFunc(content, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return "", "", errors.New("invalid content type")
	}
	return filename, content, nil
}
func streamSingleFile(c *gin.Context, root string, max int64) (staged submissions.StagedSource, status int, err error) {
	if c.GetHeader("Content-Type") != "application/octet-stream" {
		return staged, 415, errors.New("raw bytes required")
	}
	filename, content, err := uploadMetadata(c.Request.Header)
	if err != nil {
		return staged, 400, err
	}
	if c.Request.ContentLength > max {
		return staged, 413, errors.New("file exceeds limit")
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return staged, 500, err
	}
	f, err := os.CreateTemp(root, ".upload-*")
	if err != nil {
		return staged, 500, err
	}
	keep := false
	defer func() {
		if !keep {
			os.Remove(f.Name())
		}
	}()
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, hash), io.LimitReader(c.Request.Body, max+1))
	closeErr := f.Close()
	if n > max {
		return staged, 413, errors.New("file exceeds limit")
	}
	if copyErr != nil || closeErr != nil || n == 0 || c.Request.ContentLength >= 0 && c.Request.ContentLength != n {
		return staged, 400, errors.New("incomplete upload")
	}
	keep = true
	return submissions.StagedSource{Path: f.Name(), Filename: filename, ContentType: content, Size: n, SHA256: hex.EncodeToString(hash.Sum(nil))}, 200, nil
}
