// Package dashboard provides a local HTTP dashboard and a shared filesystem
// watcher used by both the dashboard SSE feed and the cmgr watch / wait CLIs.
package dashboard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/wolzey/claude-manager/internal/store"
)

// Event is the fan-out shape for every filesystem change worth reporting.
// The JSON encoding matches the SSE feed and the cmgr watch NDJSON output.
type Event struct {
	Kind      string    `json:"kind"`
	Project   string    `json:"project,omitempty"`
	Worker    string    `json:"worker,omitempty"`
	Status    string    `json:"status,omitempty"`
	Payload   any       `json:"event,omitempty"`
	Timestamp time.Time `json:"ts"`
}

const (
	KindWorkerChanged   = "worker_changed"
	KindInboxAppended   = "inbox_appended"
	KindContractChanged = "contract_changed"
	KindProjectAdded    = "project_added"
	KindProjectRemoved  = "project_removed"
)

// Watcher fans out fsnotify events to N subscribers. It debounces bursts within
// debounce so atomic writes (write tmp → rename) don't double-emit.
type Watcher struct {
	debounce time.Duration

	mu          sync.Mutex
	subscribers map[chan Event]struct{}

	// Per-path last-emit timestamp for debounce.
	lastEmit   map[string]time.Time
	lastWorker map[string]string // worker file path → last seen status, for change detection
	lastInbox  map[string]int64  // inbox file path → last byte size
}

func NewWatcher(debounce time.Duration) *Watcher {
	if debounce <= 0 {
		debounce = 100 * time.Millisecond
	}
	return &Watcher{
		debounce:    debounce,
		subscribers: make(map[chan Event]struct{}),
		lastEmit:    make(map[string]time.Time),
		lastWorker:  make(map[string]string),
		lastInbox:   make(map[string]int64),
	}
}

// Subscribe returns a buffered channel that receives every event the watcher
// emits. Caller must invoke the returned unsubscribe func when done.
func (w *Watcher) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	w.mu.Lock()
	w.subscribers[ch] = struct{}{}
	w.mu.Unlock()
	return ch, func() {
		w.mu.Lock()
		delete(w.subscribers, ch)
		w.mu.Unlock()
		close(ch)
	}
}

func (w *Watcher) emit(ev Event) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for ch := range w.subscribers {
		select {
		case ch <- ev:
		default:
			// Slow subscriber — drop rather than block the watcher loop.
		}
	}
}

// Run blocks, watching the projects root until ctx is cancelled. It seeds
// initial worker/inbox snapshots so the first real event has a baseline to
// diff against.
func (w *Watcher) Run(ctx context.Context) error {
	root := store.ProjectsDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}

	w.seedSnapshots(root)

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fsw.Close()

	if err := w.addRecursive(fsw, root); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case err, ok := <-fsw.Errors:
			if !ok {
				return nil
			}
			_ = err // fsnotify errors are non-fatal; keep going
		case ev, ok := <-fsw.Events:
			if !ok {
				return nil
			}
			w.handle(fsw, ev)
		}
	}
}

