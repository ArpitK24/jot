package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrNotFound is returned when a memory id does not exist.
var ErrNotFound = errors.New("memory not found")

// Store is a directory of memory files, optionally a git repo.
type Store struct {
	Dir   string
	Scope string
	now   func() time.Time
}

// Open returns a store rooted at dir. It does not create anything.
func Open(dir, scope string) *Store {
	return &Store{Dir: dir, Scope: scope, now: time.Now}
}

// Init creates the directory and, when git is available, a git repo in it.
func (s *Store) Init() error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	if s.isGit() || !hasGit() {
		return nil
	}
	if out, err := s.git("init", "-q"); err != nil {
		return fmt.Errorf("git init: %v: %s", err, out)
	}
	return nil
}

// Exists reports whether the store directory exists.
func (s *Store) Exists() bool {
	fi, err := os.Stat(s.Dir)
	return err == nil && fi.IsDir()
}

// Add saves a new memory and returns it with id and timestamps filled in.
func (s *Store) Add(body, kind string, tags []string, source string) (*Memory, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, errors.New("memory body is empty")
	}
	if kind == "" {
		kind = KindNote
	}
	if err := s.Init(); err != nil {
		return nil, err
	}
	now := s.now().UTC().Truncate(time.Second)
	m := &Memory{
		ID:      newID(now),
		Kind:    kind,
		Tags:    NormalizeTags(tags),
		Source:  source,
		Created: now,
		Updated: now,
		Body:    body,
		Scope:   s.Scope,
	}
	if err := s.write(m); err != nil {
		return nil, err
	}
	s.commit(fmt.Sprintf("remember %s: %s", m.ID, m.Title()))
	return m, nil
}

// Update replaces the body and, when given, kind and tags of a memory.
func (s *Store) Update(id, body, kind string, tags []string) (*Memory, error) {
	m, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if b := strings.TrimSpace(body); b != "" {
		m.Body = b
	}
	if kind != "" {
		m.Kind = kind
	}
	if tags != nil {
		m.Tags = NormalizeTags(tags)
	}
	m.Updated = s.now().UTC().Truncate(time.Second)
	if err := s.write(m); err != nil {
		return nil, err
	}
	s.commit(fmt.Sprintf("update %s: %s", m.ID, m.Title()))
	return m, nil
}

// Delete removes a memory. Git history keeps it recoverable.
func (s *Store) Delete(id string) error {
	p, err := s.path(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); err != nil {
		return ErrNotFound
	}
	if err := os.Remove(p); err != nil {
		return err
	}
	s.commit("forget " + id)
	return nil
}

// Get loads one memory by id.
func (s *Store) Get(id string) (*Memory, error) {
	p, err := s.path(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if m.ID == "" {
		m.ID = id
	}
	m.Scope = s.Scope
	return m, nil
}

// List returns every memory, newest first. Unreadable files are skipped.
func (s *Store) List() ([]*Memory, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Memory
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || strings.HasPrefix(name, ".") {
			continue
		}
		m, err := s.Get(strings.TrimSuffix(name, ".md"))
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Updated.Equal(out[j].Updated) {
			return out[i].Updated.After(out[j].Updated)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// Log returns the store's git history, newest first.
func (s *Store) Log(n int) (string, error) {
	if !s.isGit() {
		return "", errors.New("store is not a git repo")
	}
	out, err := s.git("log", fmt.Sprintf("-%d", n), "--date=short", "--pretty=format:%h %ad %s")
	if err != nil {
		return "", fmt.Errorf("git log: %v: %s", err, out)
	}
	return out, nil
}

func (s *Store) write(m *Memory) error {
	p, err := s.path(m.ID)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, m.Marshal(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (s *Store) path(id string) (string, error) {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.HasPrefix(id, ".") {
		return "", fmt.Errorf("invalid memory id %q", id)
	}
	return filepath.Join(s.Dir, id+".md"), nil
}

func (s *Store) commit(msg string) {
	if !s.isGit() {
		return
	}
	s.git("add", "-A")
	s.git("-c", "user.name=jot", "-c", "user.email=jot@localhost", "commit", "-q", "-m", msg)
}

func (s *Store) isGit() bool {
	_, err := os.Stat(filepath.Join(s.Dir, ".git"))
	return err == nil
}

func (s *Store) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = s.Dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func hasGit() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func newID(t time.Time) string {
	b := make([]byte, 2)
	rand.Read(b)
	return t.Format("20060102-150405") + "-" + hex.EncodeToString(b)
}
