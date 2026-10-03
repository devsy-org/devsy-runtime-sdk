package processprobe_test

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

type event struct {
	Role string `json:"role"`
	Kind string `json:"kind"`
	PID  int    `json:"pid"`
}

type process struct {
	handle *os.Process
	pid    int
	role   string
	pong   chan struct{}
	conn   net.Conn
	closed chan struct{}
}

type observer struct {
	directory string
	listener  net.Listener
	pending   []event
	events    chan event
	mu        sync.Mutex
	processes map[string]*process
}

func observe(t *testing.T) *observer {
	t.Helper()
	directory, err := os.MkdirTemp("", "dpc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove fixture directory: %v", err)
		}
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	o := &observer{
		directory: directory,
		listener:  listener,
		events:    make(chan event, 32),
		processes: make(map[string]*process),
	}
	t.Cleanup(func() { o.cleanup(t) })
	go o.accept()
	return o
}

func (o *observer) accept() {
	for {
		conn, err := o.listener.Accept()
		if err != nil {
			return
		}
		go o.read(conn)
	}
}

func (o *observer) read(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	decoder := json.NewDecoder(conn)
	var ready event
	if err := decoder.Decode(&ready); err != nil {
		return
	}
	handle, err := os.FindProcess(ready.PID)
	if err != nil {
		return
	}
	p := &process{
		handle: handle,
		pid:    ready.PID,
		conn:   conn,
		closed: make(chan struct{}),
		role:   ready.Role,
		pong:   make(chan struct{}, 1),
	}
	defer close(p.closed)
	o.mu.Lock()
	o.processes[ready.Role] = p
	o.mu.Unlock()
	o.events <- ready
	for {
		var next event
		if err := decoder.Decode(&next); err != nil {
			return
		}
		if next.Kind == "alive" {
			p.pong <- struct{}{}
			continue
		}
		o.events <- next
	}
}

func (o *observer) await(t *testing.T, role, kind string) event {
	t.Helper()
	if e, ok := o.take(role, kind); ok {
		return e
	}
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for {
		select {
		case e := <-o.events:
			if e.Role == role && e.Kind == kind {
				return e
			}
			o.pending = append(o.pending, e)
		case <-timer.C:
			t.Fatalf("timed out waiting for %s/%s", role, kind)
			return event{}
		}
	}
}

func (o *observer) lookup(t *testing.T, role string) *process {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	p := o.processes[role]
	if p == nil || p.pid <= 0 {
		t.Fatalf("missing %s process", role)
	}
	return p
}

func (p *process) exited(t *testing.T) {
	t.Helper()
	select {
	case <-p.closed:
	case <-time.After(10 * time.Second):
		t.Fatalf("process %d still holds its observer connection", p.pid)
	}
}

func (p *process) alive(t *testing.T) {
	t.Helper()
	select {
	case <-p.closed:
		t.Fatalf("process %d unexpectedly closed its observer connection", p.pid)
	default:
	}
	if err := p.conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(p.conn).Encode(event{Role: p.role, Kind: "ping"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.pong:
	case <-p.closed:
		t.Fatalf("process %d exited during liveness check", p.pid)
	case <-time.After(5 * time.Second):
		t.Fatalf("process %d did not answer liveness check", p.pid)
	}
}

func (p *process) kill(t *testing.T) {
	t.Helper()
	if err := p.handle.Kill(); err != nil {
		t.Fatal(err)
	}
	p.exited(t)
}

func (o *observer) cleanup(t *testing.T) {
	t.Helper()

	_ = o.listener.Close()
	o.mu.Lock()
	defer o.mu.Unlock()
	// Retain process handles from readiness through cleanup, rather than rediscovering a PID after death.
	for _, p := range o.processes {
		select {
		case <-p.closed:
		default:
			if err := p.handle.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Errorf("kill fixture %d: %v", p.pid, err)
			}
			select {
			case <-p.closed:
			case <-time.After(10 * time.Second):
				t.Errorf("fixture %d did not disconnect after cleanup", p.pid)
			}
		}
		_ = p.conn.Close()
		_ = p.handle.Release()
	}
}

func (o *observer) take(role, kind string) (event, bool) {
	for i, e := range o.pending {
		if e.Role == role && e.Kind == kind {
			o.pending = append(o.pending[:i], o.pending[i+1:]...)
			return e, true
		}
	}
	return event{}, false
}

func (o *observer) readyChild(t *testing.T) *process {
	t.Helper()
	armed := o.await(t, "plugin", "child-ready")
	ready := o.await(t, "child", "ready")
	if armed.PID != ready.PID {
		t.Fatal("child readiness PID mismatch")
	}
	return o.lookup(t, "child")
}
