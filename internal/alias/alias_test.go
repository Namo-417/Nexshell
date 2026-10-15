package alias_test

import (
	"testing"

	"nexshell/internal/alias"
)

func TestSetAndGet(t *testing.T) {
	s := alias.New()
	s.Set("ll", "ls -l")

	val, ok := s.Get("ll")
	if !ok {
		t.Fatal("expected to find alias 'll'")
	}
	if val != "ls -l" {
		t.Fatalf("got %q want %q", val, "ls -l")
	}
}

func TestGet_Missing(t *testing.T) {
	s := alias.New()
	_, ok := s.Get("nonexistent")
	if ok {
		t.Fatal("expected Get to return false for missing alias")
	}
}

func TestDelete(t *testing.T) {
	s := alias.New()
	s.Set("foo", "bar")
	s.Delete("foo")
	_, ok := s.Get("foo")
	if ok {
		t.Fatal("expected alias to be deleted")
	}
}

func TestDelete_NonExistent_IsNoOp(t *testing.T) {
	s := alias.New()
	s.Delete("ghost") // must not panic
}

func TestAll(t *testing.T) {
	s := alias.New()
	s.Set("a", "aaa")
	s.Set("b", "bbb")
	all := s.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 aliases, got %d", len(all))
	}
	if all["a"] != "aaa" {
		t.Fatalf("a: got %q want %q", all["a"], "aaa")
	}
}

func TestExpand_Match(t *testing.T) {
	s := alias.New()
	s.Set("ll", "ls -l")

	got := s.Expand("ll /tmp")
	want := "ls -l /tmp"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestExpand_NoMatch(t *testing.T) {
	s := alias.New()
	got := s.Expand("echo hello")
	if got != "echo hello" {
		t.Fatalf("expected unchanged, got %q", got)
	}
}

func TestExpand_Empty(t *testing.T) {
	s := alias.New()
	got := s.Expand("")
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestExpand_NoArgs(t *testing.T) {
	s := alias.New()
	s.Set("gs", "git status")
	got := s.Expand("gs")
	if got != "git status" {
		t.Fatalf("got %q want %q", got, "git status")
	}
}

func TestSet_TrimsWhitespace(t *testing.T) {
	s := alias.New()
	s.Set("  ll  ", "  ls -l  ")
	val, ok := s.Get("ll")
	if !ok {
		t.Fatal("expected trimmed key to be found")
	}
	if val != "ls -l" {
		t.Fatalf("expected trimmed value, got %q", val)
	}
}
