package domain

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestCustomNodes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	e, err := New(path, nil, &tp{}, &td{}, &tv{}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Snapshot().Nodes) != 0 {
		t.Fatal("expected empty nodes")
	}
	var n Node
	for i := 0; i < 7; i++ {
		n = Node{ID: fmt.Sprintf("node-%d", i), Name: "Node", Region: "ap-northeast-1", Instance: "instance", StaticIP: "203.0.113.10", DNSName: "node.example.com", ProxyTarget: "node.example.com:443"}
		if err := e.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.AddNode(n); !errors.Is(err, ErrDuplicateNode) {
		t.Fatal("duplicate accepted")
	}
	n.Name = "Updated"
	if err := e.UpdateNode(n.ID, n); err != nil {
		t.Fatal(err)
	}
	restored, err := New(path, nil, &tp{}, &td{}, &tv{}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Snapshot().Nodes) != 7 || restored.node(n.ID).Name != "Updated" {
		t.Fatal("persistence failed")
	}
	if _, err := restored.Create(n.ID, "key"); err != nil {
		t.Fatal(err)
	}
	if err := restored.UpdateNode(n.ID, n); !errors.Is(err, ErrBusy) {
		t.Fatal("active update accepted")
	}
	if err := restored.DeleteNode(n.ID); !errors.Is(err, ErrBusy) {
		t.Fatal("active delete accepted")
	}
	if err := restored.DeleteNode("node-0"); err != nil {
		t.Fatal(err)
	}
	n.ID = "invalid id"
	if err := restored.AddNode(n); !errors.Is(err, ErrInvalidNode) {
		t.Fatal("invalid accepted")
	}
}
