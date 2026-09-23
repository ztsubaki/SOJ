// Package validation contains protocol-level value checks shared by Control
// Plane modules.
package validation

// BusinessID reports whether value follows the opaque SOJ business-ID format
// defined by the internal protocols. Keep this check aligned with the
// PostgreSQL constraints: in particular, the first byte must be alphanumeric.
func BusinessID(value string) bool {
	if len(value) == 0 || len(value) > 128 || !alphaNumeric(value[0]) {
		return false
	}
	for i := 1; i < len(value); i++ {
		c := value[i]
		if !alphaNumeric(c) && c != '.' && c != '_' && c != '~' && c != '-' {
			return false
		}
	}
	return true
}

func alphaNumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
