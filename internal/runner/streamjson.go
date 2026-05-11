package runner

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
)

// StreamEvent is the union shape for `claude -p --output-format stream-json` NDJSON lines.
// Only the fields we care about — extra fields are ignored.
type StreamEvent struct {
	Type         string  `json:"type"`
	Subtype      string  `json:"subtype,omitempty"`
	SessionID    string  `json:"session_id,omitempty"`
	Result       string  `json:"result,omitempty"`
	IsError      bool    `json:"is_error,omitempty"`
	StopReason   string  `json:"stop_reason,omitempty"`
	TotalCostUSD float64 `json:"total_cost_usd,omitempty"`
	DurationMS   int64   `json:"duration_ms,omitempty"`
}

// Result is the extracted summary of one claude -p invocation.
type Result struct {
	SessionID    string
	Text         string
	IsError      bool
	StopReason   string
	TotalCostUSD float64
	DurationMS   int64
}

var ErrNoResult = errors.New("no result line in stream")

// ParseStream reads NDJSON from r, tees every raw line to teeOut (if non-nil),
// and returns the final result event. Tolerates malformed lines (skips them).
func ParseStream(r io.Reader, teeOut io.Writer) (*Result, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var finalResult *Result
	for scanner.Scan() {
		line := scanner.Bytes()
		if teeOut != nil {
			_, _ = teeOut.Write(line)
			_, _ = teeOut.Write([]byte("\n"))
		}
		var ev StreamEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Type == "result" {
			finalResult = &Result{
				SessionID:    ev.SessionID,
				Text:         ev.Result,
				IsError:      ev.IsError,
				StopReason:   ev.StopReason,
				TotalCostUSD: ev.TotalCostUSD,
				DurationMS:   ev.DurationMS,
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return finalResult, err
	}
	if finalResult == nil {
		return nil, ErrNoResult
	}
	return finalResult, nil
}
