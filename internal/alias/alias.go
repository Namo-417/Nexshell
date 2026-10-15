// Package alias implements a simple in-memory alias store for NexShell.
// Aliases are short-names that expand to longer command strings before
// dispatch, e.g.  alias ll="ls -l"
package alias

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Store holds name → value alias mappings.
type Store struct {
	m map[string]string
}

// New returns an empty alias store.
func New() *Store {
	return &Store{m: make(map[string]string)}
}

// Set adds or overwrites alias name with the given value.
// Both name and value are trimmed of surrounding whitespace.
func (s *Store) Set(name, value string) {
	s.m[strings.TrimSpace(name)] = strings.TrimSpace(value)
}

// Get returns the value for name and true, or "", false if not found.
func (s *Store) Get(name string) (string, bool) {
	v, ok := s.m[strings.TrimSpace(name)]
	return v, ok
}

// Delete removes name from the store. No-op if absent.
func (s *Store) Delete(name string) {
	delete(s.m, strings.TrimSpace(name))
}

// All returns a snapshot of all aliases sorted by name.
func (s *Store) All() map[string]string {
	out := make(map[string]string, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out
}

// Expand checks whether the first token of input is an alias and, if so,
// returns the alias value followed by the remaining input tokens.
// If no alias matches, it returns input unchanged.
func (s *Store) Expand(input string) string {
	input = strings.TrimLeftFunc(input, unicode.IsSpace)
	if input == "" {
		return input
	}
	end := strings.IndexFunc(input, unicode.IsSpace)
	if end < 0 {
		end = len(input)
	}
	if val, ok := s.Get(input[:end]); ok {
		return val + input[end:]
	}
	return input
}

// FormatAll formats all aliases as printable lines suitable for `alias` output.
func (s *Store) FormatAll() []string {
	all := s.All()
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("alias %s='%s'", k, all[k]))
	}
	return lines
}
