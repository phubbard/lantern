package blocker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestSubscriptionManagerFetchAndLoad(t *testing.T) {
	// Create a test HTTP server serving a blocklist
	blocklist := "0.0.0.0 ads.example.com\n0.0.0.0 tracker.example.com\n# comment\n\n0.0.0.0 malware.example.com\n"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"test-etag-123"`)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(blocklist))
	}))
	defer ts.Close()

	// Create temp cache dir
	cacheDir, err := os.MkdirTemp("", "lantern-sub-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cacheDir)

	b := New(nil)
	sm := NewSubscriptionManager(b, cacheDir, nil)

	// Add subscription and perform initial fetch
	if err := sm.Add(ts.URL+"/blocklist.txt", 1*time.Hour); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Verify domains were loaded
	if !b.IsBlocked("ads.example.com") {
		t.Error("expected ads.example.com to be blocked")
	}
	if !b.IsBlocked("tracker.example.com") {
		t.Error("expected tracker.example.com to be blocked")
	}
	if !b.IsBlocked("malware.example.com") {
		t.Error("expected malware.example.com to be blocked")
	}
	if b.IsBlocked("safe.example.com") {
		t.Error("expected safe.example.com to NOT be blocked")
	}

	// Check status
	status := sm.Status()
	if len(status) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(status))
	}
	if status[0].LastUpdated.IsZero() {
		t.Error("expected LastUpdated to be set")
	}
}

func TestSubscriptionConditionalFetch(t *testing.T) {
	fetchCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount++
		if r.Header.Get("If-None-Match") == `"test-etag"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"test-etag"`)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("0.0.0.0 test.com\n"))
	}))
	defer ts.Close()

	cacheDir, _ := os.MkdirTemp("", "lantern-sub-cond-*")
	defer os.RemoveAll(cacheDir)

	b := New(nil)
	sm := NewSubscriptionManager(b, cacheDir, nil)

	// First fetch - should download
	sm.Add(ts.URL+"/list.txt", 1*time.Hour)
	if fetchCount != 1 {
		t.Fatalf("expected 1 fetch, got %d", fetchCount)
	}

	// Manual update - should get 304 Not Modified
	sm.UpdateNow()
	if fetchCount != 2 {
		t.Fatalf("expected 2 fetches, got %d", fetchCount)
	}
}

func TestSubscriptionHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	cacheDir, _ := os.MkdirTemp("", "lantern-sub-err-*")
	defer os.RemoveAll(cacheDir)

	b := New(nil)
	sm := NewSubscriptionManager(b, cacheDir, nil)

	err := sm.Add(ts.URL+"/bad.txt", 1*time.Hour)
	if err == nil {
		t.Error("expected error for HTTP 500")
	}
}

func TestSubscriptionStartStop(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("0.0.0.0 test.com\n"))
	}))
	defer ts.Close()

	cacheDir, _ := os.MkdirTemp("", "lantern-sub-startstop-*")
	defer os.RemoveAll(cacheDir)

	b := New(nil)
	sm := NewSubscriptionManager(b, cacheDir, nil)
	sm.Add(ts.URL+"/list.txt", 1*time.Hour)

	// Start and stop should not panic
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sm.Start(ctx)
	sm.Stop()
}
