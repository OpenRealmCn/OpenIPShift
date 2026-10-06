package domain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Node struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Region      string `json:"region"`
	Instance    string `json:"instance"`
	StaticIP    string `json:"staticIP"`
	DNSName     string `json:"dnsName"`
	ProxyTarget string `json:"proxyTarget"`
}
type Event struct {
	At      time.Time `json:"at"`
	Phase   string    `json:"phase"`
	Message string    `json:"message"`
}
type Task struct {
	ID           string    `json:"id"`
	NodeID       string    `json:"nodeId"`
	Key          string    `json:"key"`
	OldIP        string    `json:"oldIP"`
	NewIP        string    `json:"newIP"`
	Phase        string    `json:"phase"`
	Mode         string    `json:"mode"`
	ReleaseAfter time.Time `json:"releaseAfter"`
	CreatedAt    time.Time `json:"createdAt"`
	Events       []Event   `json:"events"`
	Error        string    `json:"error,omitempty"`
}
type State struct {
	Nodes []Node           `json:"nodes"`
	Tasks map[string]*Task `json:"tasks"`
}

// Providers must implement ownership checks on every release. IP denotes an owned resource handle, not merely an address.
type Provider interface {
	Allocate(context.Context, Node, string) (string, error)
	Bind(context.Context, Node, string) error
	OwnedUnattached(context.Context, Node, string, string) (bool, error)
	Release(context.Context, Node, string) error
}
type DNS interface {
	SetA(context.Context, Node, string) error
	VerifyA(context.Context, Node, string) error
}
type Verifier interface {
	Egress(context.Context, Node, string) error
	Proxy(context.Context, Node, string) error
}

var ErrInvalidNode = errors.New("invalid node")
var ErrDuplicateNode = errors.New("node id already exists")
var ErrNodeNotFound = errors.New("unknown node")

var ErrBusy = errors.New("node already has an active or unresolved task")

type Engine struct {
	mu       sync.Mutex
	state    State
	path     string
	provider Provider
	dns      DNS
	verifier Verifier
	cooldown time.Duration
	now      func() time.Time
}

