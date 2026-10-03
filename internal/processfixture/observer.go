package main

import (
	"encoding/json"
	"net"
	"os"
	"sync"
)

// One writer serializes RPC lifecycle events and independent liveness replies.
type reporter struct {
	mu      sync.Mutex
	encoder *json.Encoder
	role    string
	address string
}

func (r *reporter) Encode(e event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.encoder.Encode(e)
}

func (r *reporter) respond(conn net.Conn, disconnected chan<- struct{}) {
	defer close(disconnected)
	decoder := json.NewDecoder(conn)
	for {
		var e event
		if err := decoder.Decode(&e); err != nil {
			return
		}
		if e.Kind != "ping" {
			return
		}
		if err := r.Encode(event{Role: r.role, Kind: "alive", PID: os.Getpid()}); err != nil {
			return
		}
	}
}
