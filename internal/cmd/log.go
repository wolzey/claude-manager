package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	"github.com/wolzey/claude-manager/internal/store"
)

func logCmd() *cobra.Command {
	var raw, follow bool
	var tail int
	c := &cobra.Command{
		Use:   "log <project> <worker>",
		Short: "Print a worker's most recent output (--raw for full stream, --follow to tail)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := store.Slugify(args[0])
			if _, err := store.LoadWorker(slug, args[1]); err != nil {
				return err
			}
			if follow {
				path := store.WorkerLogFile(slug, args[1])
				if !store.PathExists(path) {
					return fmt.Errorf("no log yet for %s/%s", slug, args[1])
				}
				tailCmd := exec.Command("tail", "-f", path)
				tailCmd.Stdout = os.Stdout
				tailCmd.Stderr = os.Stderr
				return tailCmd.Run()
			}
			if raw {
				path := store.WorkerLogFile(slug, args[1])
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				defer f.Close()
				_, err = io.Copy(os.Stdout, f)
				return err
			}
			path := store.WorkerLastFile(slug, args[1])
			if !store.PathExists(path) {
				return fmt.Errorf("no result yet for %s/%s", slug, args[1])
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			_, err = os.Stdout.Write(b)
			return err
		},
	}
	c.Flags().BoolVar(&raw, "raw", false, "dump the raw stream-json transcript instead of the final text")
	c.Flags().BoolVar(&follow, "follow", false, "tail -f the raw log")
	c.Flags().IntVar(&tail, "tail", 0, "(reserved) only with --raw")
	return c
}
