package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

type WorkerStatus string

const (
	StatusIdle    WorkerStatus = "idle"
	StatusRunning WorkerStatus = "running"
	StatusError   WorkerStatus = "error"
)

type Worker struct {
	Name           string       `json:"name"`
	SessionID      string       `json:"session_id"`
	RepoPath       string       `json:"repo_path"`
	AllowedTools   string       `json:"allowed_tools,omitempty"`
	PermissionMode string       `json:"permission_mode,omitempty"`
	Model          string       `json:"model,omitempty"`
	Status         WorkerStatus `json:"status"`
	Initialized    bool         `json:"initialized"`
	LastRunAt      *time.Time   `json:"last_run_at,omitempty"`
	LastError      string       `json:"last_error,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
}

func CreateWorker(slug, name, repoPath, allowedTools, model string) (*Worker, error) {
	if err := RequireProject(slug); err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("worker name required")
	}
	if !filepath.IsAbs(repoPath) {
		return nil, fmt.Errorf("--repo must be an absolute path, got %q", repoPath)
	}
	info, err := os.Stat(repoPath)
	if err != nil {
		return nil, fmt.Errorf("--repo path does not exist: %s", repoPath)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("--repo must be a directory: %s", repoPath)
	}
	wf := WorkerFile(slug, name)
	if PathExists(wf) {
		return nil, fmt.Errorf("worker %q already exists in project %q", name, slug)
	}
	w := &Worker{
		Name:         name,
		SessionID:    uuid.NewString(),
		RepoPath:     repoPath,
		AllowedTools: allowedTools,
		Model:        model,
		Status:       StatusIdle,
		CreatedAt:    time.Now().UTC(),
	}
	if err := WriteJSONAtomic(wf, w); err != nil {
		return nil, err
	}
	return w, nil
}

func LoadWorker(slug, name string) (*Worker, error) {
	if err := RequireProject(slug); err != nil {
		return nil, err
	}
	wf := WorkerFile(slug, name)
	if !PathExists(wf) {
		return nil, fmt.Errorf("worker %q not found in project %q", name, slug)
	}
	var w Worker
	if err := ReadJSON(wf, &w); err != nil {
		return nil, err
	}
	return &w, nil
}

func SaveWorker(slug string, w *Worker) error {
	return WriteJSONAtomic(WorkerFile(slug, w.Name), w)
}

func ListWorkers(slug string) ([]*Worker, error) {
	if err := RequireProject(slug); err != nil {
		return nil, err
	}
	dir := WorkersDir(slug)
	if !PathExists(dir) {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*Worker
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var w Worker
		if err := ReadJSON(filepath.Join(dir, e.Name()), &w); err != nil {
			continue
		}
		out = append(out, &w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func DeleteWorker(slug, name string) error {
	if err := RequireProject(slug); err != nil {
		return err
	}
	wf := WorkerFile(slug, name)
	if !PathExists(wf) {
		return fmt.Errorf("worker %q not found in project %q", name, slug)
	}
	return os.Remove(wf)
}
