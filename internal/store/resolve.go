package store

import (
	"os"
	"path/filepath"
)

// DirName is the folder name of a project store inside a repo.
const DirName = ".jot"

// GlobalDir is $JOT_HOME, or ~/.jot.
func GlobalDir() string {
	if d := os.Getenv("JOT_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return DirName
	}
	return filepath.Join(home, DirName)
}

// FindProjectDir walks up from start looking for a .jot folder, stopping at
// the global store so ~/.jot is never mistaken for a project.
func FindProjectDir(start string) (string, bool) {
	global, _ := filepath.Abs(GlobalDir())
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		cand := filepath.Join(dir, DirName)
		if cand != global {
			if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
				return cand, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// Stores returns the project store (when one is found from cwd) and the
// global store. Project comes first.
func Stores(cwd string) []*Store {
	var out []*Store
	if p, ok := FindProjectDir(cwd); ok {
		out = append(out, Open(p, "project"))
	}
	return append(out, Open(GlobalDir(), "global"))
}
