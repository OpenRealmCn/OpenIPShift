package domain

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type tp struct {
	alloc, bind, release int
	owned                bool
	fail                 error
}

func (p *tp) Allocate(context.Context, Node, string) (string, error) {
	p.alloc++
	return "198.51.100.20", p.fail
}
func (p *tp) Bind(context.Context, Node, string) error { p.bind++; return p.fail }
func (p *tp) OwnedUnattached(context.Context, Node, string, string) (bool, error) {
	return p.owned, nil
}
func (p *tp) Release(context.Context, Node, string) error { p.release++; return p.fail }

type td struct {
	set, verify int
	fail        error
}

func (d *td) SetA(context.Context, Node, string) error    { d.set++; return d.fail }
func (d *td) VerifyA(context.Context, Node, string) error { d.verify++; return d.fail }

type tv struct {
	egress, proxy int
	fail          error
}

func (v *tv) Egress(context.Context, Node, string) error { v.egress++; return v.fail }
func (v *tv) Proxy(context.Context, Node, string) error  { v.proxy++; return v.fail }
func TestRollbackOnProxyFailureAndPersistence(t *testing.T) {
	p := &tp{}
	d := &td{}
	v := &tv{fail: errors.New("proxy failed")}
	e, err := New(filepath.Join(t.TempDir(), "state.json"), []Node{{ID: "n", StaticIP: "203.0.113.1", DNSName: "x"}}, p, d, v, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	task, err := e.Create("n", "key-1")
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Run(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	s := e.Snapshot()
	if s.Tasks[task.ID].Phase != "manual" {
		t.Fatalf("phase=%s", s.Tasks[task.ID].Phase)
	}
	if p.release != 0 {
		t.Fatal("must not release after failure")
	}
	e2, err := New(filepath.Join(filepath.Dir(e.path), "state.json"), []Node{{ID: "n", StaticIP: "203.0.113.1", DNSName: "x"}}, p, d, v, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if e2.Snapshot().Tasks[task.ID].Phase != "manual" {
		t.Fatal("state did not persist")
	}
}
func TestBusyAndIdempotency(t *testing.T) {
	p := &tp{}
	d := &td{}
	v := &tv{}
	e, _ := New(filepath.Join(t.TempDir(), "s"), []Node{{ID: "n"}}, p, d, v, time.Hour)
	a, _ := e.Create("n", "same")
	b, err := e.Create("n", "same")
	if err != nil || a.ID != b.ID {
		t.Fatal("idempotency failed")
	}
	if _, err = e.Create("n", "other"); !errors.Is(err, ErrBusy) {
		t.Fatal("busy guard failed")
	}
}
func TestReleaseRequiresOwnership(t *testing.T) {
	p := &tp{owned: false}
	d := &td{}
	v := &tv{}
	e, _ := New(filepath.Join(t.TempDir(), "s"), []Node{{ID: "n", StaticIP: "203.0.113.1"}}, p, d, v, 0)
	task, _ := e.Create("n", "x")
	_ = e.Run(context.Background(), task.ID)
	p.owned = false
	_ = e.Tick(context.Background())
	if p.release != 0 {
		t.Fatal("released without ownership")
	}
}
