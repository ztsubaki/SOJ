package validation

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// SHA256 accepts the lowercase hexadecimal digest used in artifact metadata.
func SHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
