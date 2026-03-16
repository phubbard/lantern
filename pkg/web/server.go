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
	page := baseLayout(
		fmt.Sprintf("Host: %s", lease.Hostname),
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

    <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
</body>
</html>`

	base := template.New("base").Funcs(template.FuncMap{
		"PageTitle": func() string { return title },
	})

	base, err := base.Parse(baseHTML)
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
{{end}}`

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
                    <td><button class="btn btn-small">Details</button></td>
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
<div class="card">
    <h2>{{.Hostname}}</h2>
    <table style="width: auto;">
        <tr>
            <td style="font-weight: bold; padding-right: 2rem;">MAC Address:</td>
            <td>{{.MAC}}</td>
        </tr>
        <tr>
            <td style="font-weight: bold; padding-right: 2rem;">IP Address:</td>
            <td>{{.IP}}</td>
        </tr>
        <tr>
            <td style="font-weight: bold; padding-right: 2rem;">Leased At:</td>
            <td>{{.GrantedAt.Format "2006-01-02 15:04:05"}}</td>
        </tr>
        <tr>
            <td style="font-weight: bold; padding-right: 2rem;">Expires At:</td>
            <td>{{.ExpiresAt.Format "2006-01-02 15:04:05"}}</td>
        </tr>
    </table>
</div>
{{end}}`

	t, err := template.New("leaseDetailContent").Parse(html)
	if err != nil {
		panic(err)
	}
	return t
}

// dnsContent returns the DNS query log content template.
func dnsContent() *template.Template {
	html := `{{define "content"}}
<div class="card">
    <h2>DNS Query Log</h2>
    <table>
        <thead>
            <tr>
                <th>Timestamp</th>
                <th>Client IP</th>
                <th>Domain</th>
                <th>Type</th>
                <th>Status</th>
                <th>Latency</th>
            </tr>
        </thead>
        <tbody>
            <tr>
                <td colspan="6" style="text-align: center; color: #999;">Loading...</td>
            </tr>
        </tbody>
    </table>
</div>

<script>
    const eventSource = new EventSource('/api/events/stream');
    eventSource.onmessage = function(event) {
        console.log('Event:', JSON.parse(event.data));
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
{{end}}`

	t, err := template.New("metricsContent").Parse(html)
	if err != nil {
		panic(err)
	}
	return t
}
