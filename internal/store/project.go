package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Project struct {
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func CreateProject(name, description string) (*Project, error) {
	slug := Slugify(name)
	if slug == "" {
		return nil, fmt.Errorf("invalid project name %q", name)
	}
	if PathExists(ProjectFile(slug)) {
		return nil, fmt.Errorf("project %q already exists at %s", slug, ProjectDir(slug))
	}
	for _, d := range []string{ProjectDir(slug), WorkersDir(slug), LogsDir(slug), LocksDir(slug)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	p := &Project{Name: name, Slug: slug, Description: description, CreatedAt: time.Now().UTC()}
	if err := WriteJSONAtomic(ProjectFile(slug), p); err != nil {
		return nil, err
	}
	if !PathExists(ContractFile(slug)) {
		header := fmt.Sprintf("# %s — Contract\n\n%s\n\n## Decisions\n\n_(empty)_\n\n## API / Event Shapes\n\n_(empty)_\n", name, description)
		if err := os.WriteFile(ContractFile(slug), []byte(header), 0o644); err != nil {
			return nil, err
		}
	}
	if !PathExists(InboxFile(slug)) {
		if err := os.WriteFile(InboxFile(slug), nil, 0o644); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func LoadProject(slug string) (*Project, error) {
	if err := RequireProject(slug); err != nil {
		return nil, err
	}
	var p Project
	if err := ReadJSON(ProjectFile(slug), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func ListProjects() ([]*Project, error) {
	root := ProjectsDir()
	if !PathExists(root) {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []*Project
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pf := filepath.Join(root, e.Name(), "project.json")
		if !PathExists(pf) {
			continue
		}
		var p Project
		if err := ReadJSON(pf, &p); err != nil {
			continue
		}
		out = append(out, &p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func DeleteProject(slug string) error {
	if err := RequireProject(slug); err != nil {
		return err
	}
	return os.RemoveAll(ProjectDir(slug))
}
