package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/wolzey/claude-manager/internal/config"
	"github.com/wolzey/claude-manager/internal/dashboard"
	"github.com/wolzey/claude-manager/internal/store"
)

func watchCmd() *cobra.Command {
	var workerFilter string
	var includeRaw string
	var since time.Duration

	c := &cobra.Command{
		Use:   "watch <project>",
		Short: "Stream NDJSON state-change events for a project to stdout",
		Long: `Tails the filesystem watcher and prints one NDJSON event per state change.
Designed to be paired with Claude's Monitor tool (Bash run_in_background=true). The same
event shapes the dashboard SSE feed emits are printed to stdout: worker_changed,
inbox_appended, contract_changed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := store.Slugify(args[0])
			if err := store.RequireProject(slug); err != nil {
				return err
			}
			include := parseInclude(includeRaw)
			cfg, _ := config.Load()
			return runWatch(slug, workerFilter, include, since, cfg.WatchDebounceMS)
		},
	}
	c.Flags().StringVar(&workerFilter, "worker", "", "filter to a single worker")
	c.Flags().StringVar(&includeRaw, "include", "", "comma-separated event kinds: worker_changed,inbox_appended,contract_changed (default all)")
	c.Flags().DurationVar(&since, "since", 0, "replay changes from the last duration before going live (e.g. 5m)")
	return c
}

func runWatch(slug, workerFilter string, include map[string]bool, since time.Duration, debounceMS int) error {
	w := dashboard.NewWatcher(time.Duration(debounceMS) * time.Millisecond)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := w.Run(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "[cmgr] watcher: %v\n", err)
		}
	}()

	ch, unsubscribe := w.Subscribe()
	defer unsubscribe()

	enc := json.NewEncoder(os.Stdout)

	// Replay recent changes from disk timestamps if requested.
	if since > 0 {
		emitReplay(enc, slug, workerFilter, include, since)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, open := <-ch:
			if !open {
				return nil
			}
			if ev.Project != slug {
				continue
			}
			if workerFilter != "" && ev.Worker != "" && ev.Worker != workerFilter {
				continue
			}
			if len(include) > 0 && !include[ev.Kind] {
				continue
			}
			// Encode errors (e.g. EPIPE from `cmgr watch | head`) end the loop cleanly.
			if err := enc.Encode(ev); err != nil {
				return nil
			}
		}
	}
}

func parseInclude(raw string) map[string]bool {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	out := map[string]bool{}
	for _, k := range strings.Split(raw, ",") {
		k = strings.TrimSpace(k)
		if k != "" {
			out[k] = true
		}
	}
	return out
}

func emitReplay(enc *json.Encoder, slug, workerFilter string, include map[string]bool, since time.Duration) {
	cutoff := time.Now().UTC().Add(-since)
	// Replay recent inbox events.
	if include == nil || include[dashboard.KindInboxAppended] {
		inboxPath := store.InboxFile(slug)
		f, err := os.Open(inboxPath)
		if err == nil {
			defer f.Close()
			dec := json.NewDecoder(f)
			for dec.More() {
				var ev map[string]any
				if err := dec.Decode(&ev); err != nil {
					break
				}
				ts, _ := time.Parse(time.RFC3339Nano, getStr(ev, "timestamp"))
				if ts.Before(cutoff) {
					continue
				}
				worker := getStr(ev, "worker")
				if workerFilter != "" && worker != workerFilter {
					continue
				}
				_ = enc.Encode(dashboard.Event{
					Kind: dashboard.KindInboxAppended, Project: slug, Worker: worker,
					Payload: ev, Timestamp: ts,
				})
			}
		}
	}
	// Replay current worker statuses as worker_changed.
	if include == nil || include[dashboard.KindWorkerChanged] {
		workers, _ := store.ListWorkers(slug)
		for _, wk := range workers {
			if workerFilter != "" && wk.Name != workerFilter {
				continue
			}
			if wk.LastRunAt == nil || wk.LastRunAt.Before(cutoff) {
				continue
			}
			_ = enc.Encode(dashboard.Event{
				Kind: dashboard.KindWorkerChanged, Project: slug, Worker: wk.Name,
				Status: string(wk.Status), Timestamp: *wk.LastRunAt,
			})
		}
	}
}

func getStr(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}
