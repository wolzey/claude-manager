package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/wolzey/claude-manager/internal/store"
)

func projectCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "project",
		Short: "Manage projects",
	}
	c.AddCommand(projectNewCmd(), projectLsCmd(), projectShowCmd(), projectRmCmd())
	return c
}

func projectNewCmd() *cobra.Command {
	var description string
	var noEdit bool
	c := &cobra.Command{
		Use:   "new <name>",
		Short: "Create a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := store.CreateProject(args[0], description)
			if err != nil {
				return err
			}
			fmt.Printf("created project %q (slug: %s)\n", p.Name, p.Slug)
			fmt.Printf("  dir:      %s\n", store.ProjectDir(p.Slug))
			fmt.Printf("  contract: %s\n", store.ContractFile(p.Slug))
			if !noEdit {
				openInEditor(store.ContractFile(p.Slug))
			}
			return nil
		},
	}
	c.Flags().StringVar(&description, "description", "", "project description / one-liner")
	c.Flags().BoolVar(&noEdit, "no-edit", false, "don't open $EDITOR on contract.md")
	return c
}

func projectLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			projects, err := store.ListProjects()
			if err != nil {
				return err
			}
			if len(projects) == 0 {
				fmt.Println("(no projects)")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "SLUG\tNAME\tCREATED\tDESCRIPTION")
			for _, p := range projects {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Slug, p.Name, p.CreatedAt.Format("2006-01-02"), p.Description)
			}
			return w.Flush()
		},
	}
}

func projectShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name-or-slug>",
		Short: "Show a project",
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
			fmt.Printf("project:     %s (%s)\n", p.Name, p.Slug)
			fmt.Printf("description: %s\n", p.Description)
			fmt.Printf("created:     %s\n", p.CreatedAt.Format("2006-01-02 15:04:05"))
			fmt.Printf("dir:         %s\n", store.ProjectDir(p.Slug))
			fmt.Printf("contract:    %s\n", store.ContractFile(p.Slug))
			fmt.Printf("workers:     %d\n", len(workers))
			for _, w := range workers {
				fmt.Printf("  - %s  [%s]  %s\n", w.Name, w.Status, w.RepoPath)
			}
			return nil
		},
	}
}

func projectRmCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "rm <name-or-slug>",
		Short: "Delete a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := store.Slugify(args[0])
			if _, err := store.LoadProject(slug); err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf("refusing to delete %q without --yes", slug)
			}
			if err := store.DeleteProject(slug); err != nil {
				return err
			}
			fmt.Printf("deleted project %q\n", slug)
			return nil
		},
	}
	c.Flags().BoolVar(&yes, "yes", false, "confirm deletion (required, non-interactive)")
	return c
}

func openInEditor(path string) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}
