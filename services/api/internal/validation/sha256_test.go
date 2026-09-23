package validation

import "testing"

func TestSHA256(t *testing.T) {
	if !SHA256("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatal("SHA256() rejected a canonical digest")
	}
	for _, value := range []string{"", "not-a-hash", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if SHA256(value) {
			t.Errorf("SHA256(%q) = true, want false", value)
		}
	}
}
