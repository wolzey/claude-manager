package cmd

import (
	"fmt"
	"sync"

	"github.com/spf13/cobra"
	"github.com/wolzey/claude-manager/internal/store"
)

func broadcastCmd() *cobra.Command {
	var detach bool
	var budget float64
	var model string
	c := &cobra.Command{
		Use:   "broadcast <project> <message>",
		Short: "Send the same message to all workers in a project",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := store.Slugify(args[0])
			workers, err := store.ListWorkers(slug)
			if err != nil {
				return err
			}
			if len(workers) == 0 {
				return fmt.Errorf("project %q has no workers", slug)
			}

			if detach {
				for _, w := range workers {
					if err := runDetached(slug, w.Name, args[1], budget, model); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "dispatch %s: %v\n", w.Name, err)
					}
				}
				return nil
			}

			var wg sync.WaitGroup
			var mu sync.Mutex
			errs := []string{}
			for _, w := range workers {
				w := w
				wg.Add(1)
				go func() {
					defer wg.Done()
					fmt.Printf("\n=== %s ===\n", w.Name)
					if err := runSend(slug, w.Name, args[1], false, budget, model); err != nil {
						mu.Lock()
						errs = append(errs, fmt.Sprintf("%s: %v", w.Name, err))
						mu.Unlock()
					}
				}()
			}
			wg.Wait()
			if len(errs) > 0 {
				return fmt.Errorf("broadcast errors: %v", errs)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&detach, "detach", false, "fan out asynchronously; results land in inbox.jsonl")
	c.Flags().Float64Var(&budget, "budget", 0, "max-budget-usd cap per worker (0 = use config default)")
	c.Flags().StringVar(&model, "model", "", "override model for this broadcast")
	return c
}
