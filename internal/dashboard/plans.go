package dashboard

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wolzey/claude-manager/internal/store"
)

// PlanApprover is satisfied by an external "execute the approved plan" caller.
// We can't import internal/cmd from here (would create an import cycle), so
// the dashboard server is wired with an approver function from main.go's
// init path (or — simpler — we re-implement the approve/reject prompts here
// using the same constants and call directly through to runSend via a Go-level
// function pointer registered at startup).
type PlanApprover interface {
	Approve(slug, worker, extraContext string, persist bool) error
	Reject(slug, worker, feedback string) error
}

// SetApprover wires a PlanApprover into the server. Called from cmd/dashboard.go
// at startup so the HTTP handlers can fire approval / rejection sends without
// the dashboard package importing internal/cmd (cycle-safe).
func (s *Server) SetApprover(a PlanApprover) { s.approver = a }

// listPendingPlans returns all workers that currently have a pending plan in
// the project, with the plan body inlined for convenience.
func (s *Server) listPendingPlans(w http.ResponseWriter, r *http.Request, slug string) {
	cors(w)
	workers, err := store.ListWorkers(slug)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type pending struct {
		Worker        string     `json:"worker"`
		PlanPath      string     `json:"plan_path"`
		PendingPlanAt *time.Time `json:"pending_plan_at,omitempty"`
		Body          string     `json:"body"`
	}
	out := []pending{}
	for _, wk := range workers {
		if wk.PendingPlan == "" {
			continue
		}
		body, _ := os.ReadFile(filepath.Join(store.ProjectDir(slug), wk.PendingPlan))
		out = append(out, pending{
			Worker:        wk.Name,
			PlanPath:      wk.PendingPlan,
			PendingPlanAt: wk.PendingPlanAt,
			Body:          string(body),
		})
	}
	writeJSON(w, out)
}

// currentPlan returns the worker's pending plan body + metadata.
func (s *Server) currentPlan(w http.ResponseWriter, r *http.Request, slug, name string) {
	cors(w)
	worker, err := store.LoadWorker(slug, name)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if worker.PendingPlan == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	abs := filepath.Join(store.ProjectDir(slug), worker.PendingPlan)
	body, err := os.ReadFile(abs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{
		"worker":          worker.Name,
		"plan_path":       worker.PendingPlan,
		"pending_plan_at": worker.PendingPlanAt,
		"body":            string(body),
	})
}

// planHistory lists past plans on disk for the worker, newest first.
func (s *Server) planHistory(w http.ResponseWriter, r *http.Request, slug, name string) {
	cors(w)
	worker, err := store.LoadWorker(slug, name)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	dir := store.PlansDir(slug)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, []any{})
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type entry struct {
		Path    string    `json:"path"`
		ModTime time.Time `json:"mod_time"`
		Current bool      `json:"current"`
	}
	out := []entry{}
	prefix := worker.Name + "-"
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, _ := e.Info()
		rel := filepath.Join("plans", e.Name())
		out = append(out, entry{Path: rel, ModTime: info.ModTime(), Current: rel == worker.PendingPlan})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	writeJSON(w, out)
}

// approvePlan fires the approval send through the wired PlanApprover. Returns
// 202 immediately; SSE will report execution progress.
func (s *Server) approvePlan(w http.ResponseWriter, r *http.Request, slug, name string) {
	cors(w)
	if !s.allowMutating(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.approver == nil {
		writeErr(w, http.StatusServiceUnavailable, errString("approver not wired"))
		return
	}
	var body struct {
		With    string `json:"with,omitempty"`
		Persist bool   `json:"persist,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	go func() {
		_ = s.approver.Approve(slug, name, body.With, body.Persist)
	}()
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "approving"})
}

// rejectPlan fires a plan-mode revision send with the supplied feedback.
func (s *Server) rejectPlan(w http.ResponseWriter, r *http.Request, slug, name string) {
	cors(w)
	if !s.allowMutating(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.approver == nil {
		writeErr(w, http.StatusServiceUnavailable, errString("approver not wired"))
		return
	}
	var body struct {
		Feedback string `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Feedback) == "" {
		writeErr(w, http.StatusBadRequest, errString("feedback is required"))
		return
	}
	feedback := body.Feedback
	go func() {
		_ = s.approver.Reject(slug, name, feedback)
	}()
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "rejecting"})
}

// allowMutating is a minimal CSRF guard. The dashboard binds to loopback by
// default; we additionally require that any browser-issued Origin header
// match the loopback. Plain curl (no Origin) is allowed.
func (s *Server) allowMutating(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if strings.HasPrefix(origin, "http://127.0.0.1") ||
		strings.HasPrefix(origin, "http://localhost") ||
		strings.HasPrefix(origin, "http://[::1]") {
		return true
	}
	writeErr(w, http.StatusForbidden, errString("cross-origin POST blocked"))
	return false
}

type errString string

func (e errString) Error() string { return string(e) }