func New(path string, nodes []Node, p Provider, d DNS, v Verifier, cooldown time.Duration) (*Engine, error) {
	e := &Engine{path: path, provider: p, dns: d, verifier: v, cooldown: cooldown, now: time.Now, state: State{Nodes: append([]Node{}, nodes...), Tasks: map[string]*Task{}}}
	b, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(b, &e.state); err != nil {
			return nil, err
		}
		if e.state.Tasks == nil {
			return nil, errors.New("invalid state: missing tasks")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if e.state.Nodes == nil {
		e.state.Nodes = []Node{}
	}
	// Validate persisted identity uniqueness, but retain legacy records for editing.
	seen := map[string]bool{}
	for _, n := range e.state.Nodes {
		if n.ID == "" || seen[n.ID] {
			return nil, ErrInvalidNode
		}
		seen[n.ID] = true
	}
	// Any interrupted external operation has unknown outcome. Never blindly repeat it after restart.
	for _, t := range e.state.Tasks {
		// planned has no external side effect yet, so it stays safely resumable.
		// Any other unfinished phase means a cloud/DNS call was in flight: outcome unknown.
		if t.Phase != "planned" && t.Phase != "completed" && t.Phase != "rolled_back" && t.Phase != "cooling" && t.Phase != "manual" {
			e.event(t, "manual", "服务重启：操作结果不确定，必须人工核对云端与 DNS；禁止自动释放")
		}
	}
	if err = e.save(); err != nil {
		return nil, err
	}
	return e, nil
}
func (e *Engine) save() error {
	b, err := json.MarshalIndent(e.state, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(e.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(e.path), ".state-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err == nil {
		err = os.Rename(name, e.path)
	}
	if err == nil {
		var dir *os.File
		dir, err = os.Open(filepath.Dir(e.path))
		if err == nil {
			err = dir.Sync()
			dir.Close()
		}
	}
	return err
}
func (e *Engine) event(t *Task, p, msg string) {
	t.Phase = p
	t.Events = append(t.Events, Event{e.now().UTC(), p, msg})
}
func (e *Engine) Snapshot() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, _ := json.Marshal(e.state)
	var s State
	json.Unmarshal(b, &s)
	return s
}
func (e *Engine) Create(nodeID, key string) (Task, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if key == "" || len(key) > 128 {
		return Task{}, errors.New("Idempotency-Key required, max 128 characters")
	}
	for _, t := range e.state.Tasks {
		if t.NodeID == nodeID && t.Key == key {
			return *t, nil
		}
	}
	var n *Node
	for i := range e.state.Nodes {
		if e.state.Nodes[i].ID == nodeID {
			n = &e.state.Nodes[i]
		}
	}
	if n == nil {
		return Task{}, errors.New("unknown node")
	}
	for _, t := range e.state.Tasks {
		if t.NodeID == nodeID && t.Phase != "completed" && t.Phase != "rolled_back" {
			return Task{}, ErrBusy
		}
	}
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return Task{}, err
	}
	t := &Task{ID: hex.EncodeToString(buf[:]), NodeID: nodeID, Key: key, OldIP: n.StaticIP, Phase: "planned", Mode: "mock", CreatedAt: e.now().UTC(), Events: []Event{}}
	e.event(t, "planned", "模拟任务已创建：不会更改 AWS 或 DNS")
	e.state.Tasks[t.ID] = t
	if err := e.save(); err != nil {
		delete(e.state.Tasks, t.ID)
		return Task{}, err
	}
	return *t, nil
}
func (e *Engine) Run(ctx context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.state.Tasks[id]
	if t == nil {
		return errors.New("unknown task")
	}
	if t.Phase != "planned" {
		return nil
	}
	n := e.node(t.NodeID)
	step := func(p, msg string, fn func() error) error {
		e.event(t, p+"_pending", "即将执行："+msg)
		if err := e.save(); err != nil {
			return err
		}
		if err := fn(); err != nil {
			return err
		}
		e.event(t, p, msg)
		return e.save()
	}
	err := step("allocated", "模拟申请新静态 IPv4", func() error { var err error; t.NewIP, err = e.provider.Allocate(ctx, n, t.ID); return err })
	if err == nil {
		err = step("bound", "模拟绑定新 IPv4", func() error { return e.provider.Bind(ctx, n, t.NewIP) })
	}
	if err == nil {
		err = step("egress_verified", "模拟出口验证通过；未实际探测", func() error { return e.verifier.Egress(ctx, n, t.NewIP) })
	}
	if err == nil {
		err = step("dns_updated", "模拟更新 DNS A", func() error { return e.dns.SetA(ctx, n, t.NewIP) })
	}
	if err == nil {
		err = step("dns_verified", "模拟 DNS 验证通过", func() error { return e.dns.VerifyA(ctx, n, t.NewIP) })
	}
	if err == nil {
		err = step("proxy_verified", "模拟协议握手通过；未实际代理请求", func() error { return e.verifier.Proxy(ctx, n, t.NewIP) })
	}
	if err != nil {
		return e.rollback(ctx, t, n, err)
	}
	t.ReleaseAfter = e.now().Add(e.cooldown)
	e.event(t, "cooling", "新连接模拟验证通过；保留旧 IPv4，等待延迟释放。等待不证明旧连接恢复")
	return e.save()
}
func (e *Engine) rollback(ctx context.Context, t *Task, n Node, cause error) error {
	t.Error = cause.Error()
	e.event(t, "rollback_pending", "失败：尝试恢复旧绑定及旧 DNS；保留两端 IP，任何恢复错误转人工")
	if err := e.save(); err != nil {
		return err
	}
	bind := e.provider.Bind(ctx, n, t.OldIP)
	dns := e.dns.SetA(ctx, n, t.OldIP)
	if bind != nil || dns != nil {
		e.event(t, "manual", fmt.Sprintf("回滚不确定：绑定=%v DNS=%v；人工核对，禁止释放", bind, dns))
	} else {
		e.event(t, "manual", "旧绑定与 DNS 回滚完成，但缓存/旧连接及新 IP 清理需人工处理；所有 IP 保留")
	}
	return e.save()
}
func (e *Engine) node(id string) Node {
	for _, n := range e.state.Nodes {
		if n.ID == id {
			return n
		}
	}
	return Node{}
}

