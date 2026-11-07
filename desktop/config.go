package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// prefs is what the desktop app remembers between launches. It lives in
// the OS config folder, never inside a memory store.
type prefs struct {
	Onboarded     bool     `json:"onboarded"`
	Projects      []string `json:"projects"`                // folders that hold a .jot store
	Dismissed     []string `json:"dismissed"`               // duplicate pairs marked "keep both"
	Theme         string   `json:"theme,omitempty"`         // "", "light" or "dark"; "" follows the system
	SkipVersion   string   `json:"skipVersion,omitempty"`   // update banner hidden for this version
	NoUpdateCheck bool     `json:"noUpdateCheck,omitempty"` // don't look for updates on launch
	AskModel      string   `json:"askModel,omitempty"`      // model last picked in Ask

	path string
	mu   sync.Mutex
}

func prefsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "jot", "desktop.json")
}

func loadPrefs(path string) *prefs {
	p := &prefs{path: path}
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, p)
	}
	return p
}

func (p *prefs) save() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p.path, append(data, '\n'), 0o644)
}
