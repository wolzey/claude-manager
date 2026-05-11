package dashboard

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wolzey/claude-manager/internal/store"
)

// Server wraps the embedded HTTP server. One process can own one server.
type Server struct {
	watcher *Watcher
	assets  fs.FS
}

func NewServer(watcher *Watcher, assets fs.FS) *Server {
	return &Server{watcher: watcher, assets: assets}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/events", s.writeSSE)
	mux.HandleFunc("/api/projects", s.handleProjects)
	mux.HandleFunc("/api/projects/", s.handleProjectSub) // /:slug, /:slug/workers/:name, /:slug/contract, /:slug/inbox
	mux.Handle("/", s.handleStatic())
	return mux
}

// ───────────────────────────── routes ─────────────────────────────

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleStatic() http.Handler {
	return http.FileServer(http.FS(s.assets))
}

type projectSummary struct {
	Slug           string     `json:"slug"`
	Name           string     `json:"name"`
	Description    string     `json:"description,omitempty"`
	WorkerCount    int        `json:"worker_count"`
	Idle           int        `json:"idle"`
	Running        int        `json:"running"`
	Error          int        `json:"error"`
	Locked         int        `json:"locked"`
	InboxPending   int        `json:"inbox_pending"`
	LastActivityAt *time.Time `json:"last_activity_at,omitempty"`
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	cors(w)
	projects, err := store.ListProjects()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]projectSummary, 0, len(projects))
	for _, p := range projects {
		out = append(out, summarize(p))
	}
	writeJSON(w, out)
}

func (s *Server) handleProjectSub(w http.ResponseWriter, r *http.Request) {
	cors(w)
	rest := strings.TrimPrefix(r.URL.Path, "/api/projects/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(rest, "/")
	slug := parts[0]
	if err := store.RequireProject(slug); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}

	switch {
	case len(parts) == 1:
		s.writeProjectDetail(w, slug)
	case len(parts) == 2 && parts[1] == "contract":
		s.writeContract(w, slug)
	case len(parts) == 2 && parts[1] == "inbox":
		s.writeInbox(w, slug)
	case len(parts) == 3 && parts[1] == "workers":
		s.writeWorker(w, r, slug, parts[2])
	case len(parts) == 4 && parts[1] == "workers" && parts[3] == "log":
		s.writeWorkerLog(w, r, slug, parts[2])
	default:
		http.NotFound(w, r)
	}
}

// ───────────────────────────── handlers ─────────────────────────────

func (s *Server) writeProjectDetail(w http.ResponseWriter, slug string) {
	p, err := store.LoadProject(slug)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	workers, _ := store.ListWorkers(slug)
	out := struct {
		Project          *store.Project   `json:"project"`
		Workers          []workerSummary  `json:"workers"`
		InboxPending     int              `json:"inbox_pending"`
		ContractPreview  string           `json:"contract_preview,omitempty"`
	}{
		Project: p,
	}
	for _, wk := range workers {
		out.Workers = append(out.Workers, summarizeWorker(slug, wk))
	}
	out.InboxPending = countInbox(slug)
	if b, err := os.ReadFile(store.ContractFile(slug)); err == nil {
		if len(b) > 400 {
			b = b[:400]
		}
		out.ContractPreview = string(b)
	}
	writeJSON(w, out)
}

func (s *Server) writeContract(w http.ResponseWriter, slug string) {
	path := store.ContractFile(slug)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, map[string]string{"path": path, "content": ""})
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]string{"path": path, "content": string(b)})
}

func (s *Server) writeInbox(w http.ResponseWriter, slug string) {
	events := readInboxEvents(slug)
	writeJSON(w, events)
}

func (s *Server) writeWorker(w http.ResponseWriter, r *http.Request, slug, name string) {
	worker, err := store.LoadWorker(slug, name)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	wk := summarizeWorker(slug, worker)
	last, _ := os.ReadFile(store.WorkerLastFile(slug, name))
	writeJSON(w, map[string]any{
		"worker":      wk,
		"last_result": string(last),
	})
}

func (s *Server) writeWorkerLog(w http.ResponseWriter, r *http.Request, slug, name string) {
	lines := 200
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 5000 {
			lines = n
		}
	}
	raw := r.URL.Query().Get("raw") == "1"
	tail, err := tailFile(store.WorkerLogFile(slug, name), lines)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if raw {
		writeJSON(w, map[string]any{"lines": tail})
		return
	}
	// Filter to human-readable summary lines (assistant text + result).
	out := make([]string, 0, len(tail))
	for _, ln := range tail {
		var ev struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype,omitempty"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message,omitempty"`
			Result string `json:"result,omitempty"`
		}
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "assistant":
			for _, c := range ev.Message.Content {
				if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
					out = append(out, "assistant: "+c.Text)
				}
			}
		case "result":
			if ev.Result != "" {
				out = append(out, "result: "+ev.Result)
			}
		}
	}
	writeJSON(w, map[string]any{"lines": out})
}

// ───────────────────────────── helpers ─────────────────────────────

type workerSummary struct {
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	RepoPath    string     `json:"repo_path"`
	Model       string     `json:"model,omitempty"`
	LastRunAt   *time.Time `json:"last_run_at,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	Locked      bool       `json:"locked"`
	PreviewText string     `json:"preview_text,omitempty"`
}

func summarize(p *store.Project) projectSummary {
	out := projectSummary{Slug: p.Slug, Name: p.Name, Description: p.Description}
	workers, _ := store.ListWorkers(p.Slug)
	out.WorkerCount = len(workers)
	for _, w := range workers {
		switch w.Status {
		case store.StatusRunning:
			out.Running++
		case store.StatusError:
			out.Error++
		default:
			out.Idle++
		}
		if store.PathExists(store.WorkerLockFile(p.Slug, w.Name)) {
			out.Locked++
		}
		if w.LastRunAt != nil {
			if out.LastActivityAt == nil || w.LastRunAt.After(*out.LastActivityAt) {
				t := *w.LastRunAt
				out.LastActivityAt = &t
			}
		}
	}
	out.InboxPending = countInbox(p.Slug)
	return out
}

func summarizeWorker(slug string, w *store.Worker) workerSummary {
	sum := workerSummary{
		Name:      w.Name,
		Status:    string(w.Status),
		RepoPath:  w.RepoPath,
		Model:     w.Model,
		LastRunAt: w.LastRunAt,
		LastError: w.LastError,
		Locked:    store.PathExists(store.WorkerLockFile(slug, w.Name)),
	}
	if b, err := os.ReadFile(store.WorkerLastFile(slug, w.Name)); err == nil {
		txt := string(b)
		if len(txt) > 600 {
			txt = txt[:600]
		}
		sum.PreviewText = txt
	}
	return sum
}

func countInbox(slug string) int {
	f, err := os.Open(store.InboxFile(slug))
	if err != nil {
		return 0
	}
	defer f.Close()
	count := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			count++
		}
	}
	return count
}

func readInboxEvents(slug string) []map[string]any {
	out := []map[string]any{}
	f, err := os.Open(store.InboxFile(slug))
	if err != nil {
		return out
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &ev); err == nil {
			out = append(out, ev)
		}
	}
	return out
}

func tailFile(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	all := []string{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		all = append(all, scanner.Text())
	}
	if len(all) <= n {
		return all, nil
	}
	return all[len(all)-n:], nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func cors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
}
