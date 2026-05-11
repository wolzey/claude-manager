package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/wolzey/claude-manager/internal/config"
	"github.com/wolzey/claude-manager/internal/store"
)

func workerCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "worker",
		Aliases: []string{"w"},
		Short:   "Manage workers (per-repo sessions)",
	}
	c.AddCommand(workerAddCmd(), workerLsCmd(), workerShowCmd(), workerRmCmd())
	return c
}

func workerAddCmd() *cobra.Command {
	var repo, allowedTools, model, mode string
	c := &cobra.Command{
		Use:   "add <project> <name>",
		Short: "Register a worker against a repo",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := store.Slugify(args[0])
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if allowedTools == "" {
				allowedTools = cfg.DefaultAllowedTools
			}
			if mode == "" {
				mode = cfg.DefaultWorkerMode
			}
			if err := validateMode(mode); err != nil {
				return err
			}
			w, err := store.CreateWorker(slug, args[1], repo, allowedTools, model, mode)
			if err != nil {
				return err
			}
			fmt.Printf("added worker %q to project %q\n", w.Name, slug)
			fmt.Printf("  session_id:    %s\n", w.SessionID)
			fmt.Printf("  repo_path:     %s\n", w.RepoPath)
			fmt.Printf("  allowed_tools: %s\n", w.AllowedTools)
			fmt.Printf("  default_mode:  %s\n", w.DefaultMode)
			return nil
		},
	}
	c.Flags().StringVar(&repo, "repo", "", "absolute path to the repo this worker operates in (required)")
	c.Flags().StringVar(&allowedTools, "allowed-tools", "", "claude --allowed-tools value; defaults from config.yaml")
	c.Flags().StringVar(&model, "model", "", "claude --model value (e.g. opus, sonnet)")
	c.Flags().StringVar(&mode, "mode", "", "default permission mode: plan | acceptEdits | readonly (default 'plan' from config)")
	_ = c.MarkFlagRequired("repo")
	return c
}

// validateMode rejects unknown permission modes. Accepts the three modes we
// expose through cmgr; anything else gets surfaced as an error before we
// hand it to claude -p.
func validateMode(m string) error {
	switch m {
	case "plan", "acceptEdits", "readonly", "default", "":
		return nil
	}
	return fmt.Errorf("invalid mode %q: must be one of plan | acceptEdits | readonly | default", m)
}

func workerLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls <project>",
		Short: "List workers in a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := store.Slugify(args[0])
			workers, err := store.ListWorkers(slug)
			if err != nil {
				return err
			}
			if len(workers) == 0 {
				fmt.Println("(no workers)")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSTATUS\tSESSION\tREPO")
			for _, x := range workers {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", x.Name, x.Status, x.SessionID[:8], x.RepoPath)
			}
			return w.Flush()
		},
	}
}

func workerShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <project> <name>",
		Short: "Show a worker's full record",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := store.Slugify(args[0])
			w, err := store.LoadWorker(slug, args[1])
			if err != nil {
				return err
			}
			fmt.Printf("name:          %s\n", w.Name)
			fmt.Printf("session_id:    %s\n", w.SessionID)
			fmt.Printf("repo_path:     %s\n", w.RepoPath)
			fmt.Printf("allowed_tools: %s\n", w.AllowedTools)
			fmt.Printf("model:         %s\n", w.Model)
			fmt.Printf("default_mode:  %s\n", w.DefaultMode)
			fmt.Printf("status:        %s\n", w.Status)
			if w.LastRunAt != nil {
				fmt.Printf("last_run_at:   %s\n", w.LastRunAt.Format("2006-01-02 15:04:05"))
			}
			if w.LastError != "" {
				fmt.Printf("last_error:    %s\n", w.LastError)
			}
			if w.PendingPlan != "" {
				fmt.Printf("pending_plan:  %s\n", w.PendingPlan)
			}
			fmt.Printf("created_at:    %s\n", w.CreatedAt.Format("2006-01-02 15:04:05"))
			fmt.Printf("log:           %s\n", store.WorkerLogFile(slug, w.Name))
			return nil
		},
	}
}

func workerRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <project> <name>",
		Short: "Remove a worker (does not delete its claude session history)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := store.Slugify(args[0])
			if err := store.DeleteWorker(slug, args[1]); err != nil {
				return err
			}
			fmt.Printf("removed worker %q from project %q\n", args[1], slug)
			return nil
		},
	}
}
