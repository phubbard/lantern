package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/phubbard/lantern/pkg/blocker"
	"github.com/phubbard/lantern/pkg/config"
	"github.com/phubbard/lantern/pkg/events"
	"github.com/phubbard/lantern/pkg/metrics"
	"github.com/phubbard/lantern/pkg/model"
)

// Server is the HTTP server for the Lantern dashboard.
type Server struct {
	cfg           *config.Config
	pool          *model.LeasePool
	metrics       *metrics.Collector
	events        *events.Store
	blocker       *blocker.Blocker
	subscriptions *blocker.SubscriptionManager
	httpSrv       *http.Server
	logger        *slog.Logger
}

// New creates a new Server instance.
func New(cfg *config.Config, pool *model.LeasePool, m *metrics.Collector, e *events.Store, b *blocker.Blocker, subMgr *blocker.SubscriptionManager, logger *slog.Logger) *Server {
	s := &Server{
		cfg:           cfg,
		pool:          pool,
		metrics:       m,
		events:        e,
		blocker:       b,
		subscriptions: subMgr,
		logger:        logger,
	}

	mux := http.NewServeMux()

	// HTML routes
	mux.HandleFunc("GET /", s.handleDashboard)
	mux.HandleFunc("GET /leases", s.handleLeases)
	mux.HandleFunc("GET /leases/{mac}", s.handleLeaseDetail)
	mux.HandleFunc("GET /dns", s.handleDNS)
	mux.HandleFunc("GET /blocklist", s.handleBlocklist)
	mux.HandleFunc("GET /metrics", s.handleMetricsPage)

	// Health check
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	// API routes
	mux.HandleFunc("GET /api/metrics", s.handleAPIMetrics)
	mux.HandleFunc("GET /api/leases", s.handleAPILeases)
	mux.HandleFunc("GET /api/events/stream", s.handleEventStream)
	mux.HandleFunc("GET /api/blocker/status", s.handleBlockerStatus)
	mux.HandleFunc("POST /api/blocker/pause", s.handleBlockerPause)
	mux.HandleFunc("POST /api/blocker/resume", s.handleBlockerResume)
	mux.HandleFunc("GET /api/subscriptions", s.handleSubscriptionStatus)
	mux.HandleFunc("POST /api/subscriptions/update", s.handleSubscriptionUpdate)
	mux.HandleFunc("GET /api/events/{mac}", s.handleAPIEventsByMAC)
	mux.HandleFunc("DELETE /api/leases/{mac}", s.handleAPIReleaseLease)
	mux.HandleFunc("POST /api/reload", s.handleReload)
	mux.HandleFunc("POST /api/static", s.handleAddStatic)
	mux.HandleFunc("DELETE /api/static/{mac}", s.handleDeleteStatic)

	s.httpSrv = &http.Server{
		Addr:    cfg.Web.Listen,
		Handler: mux,
	}

	return s
}

// Start starts the HTTP server.
func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("starting web server", "addr", s.httpSrv.Addr)
	go func() {
		if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("server error", "err", err)
		}
	}()
	return nil
}

// Stop stops the HTTP server gracefully.
func (s *Server) Stop() error {
	if s.httpSrv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.httpSrv.Shutdown(ctx)
}

// handleDashboard serves the main dashboard page.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	page := baseLayout(
		"Dashboard",
		dashboardContent(),
	)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := page.ExecuteTemplate(w, "base", nil); err != nil {
		s.logger.Error("template error", "err", err)
	}
}

// handleLeases serves the leases table page.
func (s *Server) handleLeases(w http.ResponseWriter, r *http.Request) {
	leases := s.pool.GetAllLeases()
	page := baseLayout(
		"Leases",
		leasesContent(),
	)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := page.ExecuteTemplate(w, "base", map[string]interface{}{
		"Leases": leases,
	}); err != nil {
		s.logger.Error("template error", "err", err)
	}
}

// handleLeaseDetail serves the lease detail page for a specific MAC address.
func (s *Server) handleLeaseDetail(w http.ResponseWriter, r *http.Request) {
	macStr := r.PathValue("mac")
	parsedMAC, err := net.ParseMAC(macStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lease := s.pool.FindByMAC(parsedMAC)
	if lease == nil {
		http.NotFound(w, r)
		return
	}
	title := lease.Hostname
	if title == "" {
		title = lease.IP.String()
	}
	page := baseLayout(
		fmt.Sprintf("Host: %s", title),
		leaseDetailContent(),
	)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := page.ExecuteTemplate(w, "base", lease); err != nil {
		s.logger.Error("template error", "err", err)
	}
}

// handleDNS serves the DNS query log page.
func (s *Server) handleDNS(w http.ResponseWriter, r *http.Request) {
	page := baseLayout(
		"DNS Queries",
		dnsContent(),
	)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := page.ExecuteTemplate(w, "base", nil); err != nil {
		s.logger.Error("template error", "err", err)
	}
}

// handleBlocklist serves the blocklist management page.
func (s *Server) handleBlocklist(w http.ResponseWriter, r *http.Request) {
	page := baseLayout(
		"Blocklist Management",
		blocklistContent(),
	)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := page.ExecuteTemplate(w, "base", nil); err != nil {
		s.logger.Error("template error", "err", err)
	}
}

// handleMetricsPage serves the metrics visualization page.
func (s *Server) handleMetricsPage(w http.ResponseWriter, r *http.Request) {
	page := baseLayout(
		"Metrics",
		metricsContent(),
	)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := page.ExecuteTemplate(w, "base", nil); err != nil {
		s.logger.Error("template error", "err", err)
	}
}

// handleAPIMetrics returns current metrics as JSON.
func (s *Server) handleAPIMetrics(w http.ResponseWriter, r *http.Request) {
	snap := s.metrics.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snap)
}

