package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
}

func sendCmd() *cobra.Command {
	var detach bool
	var budget float64
	var model string
	c := &cobra.Command{
		Use:   "send <project> <worker> <message>",
		Short: "Send a message to a worker (sync by default, --detach for async)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSend(args[0], args[1], args[2], detach, budget, model)
		},
	}
	c.Flags().BoolVar(&detach, "detach", false, "fire-and-forget; result lands in inbox.jsonl")
	c.Flags().Float64Var(&budget, "budget", 0, "max-budget-usd cap (0 = use config default)")
	c.Flags().StringVar(&model, "model", "", "override worker's default model")
	return c
}

func runSend(projectArg, workerName, message string, detach bool, budget float64, model string) error {
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

	if detach {
		return runDetached(slug, w.Name, message, budget, chosenModel)
	}

	return runForeground(slug, w, message, budget, chosenModel, cfg.DefaultPermission)
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
	fmt.Fprintf(os.Stderr, "\n[cmgr] worker=%s elapsed=%s cost=$%.4f stop_reason=%s\n",
		w.Name, elapsed.Round(time.Millisecond), res.TotalCostUSD, res.StopReason)
	if res.IsError {
		return fmt.Errorf("worker reported error")
	}
	return nil
}

func runDetached(slug, worker, message string, budget float64, model string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"_run-detached", slug, worker, message, fmt.Sprintf("%.4f", budget), model}
	cmd := exec.Command(exe, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	fmt.Printf("dispatched worker=%s pid=%d (check `cmgr inbox %s` for result)\n", worker, pid, slug)
	return nil
}

// hidden subcommand executed by the detached child process
func init() {
	// nothing — registered below via root in init order if needed
}

func detachedRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "_run-detached <slug> <worker> <message> <budget> <model>",
		Hidden: true,
		Args:   cobra.ExactArgs(5),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, worker, message := args[0], args[1], args[2]
			var budget float64
			fmt.Sscanf(args[3], "%f", &budget)
			model := args[4]

			w, err := store.LoadWorker(slug, worker)
			if err != nil {
				return err
			}
			cfg, _ := config.Load()
			permMode := cfg.DefaultPermission

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
