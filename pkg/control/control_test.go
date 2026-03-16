package control

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestNewServer(t *testing.T) {
	s := NewServer("/tmp/test.sock", testLogger())
	if s == nil {
		t.Fatal("expected non-nil server")
	}
	if s.socketPath != "/tmp/test.sock" {
		t.Errorf("expected socket path /tmp/test.sock, got %s", s.socketPath)
	}
}

func TestNewServer_NilLogger(t *testing.T) {
	s := NewServer("/tmp/test.sock", nil)
	if s == nil {
		t.Fatal("expected non-nil server")
	}
	if s.logger == nil {
		t.Error("expected default logger when nil passed")
	}
}

func TestServer_Handle(t *testing.T) {
	s := NewServer("/tmp/test.sock", testLogger())

	s.Handle("test.method", func(params json.RawMessage) (any, error) {
		return map[string]string{"result": "ok"}, nil
	})

	if _, ok := s.handlers["test.method"]; !ok {
		t.Error("handler not registered")
	}
}

func TestNewClient(t *testing.T) {
	c := NewClient("/tmp/test.sock")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.socketPath != "/tmp/test.sock" {
		t.Errorf("expected socket path /tmp/test.sock, got %s", c.socketPath)
	}
}

func TestServer_DispatchHandler_NotFound(t *testing.T) {
	s := NewServer("/tmp/test.sock", testLogger())

	req := Request{Method: "nonexistent", ID: 1}
	resp := s.dispatchHandler(req)

	if resp.Error == "" {
		t.Error("expected error for unknown method")
	}
	if resp.ID != 1 {
		t.Errorf("expected ID 1, got %d", resp.ID)
	}
}

func TestServer_DispatchHandler_Success(t *testing.T) {
	s := NewServer("/tmp/test.sock", testLogger())

	s.Handle("echo", func(params json.RawMessage) (any, error) {
		return map[string]string{"echo": "hello"}, nil
	})

	req := Request{Method: "echo", ID: 42}
	resp := s.dispatchHandler(req)

	if resp.Error != "" {
		t.Errorf("unexpected error: %s", resp.Error)
	}
	if resp.ID != 42 {
		t.Errorf("expected ID 42, got %d", resp.ID)
	}
	if resp.Result == nil {
		t.Error("expected non-nil result")
	}
}

func TestServer_DispatchHandler_Error(t *testing.T) {
	s := NewServer("/tmp/test.sock", testLogger())

	s.Handle("fail", func(params json.RawMessage) (any, error) {
		return nil, fmt.Errorf("something went wrong")
	})

	req := Request{Method: "fail", ID: 1}
	resp := s.dispatchHandler(req)

	if resp.Error == "" {
		t.Error("expected error in response")
	}
}

func TestServer_ClientServer_Integration(t *testing.T) {
	// Use /tmp directly to keep socket path under macOS 104-char limit
	socketPath := fmt.Sprintf("/tmp/lantern-test-%d.sock", time.Now().UnixNano())
	t.Cleanup(func() { os.Remove(socketPath) })

	s := NewServer(socketPath, testLogger())

	// Register a handler
	s.Handle("greet", func(params json.RawMessage) (any, error) {
		var p struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return map[string]string{"greeting": "hello " + p.Name}, nil
	})

	s.Handle("status", func(params json.RawMessage) (any, error) {
		return map[string]string{"status": "ok"}, nil
	})

	// Start server
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := s.Start(ctx); err != nil {
			// Server stops when ctx cancelled
		}
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Test Call with params
	c := NewClient(socketPath)
	result, err := c.Call("greet", map[string]string{"name": "world"})
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	var greeting map[string]string
	if err := json.Unmarshal(result, &greeting); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if greeting["greeting"] != "hello world" {
		t.Errorf("expected 'hello world', got %q", greeting["greeting"])
	}

	// Test CallResult
	var status map[string]string
	if err := c.CallResult("status", nil, &status); err != nil {
		t.Fatalf("CallResult failed: %v", err)
	}
	if status["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", status["status"])
	}

	// Test unknown method
	_, err = c.Call("unknown", nil)
	if err == nil {
		t.Error("expected error for unknown method")
	}

	// Cancel context to stop server
	cancel()
	time.Sleep(100 * time.Millisecond)
}

func TestClient_Call_NoServer(t *testing.T) {
	c := NewClient("/tmp/nonexistent-socket.sock")
	_, err := c.Call("test", nil)
	if err == nil {
		t.Error("expected error when no server is running")
	}
}
