package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/wolzey/claude-manager/internal/store"
)

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <project>",
		Short: "Show worker status for a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := store.Slugify(args[0])
			p, err := store.LoadProject(slug)
			if err != nil {
				return err
			}
			workers, err := store.ListWorkers(slug)
			if err != nil {
				return err
			}
			fmt.Printf("project: %s (%s)\n", p.Name, p.Slug)
			if len(workers) == 0 {
				fmt.Println("(no workers)")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "WORKER\tSTATUS\tLAST_RUN\tLOCKED\tREPO")
			for _, x := range workers {
				lastRun := "-"
				if x.LastRunAt != nil {
					lastRun = x.LastRunAt.Format("2006-01-02 15:04")
				}
				locked := "no"
				if store.PathExists(store.WorkerLockFile(slug, x.Name)) {
					locked = "yes"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", x.Name, x.Status, lastRun, locked, x.RepoPath)
			}
			return w.Flush()
		},
	}
}