// handleAPILeases returns all leases as JSON.
func (s *Server) handleAPILeases(w http.ResponseWriter, r *http.Request) {
	leases := s.pool.GetAllLeases()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(leases)
}

// handleEventStream handles Server-Sent Events streaming.
func (s *Server) handleEventStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Subscribe to events
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case evt := <-ch:
			data, err := json.Marshal(evt)
			if err != nil {
				s.logger.Error("marshal event error", "err", err)
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// handleBlockerStatus returns the current blocker state.
func (s *Server) handleBlockerStatus(w http.ResponseWriter, r *http.Request) {
	paused, remaining := s.blocker.IsPaused()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"paused":            paused,
		"remaining_seconds": int(remaining.Seconds()),
		"blocked_domains":   s.blocker.Count(),
	})
}

// handleBlockerPause pauses blocking for a given number of minutes.
func (s *Server) handleBlockerPause(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Minutes int `json:"minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.Minutes < 1 || req.Minutes > 60 {
		http.Error(w, "minutes must be between 1 and 60", http.StatusBadRequest)
		return
	}
	s.blocker.Pause(time.Duration(req.Minutes) * time.Minute)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "paused",
		"minutes": req.Minutes,
	})
}

// handleBlockerResume resumes blocking immediately.
func (s *Server) handleBlockerResume(w http.ResponseWriter, r *http.Request) {
	s.blocker.Resume()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "resumed"})
}

// handleSubscriptionStatus returns the current subscription states.
func (s *Server) handleSubscriptionStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.subscriptions == nil {
		json.NewEncoder(w).Encode([]struct{}{})
		return
	}
	json.NewEncoder(w).Encode(s.subscriptions.Status())
}

// handleSubscriptionUpdate triggers an immediate refresh of all subscriptions.
func (s *Server) handleSubscriptionUpdate(w http.ResponseWriter, r *http.Request) {
	if s.subscriptions == nil {
		http.Error(w, "subscriptions not configured", http.StatusNotFound)
		return
	}
	s.subscriptions.UpdateNow()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

// handleAPIEventsByMAC returns events for a specific MAC address as JSON.
func (s *Server) handleAPIEventsByMAC(w http.ResponseWriter, r *http.Request) {
	macStr := r.PathValue("mac")
	evts := s.events.GetByMAC(macStr)
	w.Header().Set("Content-Type", "application/json")
	if evts == nil {
		evts = []model.HostEvent{}
	}
	json.NewEncoder(w).Encode(evts)
}

// handleAPIReleaseLease deletes a lease so the client gets NAK'd on next renewal.
func (s *Server) handleAPIReleaseLease(w http.ResponseWriter, r *http.Request) {
	macStr := r.PathValue("mac")
	parsedMAC, err := net.ParseMAC(macStr)
	if err != nil {
		http.Error(w, "invalid MAC address", http.StatusBadRequest)
		return
	}
	if s.pool == nil {
		http.Error(w, "DHCP not enabled", http.StatusNotFound)
		return
	}
	if err := s.pool.ReleaseLease(parsedMAC); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	s.logger.Info("lease released via web UI", "mac", macStr)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "released", "mac": macStr})
}

// handleReload triggers a configuration reload.
func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	// TODO: implement config reload logic
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "reloaded"})
}

// handleAddStatic adds a static host mapping.
func (s *Server) handleAddStatic(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MAC      string `json:"mac"`
		Hostname string `json:"hostname"`
		IP       string `json:"ip"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	// TODO: implement add static host logic
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "added"})
}

// handleDeleteStatic removes a static host mapping.
func (s *Server) handleDeleteStatic(w http.ResponseWriter, r *http.Request) {
	mac := r.PathValue("mac")
	// TODO: implement delete static host logic
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "mac": mac})
}

