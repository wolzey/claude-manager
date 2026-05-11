package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/wolzey/claude-manager/internal/config"
	"github.com/wolzey/claude-manager/internal/dashboard"
	"github.com/wolzey/claude-manager/internal/store"
)

const (
	exitOK      = 0
	exitTimeout = 2
)

func waitCmd() *cobra.Command {
	var workerFilter string
	var forCond string
	var timeout time.Duration

	c := &cobra.Command{
		Use:   "wait <project>",
		Short: "Block until a worker state change matches a condition (or timeout)",
		Long: `Blocks until the specified condition is met, then exits 0 and prints the
triggering event. Exits 2 on timeout. Use to gate Claude on async sends without polling:
  cmgr wait <proj> --worker backend --for completed --timeout 10m

Conditions:
  completed  worker transitions to idle, or inbox event of type "completed"
  error      worker transitions to error, or inbox event of type "error"
  idle       worker is currently (or becomes) idle
  change     any state change for the target (default)
  inbox      inbox.jsonl grows`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := store.Slugify(args[0])
			if err := store.RequireProject(slug); err != nil {
				return err
			}
			if timeout <= 0 {
				timeout = 5 * time.Minute
			}
			if timeout > time.Hour {
				timeout = time.Hour
			}
			cfg, _ := config.Load()
			code := runWait(slug, workerFilter, forCond, timeout, cfg.WatchDebounceMS)
			if code == exitTimeout {
				os.Exit(exitTimeout)
			}
			return nil
		},
	}
	c.Flags().StringVar(&workerFilter, "worker", "", "filter to a single worker")
	c.Flags().StringVar(&forCond, "for", "change", "condition: completed | error | idle | change | inbox")
	c.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "max time to wait (default 5m, capped at 1h)")
	return c
}

func runWait(slug, workerFilter, forCond string, timeout time.Duration, debounceMS int) int {
	// Fast path — "idle" is satisfied immediately if the worker is already idle.
	if forCond == "idle" && workerFilter != "" {
		if w, err := store.LoadWorker(slug, workerFilter); err == nil && string(w.Status) == "idle" {
			emitJSON(map[string]any{
				"kind": "worker_changed", "project": slug, "worker": w.Name, "status": "idle",
				"ts": time.Now().UTC().Format(time.RFC3339Nano),
			})
			return exitOK
		}
	}

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

	deadline := time.After(timeout)

	for {
		select {
		case <-ctx.Done():
			emitJSON(map[string]any{"kind": "cancelled"})
			return exitTimeout
		case <-deadline:
			emitJSON(map[string]any{"kind": "timeout"})
			return exitTimeout
		case ev, open := <-ch:
			if !open {
				return exitTimeout
			}
			if ev.Project != slug {
				continue
			}
			if workerFilter != "" && ev.Worker != "" && ev.Worker != workerFilter {
				continue
			}
			if matchesCondition(ev, forCond) {
				emitEvent(ev)
				return exitOK
			}
		}
	}
}

func matchesCondition(ev dashboard.Event, cond string) bool {
	switch cond {
	case "completed":
		if ev.Kind == dashboard.KindWorkerChanged && ev.Status == "idle" {
			return true
		}
		if ev.Kind == dashboard.KindInboxAppended {
			if payload, ok := ev.Payload.(map[string]any); ok && payload["type"] == "completed" {
				return true
			}
		}
		return false
	case "error":
		if ev.Kind == dashboard.KindWorkerChanged && ev.Status == "error" {
			return true
		}
		if ev.Kind == dashboard.KindInboxAppended {
			if payload, ok := ev.Payload.(map[string]any); ok && payload["type"] == "error" {
				return true
			}
		}
		return false
	case "idle":
		return ev.Kind == dashboard.KindWorkerChanged && ev.Status == "idle"
	case "inbox":
		return ev.Kind == dashboard.KindInboxAppended
	case "change":
		fallthrough
	default:
		return true
	}
}

func emitEvent(ev dashboard.Event) {
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(ev)
}

func emitJSON(v map[string]any) {
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(v)
}
