package stats

import (
	"testing"
	"time"
)

func TestClusterStatus(t *testing.T) {
	now := time.Now()
	s := clusterStatus(ClusterSnapshot{Sessions: 4, Empty: 1, NoQueue: 1, Queued: 2, UpdatedAt: &now})
	if s.TotalAgents != s.EmptyAgents+s.NoQueueAgents+s.QueuedAgents || !s.UpdatedAt.AsTime().Equal(now) {
		t.Fatalf("invalid snapshot: %v", s)
	}
}
