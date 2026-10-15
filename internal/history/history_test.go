package history_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nexshell/internal/history"
)

func tempPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "test_history")
}

func TestAdd_And_List(t *testing.T) {
	s := history.NewWithPath(tempPath(t))
	s.Add("echo hello")
	s.Add("pwd")
	s.Add("ls")

	entries := s.List()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0] != "echo hello" {
		t.Fatalf("got %q want %q", entries[0], "echo hello")
	}
	if entries[2] != "ls" {
		t.Fatalf("got %q want %q", entries[2], "ls")
	}
}

func TestAdd_Empty_Ignored(t *testing.T) {
	s := history.NewWithPath(tempPath(t))
	s.Add("")
	s.Add("   ")
	if s.Len() != 0 {
		t.Fatalf("expected 0 entries after adding blanks, got %d", s.Len())
	}
}

func TestAdd_Deduplicates_Consecutive(t *testing.T) {
	s := history.NewWithPath(tempPath(t))
	s.Add("ls")
	s.Add("ls")
	s.Add("ls")
	if s.Len() != 1 {
		t.Fatalf("expected 1 deduplicated entry, got %d", s.Len())
	}
}

func TestAdd_NonConsecutiveDupe_Allowed(t *testing.T) {
	s := history.NewWithPath(tempPath(t))
	s.Add("ls")
	s.Add("pwd")
	s.Add("ls")
	if s.Len() != 3 {
		t.Fatalf("expected 3 entries, got %d", s.Len())
	}
}

func TestSaveAndLoad(t *testing.T) {
	path := tempPath(t)
	s1 := history.NewWithPath(path)
	s1.Add("cmd1")
	s1.Add("cmd2")
	if err := s1.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	s2 := history.NewWithPath(path)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	entries := s2.List()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries after Load, got %d: %v", len(entries), entries)
	}
	if entries[0] != "cmd1" || entries[1] != "cmd2" {
		t.Fatalf("unexpected entries: %v", entries)
	}
}

func TestLoad_MissingFile_IsNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does_not_exist")
	s := history.NewWithPath(path)
	if err := s.Load(); err != nil {
		t.Fatalf("expected no error loading missing file, got: %v", err)
	}
}

func TestLoad_Skips_BlankLines(t *testing.T) {
	path := tempPath(t)
	if err := os.WriteFile(path, []byte("\ncmd1\n\ncmd2\n\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s := history.NewWithPath(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", s.Len())
	}
}

func TestHistoryLimitAndLongEntries(t *testing.T) {
	path := tempPath(t)
	s := history.NewWithPath(path)
	for i := 0; i < 1200; i++ {
		s.Add(fmt.Sprintf("command-%d", i))
	}
	if s.Len() != 1000 || s.List()[0] != "command-200" {
		t.Fatalf("count=%d first=%q", s.Len(), s.List()[0])
	}
	long := strings.Repeat("x", 70000)
	s.Add(long)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	loaded := history.NewWithPath(path)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	entries := loaded.List()
	if len(entries) != 1000 || entries[len(entries)-1] != long {
		t.Fatalf("loaded=%d last length=%d", len(entries), len(entries[len(entries)-1]))
	}
	copyOfEntries := loaded.List()
	copyOfEntries[0] = "changed"
	if loaded.List()[0] == "changed" {
		t.Fatal("List exposes internal slice")
	}
}
