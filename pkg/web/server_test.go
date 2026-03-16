package web

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/phubbard/lantern/pkg/blocker"
	"github.com/phubbard/lantern/pkg/config"
	"github.com/phubbard/lantern/pkg/events"
	"github.com/phubbard/lantern/pkg/metrics"
	"github.com/phubbard/lantern/pkg/model"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newTestServer() *Server {
	cfg := &config.Config{
		Domain: "test.local",
		Web:    config.WebConfig{Enabled: true, Listen: "127.0.0.1:0"},
	}

	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	pool := model.NewLeasePool(
		*subnet,
		net.ParseIP("192.168.1.100"),
		net.ParseIP("192.168.1.200"),
		"test.local",
		5*time.Minute,
		24*time.Hour,
	)

	m := metrics.NewCollector()
	e := events.NewStore(100)
	b := blocker.New(testLogger())

	return New(cfg, pool, m, e, b, testLogger())
}

func TestNew(t *testing.T) {
	s := newTestServer()
	if s == nil {
		t.Fatal("expected non-nil server")
	}
	if s.httpSrv == nil {
		t.Fatal("expected non-nil http server")
	}
}

func TestHandleDashboard(t *testing.T) {
	s := newTestServer()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	s.handleDashboard(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("expected text/html content type, got %s", contentType)
	}
}

func TestHandleAPIMetrics(t *testing.T) {
	s := newTestServer()

	// Increment some metrics
	s.metrics.IncQueriesTotal()
	s.metrics.IncQueriesBlocked()

	req := httptest.NewRequest("GET", "/api/metrics", nil)
	w := httptest.NewRecorder()

	s.handleAPIMetrics(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var snap metrics.MetricsSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if snap.QueriesTotal != 1 {
		t.Errorf("expected QueriesTotal=1, got %d", snap.QueriesTotal)
	}
	if snap.QueriesBlocked != 1 {
		t.Errorf("expected QueriesBlocked=1, got %d", snap.QueriesBlocked)
	}
}

func TestHandleAPILeases(t *testing.T) {
	s := newTestServer()

	// Add a lease
	lease := &model.Lease{
		MAC:       net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		IP:        net.ParseIP("192.168.1.100"),
		Hostname:  "testhost",
		DNSName:   "testhost.test.local",
		TTL:       5 * time.Minute,
		GrantedAt: time.Now(),
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	s.pool.SetLease(lease)

	req := httptest.NewRequest("GET", "/api/leases", nil)
	w := httptest.NewRecorder()

	s.handleAPILeases(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("expected application/json, got %s", contentType)
	}
}

func TestHandleAPILeases_Empty(t *testing.T) {
	s := newTestServer()

	req := httptest.NewRequest("GET", "/api/leases", nil)
	w := httptest.NewRecorder()

	s.handleAPILeases(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestHandleLeases(t *testing.T) {
	s := newTestServer()

	req := httptest.NewRequest("GET", "/leases", nil)
	w := httptest.NewRecorder()

	s.handleLeases(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestHandleDNS(t *testing.T) {
	s := newTestServer()

	req := httptest.NewRequest("GET", "/dns", nil)
	w := httptest.NewRecorder()

	s.handleDNS(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestHandleBlocklist(t *testing.T) {
	s := newTestServer()

	req := httptest.NewRequest("GET", "/blocklist", nil)
	w := httptest.NewRecorder()

	s.handleBlocklist(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestHandleMetricsPage(t *testing.T) {
	s := newTestServer()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	s.handleMetricsPage(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestHandleReload(t *testing.T) {
	s := newTestServer()

	req := httptest.NewRequest("POST", "/api/reload", nil)
	w := httptest.NewRecorder()

	s.handleReload(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestHandleAddStatic(t *testing.T) {
	s := newTestServer()

	body := `{"mac":"aa:bb:cc:dd:ee:ff","ip":"192.168.1.50","hostname":"myhost"}`
	req := httptest.NewRequest("POST", "/api/static", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleAddStatic(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestHandleAddStatic_BadRequest(t *testing.T) {
	s := newTestServer()

	req := httptest.NewRequest("POST", "/api/static", strings.NewReader("invalid json"))
	w := httptest.NewRecorder()

	s.handleAddStatic(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleDeleteStatic(t *testing.T) {
	s := newTestServer()

	req := httptest.NewRequest("DELETE", "/api/static/aa:bb:cc:dd:ee:ff", nil)
	req.SetPathValue("mac", "aa:bb:cc:dd:ee:ff")
	w := httptest.NewRecorder()

	s.handleDeleteStatic(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestStop_NilServer(t *testing.T) {
	s := &Server{}
	err := s.Stop()
	if err != nil {
		t.Errorf("expected nil error for nil httpSrv, got %v", err)
	}
}
