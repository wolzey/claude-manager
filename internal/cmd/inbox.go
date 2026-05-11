package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wolzey/claude-manager/internal/store"
)

func inboxCmd() *cobra.Command {
	var consume bool
	var format string
	c := &cobra.Command{
		Use:   "inbox <project>",
		Short: "Show pending detached worker completions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := store.Slugify(args[0])
			if err := store.RequireProject(slug); err != nil {
				return err
			}
			path := store.InboxFile(slug)
			f, err := os.Open(path)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Println("(empty)")
					return nil
				}
				return err
			}
			events := []InboxEvent{}
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
			for scanner.Scan() {
				var ev InboxEvent
				if err := json.Unmarshal(scanner.Bytes(), &ev); err == nil {
					events = append(events, ev)
				}
			}
			f.Close()

			if len(events) == 0 {
				fmt.Println("(empty)")
				if consume {
					_ = os.Truncate(path, 0)
				}
				return nil
			}

			switch format {
			case "json":
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				_ = enc.Encode(events)
			default:
				for _, ev := range events {
					fmt.Printf("---\nworker:   %s\ntype:     %s\ntime:     %s\nduration: %dms\ncost:     $%.4f\n",
						ev.Worker, ev.Type, ev.Timestamp.Format("2006-01-02 15:04:05"), ev.DurationMS, ev.CostUSD)
					if ev.Error != "" {
						fmt.Printf("error:    %s\n", ev.Error)
					}
					if ev.Result != "" {
						fmt.Printf("result:\n%s\n", ev.Result)
					}
				}
			}

			if consume {
				if err := os.Truncate(path, 0); err != nil {
					return err
				}
				fmt.Fprintln(os.Stderr, "[cmgr] inbox consumed")
			}
			return nil
		},
	}
	c.Flags().BoolVar(&consume, "consume", false, "truncate the inbox after printing")
	c.Flags().StringVar(&format, "format", "text", "text | json")
	return c
}
