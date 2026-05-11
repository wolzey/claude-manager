package dashboard

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/wolzey/claude-manager/internal/store"
)

// TailEvent is the compact shape sent over SSE for live worker-log tailing.
// One event per parsed stream-json line that's worth showing a human.
type TailEvent struct {
	Kind   string `json:"kind"`              // assistant | tool_use | tool_result | result | system | error
	Text   string `json:"text,omitempty"`    // assistant message text, result text, error text
	Tool   string `json:"tool,omitempty"`    // tool name (Bash, Read, Edit, …)
	Input  string `json:"input,omitempty"`   // one-line tool input summary
	Output string `json:"output,omitempty"`  // tool result preview (truncated)
}

const (
	tailPollInterval = 250 * time.Millisecond
	tailBacklogLines = 200
	tailMaxLineSize  = 16 * 1024 * 1024
)

func (s *Server) writeLogStream(w http.ResponseWriter, r *http.Request, slug, name string) {
	if _, err := store.LoadWorker(slug, name); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	path := store.WorkerLogFile(slug, name)
	pos := emitBacklog(w, flusher, path, tailBacklogLines)

	ticker := time.NewTicker(tailPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			pos = emitNew(w, flusher, path, pos)
		}
	}
}

// emitBacklog returns the last N parsed events (newest at the end) and the
// current file position so subsequent reads can pick up where we left off.
func emitBacklog(w http.ResponseWriter, flusher http.Flusher, path string, lines int) int64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), tailMaxLineSize)
	all := []string{}
	for scanner.Scan() {
		all = append(all, scanner.Text())
	}
	info, _ := f.Stat()
	start := 0
	if len(all) > lines {
		start = len(all) - lines
	}
	for _, ln := range all[start:] {
		if ev, ok := parseTailLine(ln); ok {
			writeTailSSE(w, flusher, ev)
		}
	}
	if info != nil {
		return info.Size()
	}
	return 0
}

// emitNew reads from pos to EOF, emits parsed events, and returns the new pos.
// Handles file truncation (size dropped → reset to 0) and missing file (returns
// 0 so a future create starts fresh).
func emitNew(w http.ResponseWriter, flusher http.Flusher, path string, pos int64) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	size := info.Size()
	if size < pos {
		// File was truncated (new run) — re-read from start.
		pos = 0
	}
	if size == pos {
		return pos
	}
	f, err := os.Open(path)
	if err != nil {
		return pos
	}
	defer f.Close()
	if _, err := f.Seek(pos, 0); err != nil {
		return pos
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), tailMaxLineSize)
	for scanner.Scan() {
		if ev, ok := parseTailLine(scanner.Text()); ok {
			writeTailSSE(w, flusher, ev)
		}
	}
	return size
}

func writeTailSSE(w http.ResponseWriter, flusher http.Flusher, ev TailEvent) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}

// parseTailLine converts a stream-json NDJSON line into a TailEvent, returning
// false for lines that aren't worth showing (e.g. init / type=user echoes).
func parseTailLine(line string) (TailEvent, bool) {
	if strings.TrimSpace(line) == "" {
		return TailEvent{}, false
	}
	var raw struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype,omitempty"`
		Message struct {
			Content []struct {
				Type   string          `json:"type"`
				Text   string          `json:"text,omitempty"`
				Name   string          `json:"name,omitempty"`
				Input  json.RawMessage `json:"input,omitempty"`
				Content json.RawMessage `json:"content,omitempty"`
			} `json:"content"`
		} `json:"message,omitempty"`
		Result     string `json:"result,omitempty"`
		IsError    bool   `json:"is_error,omitempty"`
		StopReason string `json:"stop_reason,omitempty"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return TailEvent{}, false
	}

	switch raw.Type {
	case "assistant":
		for _, c := range raw.Message.Content {
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				return TailEvent{Kind: "assistant", Text: c.Text}, true
			}
			if c.Type == "tool_use" {
				return TailEvent{
					Kind:  "tool_use",
					Tool:  c.Name,
					Input: summarizeToolInput(c.Name, c.Input),
				}, true
			}
		}
	case "user":
		for _, c := range raw.Message.Content {
			if c.Type == "tool_result" {
				return TailEvent{
					Kind:   "tool_result",
					Output: summarizeToolResult(c.Content),
				}, true
			}
		}
	case "result":
		ev := TailEvent{Kind: "result", Text: raw.Result}
		if raw.IsError {
			ev.Kind = "error"
		}
		return ev, true
	case "system":
		// Surface only init so the user knows the session started.
		if raw.Subtype == "init" {
			return TailEvent{Kind: "system", Text: "session initialized"}, true
		}
	}
	return TailEvent{}, false
}

func summarizeToolInput(name string, raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	// Pick the most distinctive field per tool.
	for _, k := range []string{"command", "file_path", "path", "pattern", "query", "url", "description"} {
		if v, ok := m[k].(string); ok && v != "" {
			return truncate(v, 200)
		}
	}
	// Fallback — first string value.
	for _, v := range m {
		if s, ok := v.(string); ok && s != "" {
			return truncate(s, 200)
		}
	}
	_ = name
	return ""
}

func summarizeToolResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// tool_result content can be a string OR an array of content blocks.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return truncate(s, 200)
	}
	var arr []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &arr); err == nil {
		for _, b := range arr {
			if b.Text != "" {
				return truncate(b.Text, 200)
			}
		}
	}
	return ""
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