// Tick revalidates egress, DNS and actual proxy protocol before every deferred release.
func (e *Engine) Tick(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := []string{}
	for id := range e.state.Tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		t := e.state.Tasks[id]
		if t.Phase != "cooling" || e.now().Before(t.ReleaseAfter) {
			continue
		}
		n := e.node(t.NodeID)
		if err := e.verifier.Egress(ctx, n, t.NewIP); err != nil {
			return e.rollback(ctx, t, n, err)
		}
		if err := e.dns.VerifyA(ctx, n, t.NewIP); err != nil {
			return e.rollback(ctx, t, n, err)
		}
		if err := e.verifier.Proxy(ctx, n, t.NewIP); err != nil {
			return e.rollback(ctx, t, n, err)
		}
		ok, err := e.provider.OwnedUnattached(ctx, n, t.OldIP, t.ID)
		if err != nil || !ok || t.OldIP == t.NewIP || t.NewIP == "" {
			e.event(t, "manual", "旧 IP 所有权/未绑定状态无法确认：禁止释放")
			if err = e.save(); err != nil {
				return err
			}
			continue
		}
		e.event(t, "release_pending", "已复验；准备释放确认归属且未绑定的旧 IP")
		if err = e.save(); err != nil {
			return err
		}
		if err = e.provider.Release(ctx, n, t.OldIP); err != nil {
			t.Error = err.Error()
			e.event(t, "manual", "释放结果不确定，请人工核对")
		} else {
			for i := range e.state.Nodes {
				if e.state.Nodes[i].ID == t.NodeID {
					e.state.Nodes[i].StaticIP = t.NewIP
				}
			}
			e.event(t, "completed", "模拟旧 IP 释放完成；所有检查都是模拟")
		}
		if err = e.save(); err != nil {
			return err
		}
	}
	return nil
}

var nodeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var regionPattern = regexp.MustCompile(`^[a-z]{2}(?:-[a-z]+)+-[0-9]+$`)
var instancePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)

func validHost(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func ValidateNode(n Node) error {
	if !nodeIDPattern.MatchString(n.ID) {
		return fmt.Errorf("%w: ID must be 1-64 letters, digits, _ or -", ErrInvalidNode)
	}
	if strings.TrimSpace(n.Name) == "" || len(n.Name) > 128 {
		return fmt.Errorf("%w: name required (max 128 bytes)", ErrInvalidNode)
	}
	if !regionPattern.MatchString(n.Region) || !instancePattern.MatchString(n.Instance) {
		return fmt.Errorf("%w: invalid region or instance", ErrInvalidNode)
	}
	if ip := net.ParseIP(n.StaticIP); ip == nil || ip.To4() == nil {
		return fmt.Errorf("%w: staticIP must be IPv4", ErrInvalidNode)
	}
	if !validHost(n.DNSName) {
		return fmt.Errorf("%w: DNS name required, without scheme or port", ErrInvalidNode)
	}
	host, port, err := net.SplitHostPort(n.ProxyTarget)
	number, perr := strconv.Atoi(port)
	if err != nil || perr != nil || number < 1 || number > 65535 || !(validHost(host) || net.ParseIP(host) != nil) {
		return fmt.Errorf("%w: proxyTarget must be host:port", ErrInvalidNode)
	}
	return nil
}
func (e *Engine) busy(id string) bool {
	for _, t := range e.state.Tasks {
		if t.NodeID == id && t.Phase != "completed" && t.Phase != "rolled_back" {
			return true
		}
	}
	return false
}
func (e *Engine) AddNode(n Node) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ValidateNode(n); err != nil {
		return err
	}
	for _, existing := range e.state.Nodes {
		if existing.ID == n.ID {
			return ErrDuplicateNode
		}
	}
	old := e.state.Nodes
	e.state.Nodes = append(append([]Node{}, old...), n)
	if err := e.save(); err != nil {
		e.state.Nodes = old
		return err
	}
	return nil
}
func (e *Engine) UpdateNode(id string, n Node) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if n.ID != id {
		return fmt.Errorf("%w: ID is immutable", ErrInvalidNode)
	}
	if err := ValidateNode(n); err != nil {
		return err
	}
	for i := range e.state.Nodes {
		if e.state.Nodes[i].ID == id {
			if e.busy(id) {
				return ErrBusy
			}
			old := e.state.Nodes[i]
			e.state.Nodes[i] = n
			if err := e.save(); err != nil {
				e.state.Nodes[i] = old
				return err
			}
			return nil
		}
	}
	return ErrNodeNotFound
}
func (e *Engine) DeleteNode(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.state.Nodes {
		if e.state.Nodes[i].ID == id {
			if e.busy(id) {
				return ErrBusy
			}
			old := e.state.Nodes
			e.state.Nodes = append(append([]Node{}, old[:i]...), old[i+1:]...)
			if err := e.save(); err != nil {
				e.state.Nodes = old
				return err
			}
			return nil
		}
	}
	return ErrNodeNotFound
}
