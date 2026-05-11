package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/wolzey/claude-manager/internal/config"
	"github.com/wolzey/claude-manager/internal/store"
)

const approvalPromptTmpl = "APPROVED. Execute the plan you just proposed. If anything in the plan now seems wrong given the latest project state, stop and report instead of guessing."

const rejectionPromptTmpl = "Plan rejected with the following feedback. Revise your plan and re-propose; do not make changes.\n\nFeedback:\n%s"

func planCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "plan",
		Short: "List, review, approve, or reject worker plans",
	}
	c.AddCommand(planListCmd(), planShowCmd(), planApproveCmd(), planRejectCmd(), planHistoryCmd())
	return c
}

func planListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <project>",
		Short: "List workers with pending plans",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := store.Slugify(args[0])
			workers, err := store.ListWorkers(slug)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "WORKER\tPENDING_SINCE\tPLAN")
			found := false
			for _, w := range workers {
				if w.PendingPlan == "" {
					continue
				}
				found = true
				since := "-"
				if w.PendingPlanAt != nil {
					since = w.PendingPlanAt.Format("2006-01-02 15:04")
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", w.Name, since, w.PendingPlan)
			}
			tw.Flush()
			if !found {
				fmt.Println("(no pending plans)")
			}
			return nil
		},
	}
}

func planShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <project> <worker>",
		Short: "Print the current pending plan for a worker",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := store.Slugify(args[0])
			w, err := store.LoadWorker(slug, args[1])
			if err != nil {
				return err
			}
			if w.PendingPlan == "" {
				return fmt.Errorf("worker %q has no pending plan", w.Name)
			}
			abs := filepath.Join(store.ProjectDir(slug), w.PendingPlan)
			b, err := os.ReadFile(abs)
			if err != nil {
				return err
			}
			fmt.Println(string(b))
			return nil
		},
	}
}

func planApproveCmd() *cobra.Command {
	var withText string
	var persist bool
	c := &cobra.Command{
		Use:   "approve <project> <worker>",
		Short: "Execute the worker's pending plan with a one-shot --mode acceptEdits override",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := store.Slugify(args[0])
			w, err := store.LoadWorker(slug, args[1])
			if err != nil {
				return err
			}
			if w.PendingPlan == "" {
				return fmt.Errorf("worker %q has no pending plan to approve", w.Name)
			}
			cfg, _ := config.Load()
			if cfg != nil && cfg.ApprovalPersists {
				persist = true
			}
			prompt := approvalPromptTmpl
			if strings.TrimSpace(withText) != "" {
				prompt += "\n\nAdditional context: " + withText
			}
			if err := runSend(slug, w.Name, prompt, false, 0, "", "acceptEdits"); err != nil {
				return err
			}
			if persist {
				w2, _ := store.LoadWorker(slug, w.Name)
				if w2 != nil {
					w2.DefaultMode = "acceptEdits"
					_ = store.SaveWorker(slug, w2)
					fmt.Fprintf(os.Stderr, "[cmgr] worker default_mode flipped to acceptEdits (--persist)\n")
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&withText, "with", "", "extra context to append to the approval prompt")
	c.Flags().BoolVar(&persist, "persist", false, "also flip worker.default_mode to acceptEdits (defaults from config.approval_persists)")
	return c
}

func planRejectCmd() *cobra.Command {
	var feedback string
	c := &cobra.Command{
		Use:   "reject <project> <worker>",
		Short: "Send feedback to a worker and ask for a revised plan",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(feedback) == "" {
				return fmt.Errorf("--feedback is required")
			}
			slug := store.Slugify(args[0])
			w, err := store.LoadWorker(slug, args[1])
			if err != nil {
				return err
			}
			if w.PendingPlan == "" {
				return fmt.Errorf("worker %q has no pending plan to reject", w.Name)
			}
			prompt := fmt.Sprintf(rejectionPromptTmpl, feedback)
			return runSend(slug, w.Name, prompt, false, 0, "", "plan")
		},
	}
	c.Flags().StringVar(&feedback, "feedback", "", "feedback to send to the worker (required)")
	_ = c.MarkFlagRequired("feedback")
	return c
}

func planHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history <project> <worker>",
		Short: "List past plans saved for a worker",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := store.Slugify(args[0])
			w, err := store.LoadWorker(slug, args[1])
			if err != nil {
				return err
			}
			plansDir := store.PlansDir(slug)
			entries, err := os.ReadDir(plansDir)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Println("(no plans yet)")
					return nil
				}
				return err
			}
			type planEntry struct {
				path string
				mod  time.Time
			}
			matches := []planEntry{}
			prefix := w.Name + "-"
			for _, e := range entries {
				if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ".md") {
					continue
				}
				info, _ := e.Info()
				matches = append(matches, planEntry{path: e.Name(), mod: info.ModTime()})
			}
			if len(matches) == 0 {
				fmt.Println("(no plans yet)")
				return nil
			}
			sort.Slice(matches, func(i, j int) bool { return matches[i].mod.After(matches[j].mod) })
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "WHEN\tFILE\tCURRENT")
			for _, m := range matches {
				rel := filepath.Join("plans", m.path)
				current := ""
				if rel == w.PendingPlan {
					current = "← pending"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", m.mod.Format("2006-01-02 15:04"), rel, current)
			}
			return tw.Flush()
		},
	}
}
