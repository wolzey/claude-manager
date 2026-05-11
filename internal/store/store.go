package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func Root() string {
	if v := os.Getenv("CMGR_ROOT"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "manager")
}

func ProjectsDir() string { return filepath.Join(Root(), "projects") }

func ProjectDir(slug string) string { return filepath.Join(ProjectsDir(), slug) }

func WorkersDir(slug string) string { return filepath.Join(ProjectDir(slug), "workers") }

func LogsDir(slug string) string { return filepath.Join(ProjectDir(slug), "logs") }

func LocksDir(slug string) string { return filepath.Join(ProjectDir(slug), "locks") }

func PlansDir(slug string) string { return filepath.Join(ProjectDir(slug), "plans") }

func PlanFile(slug, worker, timestamp string) string {
	return filepath.Join(PlansDir(slug), worker+"-"+timestamp+".md")
}

func ProjectFile(slug string) string { return filepath.Join(ProjectDir(slug), "project.json") }

func ContractFile(slug string) string { return filepath.Join(ProjectDir(slug), "contract.md") }

func InboxFile(slug string) string { return filepath.Join(ProjectDir(slug), "inbox.jsonl") }

func WorkerFile(slug, name string) string {
	return filepath.Join(WorkersDir(slug), name+".json")
}

func WorkerLogFile(slug, name string) string {
	return filepath.Join(LogsDir(slug), name+".jsonl")
}

func WorkerLastFile(slug, name string) string {
	return filepath.Join(LogsDir(slug), name+".last.txt")
}

func WorkerLockFile(slug, name string) string {
	return filepath.Join(LocksDir(slug), name+".lock")
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func Slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

func WriteJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

func ReadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func PathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func RequireProject(slug string) error {
	if !PathExists(ProjectFile(slug)) {
		return fmt.Errorf("project %q not found (looked in %s)", slug, ProjectDir(slug))
	}
	return nil
}