// baseLayout returns the base HTML layout template.
func baseLayout(title string, content *template.Template) *template.Template {
	baseHTML := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{ PageTitle }} - Lantern</title>
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }

        body {
            background-color: #1e1e1e;
            color: #e0e0e0;
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            line-height: 1.6;
        }

        nav {
            background-color: #2d2d2d;
            border-bottom: 1px solid #3d3d3d;
            padding: 1rem 2rem;
            display: flex;
            gap: 2rem;
            align-items: center;
        }

        nav a {
            color: #4a9eff;
            text-decoration: none;
            font-weight: 500;
            transition: color 0.2s;
        }

        nav a:hover {
            color: #7bb6ff;
        }

        nav .logo {
            font-size: 1.5rem;
            font-weight: 700;
            color: #4a9eff;
            margin-right: auto;
        }

        .container {
            max-width: 1200px;
            margin: 0 auto;
            padding: 2rem;
        }

        h1 {
            color: #4a9eff;
            margin-bottom: 1.5rem;
            font-size: 2rem;
        }

        .card {
            background-color: #2d2d2d;
            border: 1px solid #3d3d3d;
            border-radius: 8px;
            padding: 1.5rem;
            margin-bottom: 1.5rem;
        }

        .card h2 {
            color: #4a9eff;
            margin-bottom: 1rem;
            font-size: 1.2rem;
        }

        .grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
            gap: 1.5rem;
            margin-bottom: 1.5rem;
        }

        .stat {
            background-color: #2d2d2d;
            border: 1px solid #3d3d3d;
            border-radius: 8px;
            padding: 1.5rem;
            text-align: center;
        }

        .stat-value {
            font-size: 2rem;
            font-weight: 700;
            color: #4a9eff;
            margin-bottom: 0.5rem;
        }

        .stat-label {
            color: #999;
            font-size: 0.9rem;
        }

        table {
            width: 100%;
            border-collapse: collapse;
            margin-top: 1rem;
        }

        th {
            background-color: #1e1e1e;
            color: #4a9eff;
            padding: 0.75rem;
            text-align: left;
            border-bottom: 2px solid #3d3d3d;
        }

        td {
            padding: 0.75rem;
            border-bottom: 1px solid #3d3d3d;
        }

        tr:hover {
            background-color: #353535;
        }

        .btn {
            background-color: #4a9eff;
            color: #1e1e1e;
            border: none;
            padding: 0.5rem 1rem;
            border-radius: 4px;
            cursor: pointer;
            font-weight: 500;
            transition: background-color 0.2s;
        }

        .btn:hover {
            background-color: #7bb6ff;
        }

        .btn-small {
            padding: 0.25rem 0.75rem;
            font-size: 0.85rem;
        }
    </style>
</head>
<body>
    <nav>
        <div class="logo">Lantern</div>
        <a href="/">Dashboard</a>
        <a href="/leases">Leases</a>
        <a href="/dns">DNS</a>
        <a href="/blocklist">Blocklist</a>
        <a href="/metrics">Metrics</a>
    </nav>

    <div class="container">
        <h1>{{ PageTitle }}</h1>
        {{ template "content" . }}
    </div>

    {{ template "scripts" . }}
</body>
</html>`

	base := template.New("base").Funcs(template.FuncMap{
		"PageTitle": func() string { return title },
		"mul":       func(a, b float64) float64 { return a * b },
	})

	base, err := base.Parse(baseHTML)
	if err != nil {
		panic(err)
	}

	// Add empty default "scripts" block (pages can override it)
	base, err = base.Parse(`{{define "scripts"}}{{end}}`)
	if err != nil {
		panic(err)
	}

	// The content template uses {{define "content"}}...{{end}}, so
	// we need to look up the defined "content" template, not the outer one.
	ct := content.Lookup("content")
	if ct == nil {
		panic("content template must define a {{define \"content\"}} block")
	}
	base, err = base.AddParseTree("content", ct.Tree)
	if err != nil {
		panic(err)
	}

	// If the content template defines a "scripts" block, override the default
	st := content.Lookup("scripts")
	if st != nil {
		base, err = base.AddParseTree("scripts", st.Tree)
		if err != nil {
			panic(err)
		}
	}

	return base
}

// dashboardContent returns the dashboard content template.
func dashboardContent() *template.Template {
	html := `{{define "content"}}
<div class="grid">
    <div class="stat">
        <div class="stat-value">0</div>
        <div class="stat-label">Total Queries</div>
    </div>
    <div class="stat">
        <div class="stat-value">0</div>
        <div class="stat-label">Blocked Queries</div>
    </div>
    <div class="stat">
        <div class="stat-value">0ms</div>
        <div class="stat-label">Avg Latency</div>
    </div>
    <div class="stat">
        <div class="stat-value">0</div>
        <div class="stat-label">Active Leases</div>
    </div>
</div>

