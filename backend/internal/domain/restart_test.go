package domain

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRestartMarksInFlightTaskManual(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	nodes := []Node{{ID: "n", StaticIP: "203.0.113.1", DNSName: "x.invalid"}}
	p, d, v := &tp{}, &td{}, &tv{}
	e, err := New(path, nodes, p, d, v, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	task, err := e.Create("n", "k")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash right after an external call: phase left mid-flight on disk.
	e.mu.Lock()
	e.event(e.state.Tasks[task.ID], "allocated", "interrupted")
	e.save()
	e.mu.Unlock()
	e2, err := New(path, nodes, p, d, v, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	got := e2.Snapshot().Tasks[task.ID]
	if got.Phase != "manual" {
		t.Fatalf("expected manual after restart, got %s", got.Phase)
	}
	if err := e2.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.release != 0 {
		t.Fatal("must not release after restart")
	}
}
