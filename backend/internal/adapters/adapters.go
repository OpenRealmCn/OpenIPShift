package adapters

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/OpenRealmCn/OpenIPShift/internal/domain"
)

// MockProvider is a simulation. It never calls AWS and never allocates a real address.
// Allocate() hands out a documentation-range address (TEST-NET-2, RFC 5737).
type MockProvider struct {
	mu           sync.Mutex
	seq          int
	bound        map[string]string // instance -> current IP
	created      map[string]string // ip -> task id
	FailAllocate error
	FailBind     error
}

func NewMockProvider() *MockProvider {
	return &MockProvider{bound: map[string]string{}, created: map[string]string{}}
}
func (m *MockProvider) Allocate(_ context.Context, n domain.Node, taskID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FailAllocate != nil {
		return "", m.FailAllocate
	}
	if _, busy := m.created[taskID]; busy {
		return "", errors.New("allocate already done for this task")
	}
	m.seq++
	ip := fmt.Sprintf("198.51.100.%d", 10+m.seq)
	m.created[ip] = taskID
	m.created[taskID] = ip
	return ip, nil
}
func (m *MockProvider) Bind(_ context.Context, n domain.Node, ip string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FailBind != nil {
		return m.FailBind
	}
	if ip == "" {
		return errors.New("bind: empty ip")
	}
	m.bound[n.ID] = ip
	return nil
}
func (m *MockProvider) OwnedUnattached(_ context.Context, n domain.Node, ip, taskID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ip == "" {
		return false, errors.New("ownership check: no recorded IP")
	}
	owner, ok := m.created[ip]
	if !ok || owner != taskID {
		return false, fmt.Errorf("ownership check failed: %s not created by %s", ip, taskID)
	}
	if cur, attached := m.bound[n.ID]; attached && cur == ip {
		return false, errors.New("ownership check failed: ip still attached to node")
	}
	return true, nil
}
func (m *MockProvider) Release(_ context.Context, n domain.Node, ip string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if task, ok := m.created[ip]; !ok || task == "" {
		return fmt.Errorf("refusing to release unowned ip %s", ip)
	}
	if cur, attached := m.bound[n.ID]; attached && cur == ip {
		return fmt.Errorf("refusing to release ip %s: still attached", ip)
	}
	delete(m.created, ip)
	return nil
}

// MockDNS is a simulation. It stores records in memory only and never calls Cloudflare.
type MockDNS struct {
	mu                  sync.Mutex
	Records             map[string]string
	FailSet, FailVerify error
	TTLSeconds          int
}

func NewMockDNS() *MockDNS { return &MockDNS{Records: map[string]string{}, TTLSeconds: 300} }
func (d *MockDNS) SetA(_ context.Context, n domain.Node, ip string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.FailSet != nil {
		return d.FailSet
	}
	if ip == "" {
		return errors.New("dns: empty ip")
	}
	d.Records[n.DNSName] = ip
	return nil
}
func (d *MockDNS) VerifyA(_ context.Context, n domain.Node, ip string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.FailVerify != nil {
		return d.FailVerify
	}
	if d.Records[n.DNSName] != ip {
		return fmt.Errorf("dns verification mismatch for %s", n.DNSName)
	}
	return nil
}

// MockVerifier simulates both checks. Simulated proxy success is NOT a protocol handshake.
type MockVerifier struct{ FailEgress, FailProxy error }

func (v MockVerifier) Egress(context.Context, domain.Node, string) error { return v.FailEgress }
func (v MockVerifier) Proxy(context.Context, domain.Node, string) error  { return v.FailProxy }

// EgressChecker is a real reachability check of the instance's outbound address.
// It only proves the host answers; it never proves proxy protocol success.
type EgressChecker struct{ DialTimeout time.Duration }

func (c EgressChecker) Egress(ctx context.Context, n domain.Node, ip string) error {
	if n.ProxyTarget == "" {
		return errors.New("egress: node has no proxy target to probe")
	}
	if strings.Contains(n.ProxyTarget, "://") {
		return errors.New("egress: target must be host:port, not a URL")
	}
	d := net.Dialer{Timeout: c.DialTimeout}
	conn, err := d.DialContext(ctx, "tcp", n.ProxyTarget)
	if err != nil {
		return fmt.Errorf("egress probe failed: %w", err)
	}
	return conn.Close()
}

// TCPProbe is deliberately NOT a domain.Verifier. Connecting to a port proves nothing
// about a proxy protocol handshake, so it must never be wired in as a proxy success signal.
type TCPProbe struct{ Timeout time.Duration }

func (p TCPProbe) Dial(ctx context.Context, target string) error {
	d := net.Dialer{Timeout: p.Timeout}
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return err
	}
	return conn.Close()
}
