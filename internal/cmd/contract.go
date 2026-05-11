package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wolzey/claude-manager/internal/store"
)

func contractCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "contract",
		Short: "Show or edit a project's contract.md (the shared blackboard)",
	}
	c.AddCommand(
		&cobra.Command{
			Use:   "show <project>",
			Short: "Print contract.md",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				slug := store.Slugify(args[0])
				if err := store.RequireProject(slug); err != nil {
					return err
				}
				b, err := os.ReadFile(store.ContractFile(slug))
				if err != nil {
					return err
				}
				_, err = os.Stdout.Write(b)
				return err
			},
		},
		&cobra.Command{
			Use:   "edit <project>",
			Short: "Open contract.md in $EDITOR",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				slug := store.Slugify(args[0])
				if err := store.RequireProject(slug); err != nil {
					return err
				}
				openInEditor(store.ContractFile(slug))
				return nil
			},
		},
		&cobra.Command{
			Use:   "path <project>",
			Short: "Print the absolute path to contract.md",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				slug := store.Slugify(args[0])
				if err := store.RequireProject(slug); err != nil {
					return err
				}
				fmt.Println(store.ContractFile(slug))
				return nil
			},
		},
	)
	return c
}
