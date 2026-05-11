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
	c.AddCommand(workerAddCmd(), workerLsCmd(), workerShowCmd(), workerRmCmd(), workerSetCmd(), workerElevateCmd())
	return c
}

func workerAddCmd() *cobra.Command {
	var repo, allowedTools, model string
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
			w, err := store.CreateWorker(slug, args[1], repo, allowedTools, model)
			if err != nil {
				return err
			}
			fmt.Printf("added worker %q to project %q\n", w.Name, slug)
			fmt.Printf("  session_id:    %s\n", w.SessionID)
			fmt.Printf("  repo_path:     %s\n", w.RepoPath)
			fmt.Printf("  allowed_tools: %s\n", w.AllowedTools)
			return nil
		},
	}
	c.Flags().StringVar(&repo, "repo", "", "absolute path to the repo this worker operates in (required)")
	c.Flags().StringVar(&allowedTools, "allowed-tools", "", "claude --allowed-tools value; defaults from config.yaml")
	c.Flags().StringVar(&model, "model", "", "claude --model value (e.g. opus, sonnet)")
	_ = c.MarkFlagRequired("repo")
	return c
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
			fmt.Printf("name:            %s\n", w.Name)
			fmt.Printf("session_id:      %s\n", w.SessionID)
			fmt.Printf("repo_path:       %s\n", w.RepoPath)
			fmt.Printf("allowed_tools:   %s\n", w.AllowedTools)
			fmt.Printf("permission_mode: %s\n", w.PermissionMode)
			fmt.Printf("model:           %s\n", w.Model)
			fmt.Printf("status:          %s\n", w.Status)
			if w.LastRunAt != nil {
				fmt.Printf("last_run_at:     %s\n", w.LastRunAt.Format("2006-01-02 15:04:05"))
			}
			if w.LastError != "" {
				fmt.Printf("last_error:      %s\n", w.LastError)
			}
			fmt.Printf("created_at:      %s\n", w.CreatedAt.Format("2006-01-02 15:04:05"))
			fmt.Printf("log:             %s\n", store.WorkerLogFile(slug, w.Name))
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

// validPermissionModes is enforced by `worker elevate`. `worker set` is more
// permissive — it accepts any string so we don't block users when claude
// introduces a new mode.
var validPermissionModes = map[string]bool{
	"default":           true,
	"acceptEdits":       true,
	"plan":              true,
	"bypassPermissions": true,
}

func workerSetCmd() *cobra.Command {
	var allowedTools, permMode, model string
	c := &cobra.Command{
		Use:   "set <project> <name>",
		Short: "Update an existing worker's allowed tools, permission mode, or model",
		Long: `Update an existing worker without losing its session history.
Only fields explicitly passed via flags are changed; unset flags leave the
current value alone.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := store.Slugify(args[0])
			w, err := store.LoadWorker(slug, args[1])
			if err != nil {
				return err
			}

			changed := false
			if cmd.Flags().Changed("allowed-tools") {
				w.AllowedTools = allowedTools
				changed = true
			}
			if cmd.Flags().Changed("permission-mode") {
				w.PermissionMode = permMode
				changed = true
			}
			if cmd.Flags().Changed("model") {
				w.Model = model
				changed = true
			}
			if !changed {
				return fmt.Errorf("no changes requested; pass at least one of --allowed-tools, --permission-mode, --model")
			}

			if err := store.SaveWorker(slug, w); err != nil {
				return err
			}
			fmt.Printf("updated worker %q in project %q\n", w.Name, slug)
			fmt.Printf("  allowed_tools:   %s\n", w.AllowedTools)
			fmt.Printf("  permission_mode: %s\n", w.PermissionMode)
			fmt.Printf("  model:           %s\n", w.Model)
			return nil
		},
	}
	c.Flags().StringVar(&allowedTools, "allowed-tools", "", "claude --allowed-tools value (pass empty string to clear)")
	c.Flags().StringVar(&permMode, "permission-mode", "", "claude --permission-mode value: default, acceptEdits, plan, bypassPermissions")
	c.Flags().StringVar(&model, "model", "", "claude --model value (e.g. opus, sonnet)")
	return c
}

func workerElevateCmd() *cobra.Command {
	var mode, allowedTools string
	c := &cobra.Command{
		Use:   "elevate <project> <name>",
		Short: "Grant a worker elevated permissions (shortcut for `worker set`)",
		Long: `Grant a worker elevated permissions in one shot. Defaults to
permission_mode=bypassPermissions, which disables ALL permission prompts in
the worker session — use with care.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !validPermissionModes[mode] {
				return fmt.Errorf("invalid --mode %q (want one of: default, acceptEdits, plan, bypassPermissions)", mode)
			}
			slug := store.Slugify(args[0])
			w, err := store.LoadWorker(slug, args[1])
			if err != nil {
				return err
			}
			w.PermissionMode = mode
			if cmd.Flags().Changed("allowed-tools") {
				w.AllowedTools = allowedTools
			}
			if err := store.SaveWorker(slug, w); err != nil {
				return err
			}
			fmt.Printf("elevated worker %q in project %q\n", w.Name, slug)
			fmt.Printf("  permission_mode: %s\n", w.PermissionMode)
			fmt.Printf("  allowed_tools:   %s\n", w.AllowedTools)
			if mode == "bypassPermissions" {
				fmt.Fprintln(os.Stderr, "[cmgr] warning: bypassPermissions disables ALL prompts; the worker can run any tool without confirmation.")
			}
			return nil
		},
	}
	c.Flags().StringVar(&mode, "mode", "bypassPermissions", "permission mode: default, acceptEdits, plan, bypassPermissions")
	c.Flags().StringVar(&allowedTools, "allowed-tools", "", "optionally also widen --allowed-tools")
	return c
}