<div class="card" id="blocker-card">
    <h2>Ad Blocking</h2>
    <div style="display: flex; align-items: center; gap: 1rem; flex-wrap: wrap;">
        <span id="blocker-status" style="font-weight: 600;"></span>
        <span id="blocker-countdown" style="color: #f59e0b;"></span>
        <div style="display: flex; gap: 0.5rem;">
            <button class="btn btn-small" onclick="pauseBlocking(1)">Pause 1m</button>
            <button class="btn btn-small" onclick="pauseBlocking(5)">Pause 5m</button>
            <button class="btn btn-small" onclick="pauseBlocking(10)">Pause 10m</button>
            <button class="btn btn-small" id="resume-btn" onclick="resumeBlocking()" style="display:none; background-color:#22c55e;">Resume</button>
        </div>
    </div>
</div>

<div class="card">
    <h2>Query Rate (24h)</h2>
    <canvas id="queryChart" style="height: 300px;"></canvas>
</div>

<div class="card">
    <h2>Top Clients</h2>
    <table>
        <thead>
            <tr>
                <th>Hostname</th>
                <th>IP Address</th>
                <th>Queries</th>
            </tr>
        </thead>
        <tbody>
            <tr>
                <td colspan="3" style="text-align: center; color: #999;">Loading...</td>
            </tr>
        </tbody>
    </table>
</div>

<script>
    async function pauseBlocking(minutes) {
        await fetch('/api/blocker/pause', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({minutes: minutes})
        });
        updateBlockerStatus();
    }

    async function resumeBlocking() {
        await fetch('/api/blocker/resume', {method: 'POST'});
        updateBlockerStatus();
    }

    async function updateBlockerStatus() {
        const resp = await fetch('/api/blocker/status');
        const data = await resp.json();
        const status = document.getElementById('blocker-status');
        const countdown = document.getElementById('blocker-countdown');
        const resumeBtn = document.getElementById('resume-btn');

        if (data.paused) {
            const mins = Math.floor(data.remaining_seconds / 60);
            const secs = data.remaining_seconds % 60;
            status.textContent = 'PAUSED';
            status.style.color = '#f59e0b';
            countdown.textContent = mins + 'm ' + secs + 's remaining';
            resumeBtn.style.display = 'inline-block';
        } else {
            status.textContent = 'ACTIVE — ' + data.blocked_domains.toLocaleString() + ' domains blocked';
            status.style.color = '#22c55e';
            countdown.textContent = '';
            resumeBtn.style.display = 'none';
        }
    }

    updateBlockerStatus();
    setInterval(updateBlockerStatus, 5000);
</script>
{{end}}
{{define "scripts"}}<script async src="https://cdn.jsdelivr.net/npm/chart.js"></script>{{end}}`

	t, err := template.New("dashboardContent").Parse(html)
	if err != nil {
		panic(err)
	}
	return t
}

// leasesContent returns the leases table content template.
func leasesContent() *template.Template {
	html := `{{define "content"}}
<div class="card">
    <h2>DHCP Leases</h2>
    <table>
        <thead>
            <tr>
                <th>Hostname</th>
                <th>MAC Address</th>
                <th>IP Address</th>
                <th>Expires</th>
                <th>Actions</th>
            </tr>
        </thead>
        <tbody>
            {{if .Leases}}
                {{range .Leases}}
                <tr>
                    <td><a href="/leases/{{.MAC}}" style="color: #4a9eff;">{{.Hostname}}</a></td>
                    <td>{{.MAC}}</td>
                    <td>{{.IP}}</td>
                    <td>{{.ExpiresAt.Format "2006-01-02 15:04:05"}}</td>
                    <td><a href="/leases/{{.MAC}}" class="btn btn-small" style="text-decoration:none;">Details</a></td>
                </tr>
                {{end}}
            {{else}}
                <tr>
                    <td colspan="5" style="text-align: center; color: #999;">No leases</td>
                </tr>
            {{end}}
        </tbody>
    </table>
</div>
{{end}}`

	t, err := template.New("leasesContent").Parse(html)
	if err != nil {
		panic(err)
	}
	return t
}

// leaseDetailContent returns the lease detail content template.
func leaseDetailContent() *template.Template {
	html := `{{define "content"}}
