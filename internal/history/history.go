// Package history implements command history for NexShell.
// History is kept in memory and persisted to disk (default: ~/.nexshell_history).
package history

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const defaultMaxEntries = 1000

// Store holds the command history.
type Store struct {
	mu      sync.Mutex
	entries []string
	maxSize int
	path    string // path to the history file
}

// New creates a Store with the default file path (~/.nexshell_history).
func New() *Store {
	home, err := os.UserHomeDir()
	path := ".nexshell_history"
	if err == nil {
		path = filepath.Join(home, ".nexshell_history")
	}
	return &Store{
		maxSize: defaultMaxEntries,
		path:    path,
	}
}

// NewWithPath creates a Store that uses path as the history file.
func NewWithPath(path string) *Store {
	return &Store{
		maxSize: defaultMaxEntries,
		path:    path,
	}
}

// Add appends line to history. Empty lines and duplicates of the last entry
// are silently dropped.
func (s *Store) Add(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) > 0 && s.entries[len(s.entries)-1] == line {
		return
	}
	s.entries = append(s.entries, line)
	if len(s.entries) > s.maxSize {
		s.entries = s.entries[len(s.entries)-s.maxSize:]
	}
}

// List returns a copy of all history entries in chronological order.
func (s *Store) List() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.entries))
	copy(out, s.entries)
	return out
}

// Save writes history to disk, creating the file if necessary.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Create(s.path)
	if err != nil {
		return fmt.Errorf("history save: %w", err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, e := range s.entries {
		if _, err := fmt.Fprintln(w, e); err != nil {
			return err
		}
	}
	return w.Flush()
}

// Load reads history from disk. If the file does not exist, Load is a no-op.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("history load: %w", err)
	}
	defer f.Close()

	var entries []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			entries = append(entries, line)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("history load: %w", err)
	}
	// Merge loaded entries before any entries added this session.
	s.entries = append(entries, s.entries...)
	if len(s.entries) > s.maxSize {
		s.entries = s.entries[len(s.entries)-s.maxSize:]
	}
	return nil
}

// Len returns the number of entries currently held.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
