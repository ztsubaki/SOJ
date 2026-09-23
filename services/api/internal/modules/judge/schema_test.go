package judge

import "testing"

func TestStructuredScore(t *testing.T) {
	for _, v := range []string{"0", "815.4", "0.00001"} {
		if !validDecimal(v) {
			t.Fatalf("valid score %q rejected", v)
		}
	}
	for _, v := range []string{"", "-1", "01", "1e3", "NaN", "1."} {
		if validDecimal(v) {
			t.Fatalf("invalid score %q accepted", v)
		}
	}
}
