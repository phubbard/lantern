package control

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
)

// Request represents a JSON-RPC request.
type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
	ID     int             `json:"id"`
}

// Response represents a JSON-RPC response.
type Response struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	ID     int             `json:"id"`
}

// HandlerFunc is the signature for RPC method handlers.
// Returns the result to be JSON-encoded, or an error.
type HandlerFunc func(params json.RawMessage) (any, error)

// Server implements a Unix domain socket JSON-RPC server.
type Server struct {
	socketPath string
	handlers   map[string]HandlerFunc
	listener   net.Listener
	logger     *slog.Logger
	mu         sync.RWMutex
}

// NewServer creates a new control server.
func NewServer(socketPath string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return &Server{
		socketPath: socketPath,
		handlers:   make(map[string]HandlerFunc),
		logger:     logger,
	}
}

// Handle registers a handler for an RPC method.
func (s *Server) Handle(method string, handler HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.handlers[method] = handler
	s.logger.Debug("registered handler", "method", method)
}

// Start begins listening on the Unix domain socket.
// Removes any existing socket file before listening.
// Each connection is handled in its own goroutine.
// Returns when ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	// Remove old socket file if it exists
	if err := os.Remove(s.socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove old socket file: %w", err)
	}

	// Listen on Unix domain socket
	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket %s: %w", s.socketPath, err)
	}

	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()

	s.logger.Info("control server started", "socket", s.socketPath)

	// Set restrictive permissions on socket file
	if err := os.Chmod(s.socketPath, 0600); err != nil {
		s.logger.Warn("failed to chmod socket file", "error", err)
	}

	// Accept connections in a goroutine
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				// Check if it's a context cancellation
				select {
				case <-ctx.Done():
					return
				default:
					s.logger.Error("accept error", "error", err)
					return
				}
			}

			// Handle each connection in its own goroutine
			go s.handleConnection(conn)
		}
	}()

	// Wait for context to be cancelled
	<-ctx.Done()
	return s.Stop()
}

// handleConnection reads JSON requests, dispatches to handlers, and writes responses.
func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var req Request
		if err := decoder.Decode(&req); err != nil {
			// EOF or decode error means client disconnected
			if err.Error() == "EOF" {
				return
			}
			s.logger.Error("decode error", "error", err)
			return
		}

		// Dispatch to handler
		resp := s.dispatchHandler(req)

		// Send response
		if err := encoder.Encode(resp); err != nil {
			s.logger.Error("encode error", "error", err)
			return
		}
	}
}

// dispatchHandler dispatches a request to the appropriate handler.
func (s *Server) dispatchHandler(req Request) Response {
	resp := Response{ID: req.ID}

	s.mu.RLock()
	handler, exists := s.handlers[req.Method]
	s.mu.RUnlock()

	if !exists {
		resp.Error = fmt.Sprintf("method not found: %s", req.Method)
		return resp
	}

	// Call handler
	result, err := handler(req.Params)
	if err != nil {
		resp.Error = err.Error()
		return resp
	}

	// Marshal result to JSON
	if result != nil {
		data, err := json.Marshal(result)
		if err != nil {
			resp.Error = fmt.Sprintf("failed to marshal result: %v", err)
			return resp
		}
		resp.Result = json.RawMessage(data)
	}

	return resp
}

// Stop closes the listener and removes the socket file.
func (s *Server) Stop() error {
	s.mu.Lock()
	listener := s.listener
	s.mu.Unlock()

	if listener != nil {
		listener.Close()
	}

	// Remove socket file
	if err := os.Remove(s.socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove socket file: %w", err)
	}

	s.logger.Info("control server stopped")
	return nil
}

// Client implements a client for calling the control server.
type Client struct {
	socketPath string
}

// NewClient creates a new control client.
func NewClient(socketPath string) *Client {
	return &Client{socketPath: socketPath}
}

// Call sends a method call to the server and returns the raw result.
// Handles connection errors gracefully.
func (c *Client) Call(method string, params any) (json.RawMessage, error) {
	// Connect to Unix socket
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to socket %s: %w", c.socketPath, err)
	}
	defer conn.Close()

	// Encode params
	var rawParams json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal params: %w", err)
		}
		rawParams = json.RawMessage(data)
	}

	// Send request
	req := Request{
		Method: method,
		Params: rawParams,
		ID:     1,
	}

	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(req); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	// Read response
	decoder := json.NewDecoder(conn)
	var resp Response
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Check for RPC error
	if resp.Error != "" {
		return nil, fmt.Errorf("rpc error: %s", resp.Error)
	}

	return resp.Result, nil
}

// CallResult sends a method call to the server and unmarshals the result.
func (c *Client) CallResult(method string, params any, result any) error {
	rawResult, err := c.Call(method, params)
	if err != nil {
		return err
	}

	if rawResult == nil {
		return nil
	}

	if err := json.Unmarshal(rawResult, result); err != nil {
		return fmt.Errorf("failed to unmarshal result: %w", err)
	}

	return nil
}

// Registered RPC methods documentation:
//
// reload
//   - Reloads the server configuration from disk.
//   - Params: none
//   - Returns: {"message": "config reloaded"}
//
// status
//   - Returns the current server status.
//   - Params: none
//   - Returns: {"uptime": seconds, "leases": count, "blocked_domains": count}
//
// leases
//   - Returns all active DHCP leases.
//   - Params: none
//   - Returns: [{"ip": "192.168.1.10", "mac": "aa:bb:cc:dd:ee:ff", "hostname": "myhost", "expires": unix_timestamp}]
//
// static.add
//   - Adds a static DHCP reservation.
//   - Params: {"mac": "aa:bb:cc:dd:ee:ff", "ip": "192.168.1.100", "hostname": "reserved-host"}
//   - Returns: {"message": "static reservation added"}
//
// static.remove
//   - Removes a static DHCP reservation.
//   - Params: {"mac": "aa:bb:cc:dd:ee:ff"}
//   - Returns: {"message": "static reservation removed"}
//
// blocklist.reload
//   - Reloads all DNS blocklists from disk.
//   - Params: none
//   - Returns: {"message": "blocklists reloaded", "count": number_of_domains}
//
// import.hosts
//   - Imports a hosts file as a blocklist.
//   - Params: {"path": "/path/to/hosts/file"}
//   - Returns: {"message": "hosts file imported", "entries": count}
//
// import.bind
//   - Imports a BIND zone file.
//   - Params: {"path": "/path/to/zone/file", "zone": "example.com"}
//   - Returns: {"message": "bind zone imported", "entries": count}
