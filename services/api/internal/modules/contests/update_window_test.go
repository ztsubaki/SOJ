package contests

import (
	"errors"
	"testing"
	"time"
)

func TestContestUpdateWindow(t *testing.T) {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	before := AdminContest{StartsAt: start, EndsAt: start.Add(time.Hour), ScoringPolicy: "best", Problems: summariesForIDs([]int64{1, 2})}
	for _, tc := range []struct {
		name    string
		now     time.Time
		change  func(*AdminContest)
		blocked bool
	}{
		{"before start", start.Add(-time.Second), func(c *AdminContest) { c.ScoringPolicy = "latest" }, false},
		{"start boundary", start, func(c *AdminContest) { c.ScoringPolicy = "latest" }, false},
		{"running start time", start, func(c *AdminContest) { c.StartsAt = start.Add(-time.Hour) }, false},
		{"problem order", start, func(c *AdminContest) { c.Problems = summariesForIDs([]int64{2, 1}) }, false},
		{"running extend", start, func(c *AdminContest) { c.EndsAt = c.EndsAt.Add(time.Hour) }, false},
		{"ended time", before.EndsAt, func(c *AdminContest) { c.EndsAt = c.EndsAt.Add(-time.Second) }, true},
		{"ended same fields", before.EndsAt, func(c *AdminContest) { c.Title = "Correction"; c.Problems = summariesForIDs([]int64{1, 2}) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := before
			tc.change(&after)
			err := validateUpdateWindow(before, after, tc.now)
			if errors.Is(err, ErrContestFixedFields) != tc.blocked {
				t.Fatalf("error=%v, blocked=%v", err, tc.blocked)
			}
		})
	}
}
