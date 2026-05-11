package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/wolzey/claude-manager/internal/config"
	"github.com/wolzey/claude-manager/internal/runner"
	"github.com/wolzey/claude-manager/internal/store"
)

type InboxEvent struct {
	Worker     string    `json:"worker"`
	Timestamp  time.Time `json:"timestamp"`
	Type       string    `json:"type"`
	Result     string    `json:"result,omitempty"`
	Error      string    `json:"error,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	CostUSD    float64   `json:"cost_usd,omitempty"`
	PlanPath   string    `json:"plan_path,omitempty"`
}

func sendCmd() *cobra.Command {
	var detach bool
	var budget float64
	var model, mode string
	c := &cobra.Command{
		Use:   "send <project> <worker> <message>",
		Short: "Send a message to a worker (sync by default, --detach for async)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateMode(mode); err != nil {
				return err
			}
			return runSend(args[0], args[1], args[2], detach, budget, model, mode)
		},
	}
	c.Flags().BoolVar(&detach, "detach", false, "fire-and-forget; result lands in inbox.jsonl")
	c.Flags().Float64Var(&budget, "budget", 0, "max-budget-usd cap (0 = use config default)")
	c.Flags().StringVar(&model, "model", "", "override worker's default model")
	c.Flags().StringVar(&mode, "mode", "", "one-shot permission mode override: plan | acceptEdits | readonly")
	return c
}

func runSend(projectArg, workerName, message string, detach bool, budget float64, model, mode string) error {
	slug := store.Slugify(projectArg)
	w, err := store.LoadWorker(slug, workerName)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if budget <= 0 {
		budget = cfg.DefaultBudgetUSD
	}
	chosenModel := model
	if chosenModel == "" {
		chosenModel = w.Model
	}
	if chosenModel == "" {
		chosenModel = cfg.DefaultModel
	}
	chosenMode := resolveMode(mode, w.DefaultMode, cfg.DefaultPermission)

	if detach {
		return runDetached(slug, w.Name, message, budget, chosenModel, chosenMode)
	}

	return runForeground(slug, w, message, budget, chosenModel, chosenMode)
}

// resolveMode applies the per-send → per-worker → config-default resolution
// order. "default" is treated as "fall through to the next layer."
func resolveMode(send, worker, cfg string) string {
	for _, m := range []string{send, worker, cfg} {
		if m != "" && m != "default" {
			return m
		}
	}
	return "acceptEdits"
}

func runForeground(slug string, w *store.Worker, message string, budget float64, model, permMode string) error {
	lock, err := runner.Acquire(store.WorkerLockFile(slug, w.Name))
	if err != nil {
		return fmt.Errorf("worker %q is busy (lock held); try `cmgr status %s`", w.Name, slug)
	}
	defer lock.Release()

	w.Status = store.StatusRunning
	now := time.Now().UTC()
	w.LastRunAt = &now
	_ = store.SaveWorker(slug, w)

	start := time.Now()
	res, runErr := runner.Run(runner.Options{
		Prompt:         message,
		SessionID:      w.SessionID,
		Resume:         w.Initialized,
		Name:           w.Name,
		Cwd:            w.RepoPath,
		AllowedTools:   w.AllowedTools,
		Model:          model,
		BudgetUSD:      budget,
		PermissionMode: permMode,
		LogPath:        store.WorkerLogFile(slug, w.Name),
	})
	elapsed := time.Since(start)

	if runErr != nil && res == nil {
		w.Status = store.StatusError
		w.LastError = runErr.Error()
		_ = store.SaveWorker(slug, w)
		return runErr
	}
	if res.IsError {
		w.Status = store.StatusError
		w.LastError = res.Text
	} else if permMode != "plan" && w.PendingPlan != "" {
		// Successful non-plan execution clears any prior pending plan (this is
		// what `cmgr plan approve` relies on — the plan is now executed).
		w.PendingPlan = ""
		w.PendingPlanAt = nil
		w.Status = store.StatusIdle
		w.LastError = ""
		w.Initialized = true
	} else if permMode == "plan" && strings.TrimSpace(res.Text) != "" {
		planPath, err := capturePlan(slug, w.Name, res.Text)
		if err == nil {
			w.PendingPlan = planPath
			now := time.Now().UTC()
			w.PendingPlanAt = &now
			w.Status = store.StatusPlanPending
		} else {
			fmt.Fprintf(os.Stderr, "[cmgr] failed to persist plan: %v\n", err)
			w.Status = store.StatusIdle
		}
		w.LastError = ""
		w.Initialized = true
	} else {
		w.Status = store.StatusIdle
		w.LastError = ""
		w.Initialized = true
	}
	_ = store.SaveWorker(slug, w)

	if res.Text != "" {
		_ = os.WriteFile(store.WorkerLastFile(slug, w.Name), []byte(res.Text), 0o644)
	}

	fmt.Println(res.Text)
	fmt.Fprintf(os.Stderr, "\n[cmgr] worker=%s mode=%s elapsed=%s cost=$%.4f stop_reason=%s\n",
		w.Name, permMode, elapsed.Round(time.Millisecond), res.TotalCostUSD, res.StopReason)
	if w.Status == store.StatusPlanPending {
		fmt.Fprintf(os.Stderr, "[cmgr] plan saved to %s — review with `cmgr plan show %s %s`\n",
			w.PendingPlan, slug, w.Name)
	}
	if res.IsError {
		return fmt.Errorf("worker reported error")
	}
	return nil
}

// capturePlan persists the worker's plan-mode result text to a timestamped
// file under plans/. Returns the project-relative path so it can be stored on
// Worker.PendingPlan.
func capturePlan(slug, workerName, text string) (string, error) {
	ts := time.Now().UTC().Format("2006-01-02T15-04-05")
	abs := store.PlanFile(slug, workerName, ts)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(text), 0o644); err != nil {
		return "", err
	}
	// Return path relative to the project dir so it's portable across machines.
	rel, err := filepath.Rel(store.ProjectDir(slug), abs)
	if err != nil {
		return abs, nil
	}
	return rel, nil
}

func runDetached(slug, worker, message string, budget float64, model, mode string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if mode == "" {
		mode = "default"
	}
	args := []string{"_run-detached", slug, worker, message, fmt.Sprintf("%.4f", budget), model, mode}
	cmd := exec.Command(exe, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	fmt.Printf("dispatched worker=%s mode=%s pid=%d (check `cmgr inbox %s` for result)\n", worker, mode, pid, slug)
	return nil
}

// hidden subcommand executed by the detached child process
func init() {
	// nothing — registered below via root in init order if needed
}

func detachedRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "_run-detached <slug> <worker> <message> <budget> <model> <mode>",
		Hidden: true,
		Args:   cobra.RangeArgs(5, 6),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, worker, message := args[0], args[1], args[2]
			var budget float64
			fmt.Sscanf(args[3], "%f", &budget)
			model := args[4]
			modeArg := ""
			if len(args) >= 6 {
				modeArg = args[5]
			}

			w, err := store.LoadWorker(slug, worker)
			if err != nil {
				return err
			}
			cfg, _ := config.Load()
			permMode := resolveMode(modeArg, w.DefaultMode, cfg.DefaultPermission)

			lock, err := runner.Acquire(store.WorkerLockFile(slug, worker))
			if err != nil {
				appendInbox(slug, InboxEvent{Worker: worker, Timestamp: time.Now().UTC(), Type: "error", Error: "lock contention"})
				return err
			}
			defer lock.Release()

			w.Status = store.StatusRunning
			now := time.Now().UTC()
			w.LastRunAt = &now
			_ = store.SaveWorker(slug, w)

			start := time.Now()
			res, runErr := runner.Run(runner.Options{
				Prompt:         message,
				SessionID:      w.SessionID,
				Resume:         w.Initialized,
				Name:           w.Name,
				Cwd:            w.RepoPath,
				AllowedTools:   w.AllowedTools,
				Model:          model,
				BudgetUSD:      budget,
				PermissionMode: permMode,
				LogPath:        store.WorkerLogFile(slug, w.Name),
			})
			elapsed := time.Since(start)

			ev := InboxEvent{Worker: worker, Timestamp: time.Now().UTC(), DurationMS: elapsed.Milliseconds()}
			if runErr != nil && res == nil {
				ev.Type = "error"
				ev.Error = runErr.Error()
				w.Status = store.StatusError
				w.LastError = runErr.Error()
			} else if res.IsError {
				ev.Type = "error"
				ev.Error = res.Text
				ev.CostUSD = res.TotalCostUSD
				w.Status = store.StatusError
				w.LastError = res.Text
			} else if permMode == "plan" && strings.TrimSpace(res.Text) != "" {
				planPath, err := capturePlan(slug, worker, res.Text)
				if err == nil {
					w.PendingPlan = planPath
					nowP := time.Now().UTC()
					w.PendingPlanAt = &nowP
					w.Status = store.StatusPlanPending
					ev.Type = "plan_proposed"
					ev.PlanPath = planPath
				} else {
					ev.Type = "completed"
					w.Status = store.StatusIdle
				}
				ev.Result = res.Text
				ev.CostUSD = res.TotalCostUSD
				w.LastError = ""
				w.Initialized = true
				_ = os.WriteFile(store.WorkerLastFile(slug, worker), []byte(res.Text), 0o644)
			} else {
				ev.Type = "completed"
				ev.Result = res.Text
				ev.CostUSD = res.TotalCostUSD
				w.Status = store.StatusIdle
				w.LastError = ""
				w.Initialized = true
				_ = os.WriteFile(store.WorkerLastFile(slug, worker), []byte(res.Text), 0o644)
			}
			_ = store.SaveWorker(slug, w)
			appendInbox(slug, ev)
			return nil
		},
	}
}

func appendInbox(slug string, ev InboxEvent) {
	f, err := os.OpenFile(store.InboxFile(slug), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	f.Write(append(b, '\n'))
}