<style>
    .detail-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 1.5rem; margin-bottom: 1.5rem; }
    .detail-row { display: flex; padding: 0.5rem 0; border-bottom: 1px solid #3d3d3d; }
    .detail-label { color: #999; min-width: 140px; font-size: 0.9rem; }
    .detail-value { color: #e0e0e0; font-family: monospace; }
    .badge { display: inline-block; padding: 0.15rem 0.5rem; border-radius: 3px; font-size: 0.8rem; font-weight: 600; }
    .badge-static { background: #22c55e33; color: #22c55e; }
    .badge-dynamic { background: #4a9eff33; color: #4a9eff; }
    .badge-dhcp { background: #8b5cf633; color: #a78bfa; }
    .badge-dns { background: #06b6d433; color: #22d3ee; }
    .badge-other { background: #64748b33; color: #94a3b8; }
    .tab-bar { display: flex; gap: 0; border-bottom: 2px solid #3d3d3d; margin-bottom: 1rem; }
    .tab { padding: 0.5rem 1.25rem; cursor: pointer; color: #999; border-bottom: 2px solid transparent; margin-bottom: -2px; }
    .tab:hover { color: #e0e0e0; }
    .tab.active { color: #4a9eff; border-bottom-color: #4a9eff; }
    .tab-panel { display: none; }
    .tab-panel.active { display: block; }
    .pager { display: flex; gap: 0.5rem; align-items: center; margin-top: 1rem; justify-content: center; }
    .pager button { background: #3d3d3d; color: #e0e0e0; border: none; padding: 0.4rem 0.8rem; border-radius: 4px; cursor: pointer; }
    .pager button:hover { background: #4a9eff; }
    .pager button:disabled { opacity: 0.3; cursor: default; }
    .pager button:disabled:hover { background: #3d3d3d; }
    .dns-stat { text-align: center; }
    .dns-stat-value { font-size: 1.5rem; font-weight: 700; color: #4a9eff; }
    .dns-stat-label { font-size: 0.8rem; color: #999; }
</style>

<div class="detail-grid">
    <div class="card">
        <h2>Host Information</h2>
        <div class="detail-row">
            <span class="detail-label">Hostname</span>
            <span class="detail-value">{{.Hostname}}{{if not .Hostname}}<span style="color:#666">unknown</span>{{end}}</span>
        </div>
        <div class="detail-row">
            <span class="detail-label">DNS Name</span>
            <span class="detail-value">{{.DNSName}}{{if not .DNSName}}<span style="color:#666">none</span>{{end}}</span>
        </div>
        <div class="detail-row">
            <span class="detail-label">MAC Address</span>
            <span class="detail-value">{{.MAC}}</span>
        </div>
        <div class="detail-row">
            <span class="detail-label">IP Address</span>
            <span class="detail-value">{{.IP}}</span>
        </div>
        <div class="detail-row">
            <span class="detail-label">Type</span>
            <span class="detail-value">{{if .Static}}<span class="badge badge-static">Static</span>{{else}}<span class="badge badge-dynamic">Dynamic</span>{{end}}</span>
        </div>
        <div class="detail-row">
            <span class="detail-label">Client ID</span>
            <span class="detail-value">{{.ClientID}}{{if not .ClientID}}<span style="color:#666">none</span>{{end}}</span>
        </div>
    </div>

    <div class="card">
        <h2>Lease Timing</h2>
        <div class="detail-row">
            <span class="detail-label">Granted At</span>
            <span class="detail-value">{{.GrantedAt.Format "2006-01-02 15:04:05"}}</span>
        </div>
        <div class="detail-row">
            <span class="detail-label">Expires At</span>
            <span class="detail-value" id="expires-at" data-expires="{{.ExpiresAt.Format "2006-01-02T15:04:05Z07:00"}}">{{.ExpiresAt.Format "2006-01-02 15:04:05"}}</span>
        </div>
        <div class="detail-row">
            <span class="detail-label">TTL</span>
            <span class="detail-value">{{.TTL}}</span>
        </div>
        <div class="detail-row">
            <span class="detail-label">Time Remaining</span>
            <span class="detail-value" id="time-remaining">calculating...</span>
        </div>
        {{if .Fingerprint}}
        <h2 style="margin-top: 1.5rem;">Device Fingerprint</h2>
        <div class="detail-row">
            <span class="detail-label">OS</span>
            <span class="detail-value">{{.Fingerprint.OS}} {{.Fingerprint.OSVersion}}</span>
        </div>
        <div class="detail-row">
            <span class="detail-label">Device Type</span>
            <span class="detail-value">{{.Fingerprint.DeviceType}}</span>
        </div>
        <div class="detail-row">
            <span class="detail-label">Confidence</span>
            <span class="detail-value">{{printf "%.0f" (mul .Fingerprint.Confidence 100)}}%</span>
        </div>
        <div class="detail-row">
            <span class="detail-label">First Seen</span>
            <span class="detail-value">{{.Fingerprint.FirstSeen.Format "2006-01-02 15:04:05"}}</span>
        </div>
        {{end}}
    </div>
</div>

{{if not .Static}}
<div class="card" id="release-card">
    <h2>Lease Actions</h2>
    <div style="display: flex; align-items: center; gap: 1rem; flex-wrap: wrap;">
        <button class="btn" onclick="releaseLease()" id="release-btn" style="background-color: #ef4444;">Release Lease</button>
        <span style="color: #999; font-size: 0.9rem;">
            Removes this lease from the server. The client will get a NAK on its next renewal
            and fall back to DHCPDISCOVER to get a new lease.
        </span>
        <span id="release-status" style="font-weight: 600;"></span>
    </div>
</div>
{{end}}

<div class="card">
    <div class="tab-bar">
        <div class="tab active" onclick="switchTab('all')">All Events</div>
        <div class="tab" onclick="switchTab('dhcp')">DHCP</div>
        <div class="tab" onclick="switchTab('dns')">DNS</div>
    </div>

    <div id="dns-summary" style="display:none; margin-bottom: 1rem;">
        <div class="grid" style="grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));">
            <div class="dns-stat"><div class="dns-stat-value" id="dns-total">0</div><div class="dns-stat-label">Total Queries</div></div>
            <div class="dns-stat"><div class="dns-stat-value" id="dns-blocked">0</div><div class="dns-stat-label">Blocked</div></div>
            <div class="dns-stat"><div class="dns-stat-value" id="dns-unique">0</div><div class="dns-stat-label">Unique Domains</div></div>
        </div>
    </div>

    <table>
        <thead>
            <tr>
                <th>Time</th>
                <th>Type</th>
                <th>Detail</th>
            </tr>
        </thead>
        <tbody id="events-body">
            <tr><td colspan="3" style="text-align:center; color:#999;">Loading events...</td></tr>
        </tbody>
    </table>
    <div class="pager">
        <button id="page-prev" onclick="changePage(-1)" disabled>&larr; Newer</button>
        <span id="page-info" style="color:#999; font-size:0.9rem;"></span>
        <button id="page-next" onclick="changePage(1)" disabled>Older &rarr;</button>
    </div>
</div>

<script>
    const MAC = "{{.MAC}}";
    const PAGE_SIZE = 25;
    let allEvents = [];
    let filteredEvents = [];
    let currentPage = 0;
    let currentFilter = 'all';

    function badgeClass(type) {
        if (type.startsWith('dhcp_')) return 'badge-dhcp';
        if (type === 'dns_query') return 'badge-dns';
        return 'badge-other';
    }

    function fmtTime(ts) {
        const d = new Date(ts);
        return d.toLocaleString();
    }

    function switchTab(filter) {
        currentFilter = filter;
        currentPage = 0;
        document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
        document.querySelector('.tab[onclick*="' + filter + '"]').classList.add('active');
        applyFilter();
        render();
    }

    function applyFilter() {
        if (currentFilter === 'all') {
            filteredEvents = allEvents;
        } else if (currentFilter === 'dhcp') {
            filteredEvents = allEvents.filter(e => e.type.startsWith('dhcp_'));
        } else if (currentFilter === 'dns') {
            filteredEvents = allEvents.filter(e => e.type === 'dns_query');
        }
        document.getElementById('dns-summary').style.display = currentFilter === 'dns' ? 'block' : 'none';
    }

    function changePage(delta) {
        currentPage += delta;
        render();
    }

    function render() {
        const totalPages = Math.max(1, Math.ceil(filteredEvents.length / PAGE_SIZE));
        currentPage = Math.max(0, Math.min(currentPage, totalPages - 1));

        const start = currentPage * PAGE_SIZE;
        const pageEvents = filteredEvents.slice(start, start + PAGE_SIZE);

        const tbody = document.getElementById('events-body');
        if (pageEvents.length === 0) {
            tbody.innerHTML = '<tr><td colspan="3" style="text-align:center; color:#999;">No events</td></tr>';
        } else {
            tbody.innerHTML = pageEvents.map(e =>
                '<tr>' +
                '<td style="white-space:nowrap;">' + fmtTime(e.timestamp) + '</td>' +
                '<td><span class="badge ' + badgeClass(e.type) + '">' + e.type.replace('_', ' ') + '</span></td>' +
                '<td style="word-break:break-all;">' + (e.detail || '') + '</td>' +
                '</tr>'
            ).join('');
        }

        document.getElementById('page-info').textContent =
            'Page ' + (currentPage + 1) + ' of ' + totalPages + ' (' + filteredEvents.length + ' events)';
        document.getElementById('page-prev').disabled = currentPage === 0;
        document.getElementById('page-next').disabled = currentPage >= totalPages - 1;
    }

    function computeDNSStats() {
        const dnsEvents = allEvents.filter(e => e.type === 'dns_query');
        const blocked = dnsEvents.filter(e => e.detail && e.detail.includes('blocked')).length;
        const domains = new Set(dnsEvents.map(e => {
            const m = e.detail && e.detail.match(/^(\S+)/);
            return m ? m[1] : '';
        }).filter(Boolean));
        document.getElementById('dns-total').textContent = dnsEvents.length;
        document.getElementById('dns-blocked').textContent = blocked;
        document.getElementById('dns-unique').textContent = domains.size;
    }

    function updateTimeRemaining() {
        const el = document.getElementById('expires-at');
        const exp = new Date(el.dataset.expires);
        const now = new Date();
        const diff = exp - now;
        const rem = document.getElementById('time-remaining');
        if (diff <= 0) {
            rem.textContent = 'Expired';
            rem.style.color = '#ef4444';
        } else {
            const h = Math.floor(diff / 3600000);
            const m = Math.floor((diff % 3600000) / 60000);
            const s = Math.floor((diff % 60000) / 1000);
            rem.textContent = (h > 0 ? h + 'h ' : '') + m + 'm ' + s + 's';
            rem.style.color = diff < 300000 ? '#f59e0b' : '#22c55e';
        }
    }

    async function releaseLease() {
        if (!confirm('Release this lease? The client will need to re-discover to get a new one.')) return;
        const btn = document.getElementById('release-btn');
        const status = document.getElementById('release-status');
        btn.disabled = true;
        btn.textContent = 'Releasing...';
        try {
            const resp = await fetch('/api/leases/' + encodeURIComponent(MAC), { method: 'DELETE' });
            if (resp.ok) {
                status.textContent = 'Lease released';
                status.style.color = '#22c55e';
                btn.textContent = 'Released';
                setTimeout(() => { window.location.href = '/leases'; }, 1500);
            } else {
                const err = await resp.text();
                status.textContent = 'Error: ' + err;
                status.style.color = '#ef4444';
                btn.disabled = false;
                btn.textContent = 'Release Lease';
            }
        } catch(e) {
            status.textContent = 'Network error';
            status.style.color = '#ef4444';
            btn.disabled = false;
            btn.textContent = 'Release Lease';
        }
    }

    async function loadEvents() {
        try {
            const resp = await fetch('/api/events/' + encodeURIComponent(MAC));
            const data = await resp.json();
            allEvents = data.sort((a, b) => new Date(b.timestamp) - new Date(a.timestamp));
            applyFilter();
            computeDNSStats();
            render();
        } catch(err) {
            document.getElementById('events-body').innerHTML =
                '<tr><td colspan="3" style="text-align:center; color:#ef4444;">Failed to load events</td></tr>';
        }
    }

    loadEvents();
    updateTimeRemaining();
    setInterval(updateTimeRemaining, 1000);
</script>
{{end}}`

	t, err := template.New("leaseDetailContent").Funcs(template.FuncMap{
		"mul": func(a, b float64) float64 { return a * b },
	}).Parse(html)
	if err != nil {
		panic(err)
	}
	return t
}

// dnsContent returns the DNS query log content template.
func dnsContent() *template.Template {
	html := `{{define "content"}}
<style>
    .dns-badge { display: inline-block; padding: 0.15rem 0.5rem; border-radius: 3px; font-size: 0.8rem; font-weight: 600; }
    .dns-blocked { background: #ef444433; color: #f87171; }
    .dns-local { background: #22c55e33; color: #22c55e; }
    .dns-cache, .dns-cached { background: #f59e0b33; color: #f59e0b; }
    .dns-upstream, .dns-doh, .dns-fallback { background: #4a9eff33; color: #4a9eff; }
    .dns-error { background: #ef444433; color: #ef4444; }
    .dns-controls { display: flex; gap: 1rem; align-items: center; margin-bottom: 1rem; flex-wrap: wrap; }
    .dns-counter { color: #999; font-size: 0.9rem; }
    .dns-counter b { color: #e0e0e0; }
    #dns-live-dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; background: #22c55e; margin-right: 0.5rem; animation: pulse 2s infinite; }
    @keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.3; } }
</style>

<div class="card">
    <h2>DNS Query Log</h2>
    <div class="dns-controls">
        <span><span id="dns-live-dot"></span>Live</span>
        <span class="dns-counter">Total: <b id="cnt-total">0</b></span>
        <span class="dns-counter">Blocked: <b id="cnt-blocked" style="color:#f87171;">0</b></span>
        <span class="dns-counter">Cached: <b id="cnt-cached" style="color:#f59e0b;">0</b></span>
        <span class="dns-counter">Upstream: <b id="cnt-upstream" style="color:#4a9eff;">0</b></span>
        <button class="btn btn-small" onclick="clearLog()">Clear</button>
        <label style="color:#999; font-size:0.9rem;"><input type="checkbox" id="auto-scroll" checked> Auto-scroll</label>
    </div>
    <div style="max-height: 600px; overflow-y: auto;" id="dns-scroll">
    <table>
        <thead>
            <tr>
                <th style="width:160px;">Timestamp</th>
                <th>Domain</th>
                <th style="width:60px;">Type</th>
                <th style="width:90px;">Source</th>
            </tr>
        </thead>
        <tbody id="dns-body">
            <tr id="dns-empty">
                <td colspan="4" style="text-align: center; color: #999;">Waiting for queries...</td>
            </tr>
        </tbody>
    </table>
    </div>
</div>

<script>
    const MAX_ROWS = 500;
    let total = 0, blocked = 0, cached = 0, upstream = 0;

    function parseDetail(detail) {
        const parts = {};
        (detail || '').split(' ').forEach(p => {
            const [k, ...v] = p.split('=');
            if (k && v.length) parts[k] = v.join('=');
        });
        return parts;
    }

    function sourceClass(src) {
        if (src === 'blocked') return 'dns-blocked';
        if (src === 'local') return 'dns-local';
        if (src === 'cache' || src === 'cached') return 'dns-cache';
        if (src === 'error') return 'dns-error';
        return 'dns-upstream';
    }

    function fmtTime(ts) {
        const d = new Date(ts);
        return d.toLocaleTimeString() + '.' + String(d.getMilliseconds()).padStart(3, '0');
    }

    function addRow(evt) {
        if (evt.type !== 'dns_query') return;

        const p = parseDetail(evt.detail);
        const domain = (p.name || '').replace(/\.$/, '');
        const qtype = p.type || '?';
        const source = p.source || 'unknown';

        total++;
        if (source === 'blocked') blocked++;
        else if (source === 'cache' || source === 'cached') cached++;
        else if (source !== 'local' && source !== 'error') upstream++;

        document.getElementById('cnt-total').textContent = total;
        document.getElementById('cnt-blocked').textContent = blocked;
        document.getElementById('cnt-cached').textContent = cached;
        document.getElementById('cnt-upstream').textContent = upstream;

        const empty = document.getElementById('dns-empty');
        if (empty) empty.remove();

        const tbody = document.getElementById('dns-body');
        const tr = document.createElement('tr');
        tr.innerHTML =
            '<td style="white-space:nowrap; font-size:0.85rem;">' + fmtTime(evt.timestamp) + '</td>' +
            '<td style="font-family:monospace; word-break:break-all;">' + domain + '</td>' +
            '<td>' + qtype + '</td>' +
            '<td><span class="dns-badge ' + sourceClass(source) + '">' + source + '</span></td>';

        // Insert at top (newest first)
        tbody.insertBefore(tr, tbody.firstChild);

        // Trim old rows
        while (tbody.children.length > MAX_ROWS) {
            tbody.removeChild(tbody.lastChild);
        }

        if (document.getElementById('auto-scroll').checked) {
            document.getElementById('dns-scroll').scrollTop = 0;
        }
    }

    function clearLog() {
        document.getElementById('dns-body').innerHTML =
            '<tr id="dns-empty"><td colspan="4" style="text-align:center; color:#999;">Waiting for queries...</td></tr>';
        total = blocked = cached = upstream = 0;
        document.getElementById('cnt-total').textContent = '0';
        document.getElementById('cnt-blocked').textContent = '0';
        document.getElementById('cnt-cached').textContent = '0';
        document.getElementById('cnt-upstream').textContent = '0';
    }

    // Load recent events first
    async function loadRecent() {
        try {
            const resp = await fetch('/api/events/stream', {
                headers: { 'Accept': 'text/event-stream' }
            });
        } catch(e) {}
    }

    const eventSource = new EventSource('/api/events/stream');
    eventSource.onmessage = function(event) {
        try {
            const evt = JSON.parse(event.data);
            addRow(evt);
        } catch(e) {
            console.error('Failed to parse event:', e);
        }
    };
    eventSource.onerror = function() {
        const dot = document.getElementById('dns-live-dot');
        if (dot) dot.style.background = '#ef4444';
    };
</script>
{{end}}`

	t, err := template.New("dnsContent").Parse(html)
	if err != nil {
		panic(err)
	}
	return t
}

// blocklistContent returns the blocklist management content template.
func blocklistContent() *template.Template {
	html := `{{define "content"}}
<div class="card">
    <h2>Blocklist Management</h2>
    <p style="margin-bottom: 1rem; color: #999;">Manage DNS blocklists for ad/malware filtering</p>

    <h3 style="color: #4a9eff; margin-top: 1.5rem; margin-bottom: 1rem;">Active Blocklists</h3>
    <table>
        <thead>
            <tr>
                <th>Name</th>
                <th>URL</th>
                <th>Rules Count</th>
                <th>Last Updated</th>
                <th>Actions</th>
            </tr>
        </thead>
        <tbody>
            <tr>
                <td colspan="5" style="text-align: center; color: #999;">No blocklists configured</td>
            </tr>
        </tbody>
    </table>
</div>
{{end}}`

	t, err := template.New("blocklistContent").Parse(html)
	if err != nil {
		panic(err)
	}
	return t
}

// metricsContent returns the metrics content template.
func metricsContent() *template.Template {
	html := `{{define "content"}}
<div class="card">
    <h2>Query Metrics</h2>
    <canvas id="metricsChart" style="height: 400px;"></canvas>
</div>

<div class="card">
    <h2>Block Rate</h2>
    <canvas id="blockChart" style="height: 300px;"></canvas>
</div>

<script>
    async function loadMetrics() {
        const response = await fetch('/api/metrics');
        const data = await response.json();
        console.log('Metrics:', data);
        // Initialize Chart.js charts with data
    }

    loadMetrics();
</script>
{{end}}
{{define "scripts"}}<script async src="https://cdn.jsdelivr.net/npm/chart.js"></script>{{end}}`

	t, err := template.New("metricsContent").Parse(html)
	if err != nil {
		panic(err)
	}
	return t
}