func (w *Watcher) handle(fsw *fsnotify.Watcher, ev fsnotify.Event) {
	// Debounce — skip if we emitted for this path within the window.
	w.mu.Lock()
	if last, ok := w.lastEmit[ev.Name]; ok && time.Since(last) < w.debounce {
		w.mu.Unlock()
		return
	}
	w.lastEmit[ev.Name] = time.Now()
	w.mu.Unlock()

	// New directory: make sure we watch it.
	if ev.Op&(fsnotify.Create) != 0 {
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
			_ = fsw.Add(ev.Name)
		}
	}

	rel, ok := w.relativeToProjects(ev.Name)
	if !ok {
		return
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) == 0 || parts[0] == "" {
		return
	}
	project := parts[0]

	// project.json — added or removed
	if len(parts) == 2 && parts[1] == "project.json" {
		if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) != 0 {
			if store.PathExists(ev.Name) {
				w.emit(Event{Kind: KindProjectAdded, Project: project})
			}
		}
		if ev.Op&fsnotify.Remove != 0 {
			w.emit(Event{Kind: KindProjectRemoved, Project: project})
		}
		return
	}

	// contract.md
	if len(parts) == 2 && parts[1] == "contract.md" {
		w.emit(Event{Kind: KindContractChanged, Project: project})
		return
	}

	// inbox.jsonl — only emit when size grows
	if len(parts) == 2 && parts[1] == "inbox.jsonl" {
		w.handleInbox(project, ev.Name)
		return
	}

	// workers/<name>.json
	if len(parts) == 3 && parts[1] == "workers" && strings.HasSuffix(parts[2], ".json") {
		name := strings.TrimSuffix(parts[2], ".json")
		// Skip the atomic-write temp file (".tmp-*").
		if strings.HasPrefix(name, ".tmp-") {
			return
		}
		w.handleWorker(project, name, ev.Name)
		return
	}

	// locks/<name>.lock — flock acquire/release
	if len(parts) == 3 && parts[1] == "locks" && strings.HasSuffix(parts[2], ".lock") {
		name := strings.TrimSuffix(parts[2], ".lock")
		// Treat as a worker status hint — emit with current worker status.
		wf := store.WorkerFile(project, name)
		if status, ok := readWorkerStatus(wf); ok {
			if !store.PathExists(ev.Name) {
				// Lock just released; status may not have caught up yet.
				w.emit(Event{Kind: KindWorkerChanged, Project: project, Worker: name, Status: status})
			}
		}
		return
	}
}

func (w *Watcher) handleWorker(project, name, path string) {
	status, ok := readWorkerStatus(path)
	if !ok {
		return
	}
	w.mu.Lock()
	prev := w.lastWorker[path]
	w.lastWorker[path] = status
	w.mu.Unlock()
	if prev == status {
		return
	}
	w.emit(Event{Kind: KindWorkerChanged, Project: project, Worker: name, Status: status})
}

func (w *Watcher) handleInbox(project, path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	size := info.Size()
	w.mu.Lock()
	prev := w.lastInbox[path]
	w.lastInbox[path] = size
	w.mu.Unlock()
	if size <= prev {
		return
	}
	// Read just the appended tail and emit the last event.
	ev, ok := readLastInboxEvent(path, prev)
	if !ok {
		return
	}
	w.emit(Event{Kind: KindInboxAppended, Project: project, Worker: getString(ev, "worker"), Payload: ev})
}

func (w *Watcher) seedSnapshots(root string) {
	projects, _ := os.ReadDir(root)
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		slug := p.Name()
		// Worker statuses.
		workersDir := filepath.Join(root, slug, "workers")
		if entries, err := os.ReadDir(workersDir); err == nil {
			for _, e := range entries {
				if !strings.HasSuffix(e.Name(), ".json") {
					continue
				}
				wf := filepath.Join(workersDir, e.Name())
				if status, ok := readWorkerStatus(wf); ok {
					w.lastWorker[wf] = status
				}
			}
		}
		// Inbox size.
		ip := filepath.Join(root, slug, "inbox.jsonl")
		if info, err := os.Stat(ip); err == nil {
			w.lastInbox[ip] = info.Size()
		}
	}
}

func (w *Watcher) addRecursive(fsw *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable, keep going
		}
		if d.IsDir() {
			return fsw.Add(p)
		}
		return nil
	})
}

func (w *Watcher) relativeToProjects(path string) (string, bool) {
	rel, err := filepath.Rel(store.ProjectsDir(), path)
	if err != nil {
		return "", false
	}
	if strings.HasPrefix(rel, "..") {
		return "", false
	}
	return rel, true
}

func readWorkerStatus(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var w struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return "", false
	}
	return w.Status, true
}

func readLastInboxEvent(path string, prevSize int64) (map[string]any, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if _, err := f.Seek(prevSize, 0); err != nil {
		return nil, false
	}
	b := make([]byte, 1024*1024)
	n, _ := f.Read(b)
	if n == 0 {
		return nil, false
	}
	// Take the last complete line in the appended chunk.
	lines := strings.Split(strings.TrimRight(string(b[:n]), "\n"), "\n")
	if len(lines) == 0 {
		return nil, false
	}
	last := lines[len(lines)-1]
	var ev map[string]any
	if err := json.Unmarshal([]byte(last), &ev); err != nil {
		return nil, false
	}
	return ev, true
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
