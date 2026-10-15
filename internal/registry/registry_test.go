package registry_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"nexshell/internal/registry"
)

// ─── fakes ────────────────────────────────────────────────────────────────────

type fakeAliasStore struct{ m map[string]string }

func newFakeAliasStore() *fakeAliasStore              { return &fakeAliasStore{m: make(map[string]string)} }
func (f *fakeAliasStore) Set(n, v string)             { f.m[n] = v }
func (f *fakeAliasStore) Get(n string) (string, bool) { v, ok := f.m[n]; return v, ok }
func (f *fakeAliasStore) Delete(n string)             { delete(f.m, n) }
func (f *fakeAliasStore) All() map[string]string {
	out := make(map[string]string)
	for k, v := range f.m {
		out[k] = v
	}
	return out
}

type fakeHistoryStore struct{ entries []string }

func (h *fakeHistoryStore) Add(l string)   { h.entries = append(h.entries, l) }
func (h *fakeHistoryStore) List() []string { return h.entries }
func (h *fakeHistoryStore) Save() error    { return nil }
func (h *fakeHistoryStore) Load() error    { return nil }

type fakeCmd struct {
	name string
	ran  bool
	err  error
}

func (c *fakeCmd) Name() string        { return c.name }
func (c *fakeCmd) Description() string { return "fake command " + c.name }
func (c *fakeCmd) Run(_ registry.Context, _ []string) error {
	c.ran = true
	return c.err
}

// ─── tests ────────────────────────────────────────────────────────────────────

func TestRegister_And_Lookup(t *testing.T) {
	reg := registry.New()
	cmd := &fakeCmd{name: "test"}
	reg.Register(cmd)

	got, ok := reg.Lookup("test")
	if !ok {
		t.Fatal("expected to find registered command 'test'")
	}
	if got.Name() != "test" {
		t.Fatalf("got name %q want %q", got.Name(), "test")
	}
}

func TestLookup_NotFound(t *testing.T) {
	reg := registry.New()
	_, ok := reg.Lookup("nonexistent")
	if ok {
		t.Fatal("expected lookup to return false for unregistered command")
	}
}

func TestRegister_Duplicate_Panics(t *testing.T) {
	reg := registry.New()
	reg.Register(&fakeCmd{name: "dup"})
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration but got none")
		}
	}()
	reg.Register(&fakeCmd{name: "dup"})
}

func TestAll_Sorted(t *testing.T) {
	reg := registry.New()
	reg.Register(&fakeCmd{name: "zebra"})
	reg.Register(&fakeCmd{name: "alpha"})
	reg.Register(&fakeCmd{name: "middle"})

	all := reg.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(all))
	}
	names := []string{all[0].Name(), all[1].Name(), all[2].Name()}
	want := []string{"alpha", "middle", "zebra"}
	for i, n := range names {
		if n != want[i] {
			t.Fatalf("position %d: got %q want %q", i, n, want[i])
		}
	}
}

func TestCommand_Run(t *testing.T) {
	reg := registry.New()
	fake := &fakeCmd{name: "run-me", err: errors.New("boom")}
	reg.Register(fake)

	cmd, _ := reg.Lookup("run-me")
	ctx := registry.Context{
		Aliases:  newFakeAliasStore(),
		History:  &fakeHistoryStore{},
		Registry: reg,
	}
	err := cmd.Run(ctx, nil)
	if !fake.ran {
		t.Fatal("expected command to have been run")
	}
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected error 'boom', got %v", err)
	}
}

func TestContext_DefaultStreamsAndCancellation(t *testing.T) {
	ctx := registry.Context{}
	if ctx.Input() != os.Stdin || ctx.Output() != os.Stdout || ctx.ErrorOutput() != os.Stderr {
		t.Fatal("nil streams must preserve inherited process streams")
	}
	if ctx.Cancellation() == nil || ctx.Cancellation().Err() != nil {
		t.Fatal("nil cancellation context must use an active background context")
	}
}

func TestContext_ExplicitStreamsAndCancellation(t *testing.T) {
	in := strings.NewReader("input")
	var out, diagnostics bytes.Buffer
	commandContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := registry.Context{Context: commandContext, Stdin: in, Stdout: &out, Stderr: &diagnostics}
	if ctx.Input() != in || ctx.Output() != &out || ctx.ErrorOutput() != &diagnostics || ctx.Cancellation() != commandContext {
		t.Fatal("explicit streams and context were not preserved")
	}
	cancel()
	if !errors.Is(ctx.Cancellation().Err(), context.Canceled) {
		t.Fatal("context cancellation was not preserved")
	}
}
