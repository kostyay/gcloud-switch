package fuzzyfinder

import (
	"context"
	"errors"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestFindReturnsHighlightedItemOnHotkey(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sim.Fini)
	sim.SetSize(80, 24)

	events := make(chan tcell.Event, 1)
	events <- tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone)

	f := newFinder()
	f.term = &termImpl{screen: sim}
	f.termEventsChan = events

	idx, err := f.Find(
		[]string{"personal", "production"},
		func(i int) string { return []string{"personal", "production"}[i] },
		WithHotkey('r'),
	)
	if !errors.Is(err, ErrHotkey) {
		t.Fatalf("err = %v, want ErrHotkey", err)
	}
	if idx != 0 {
		t.Fatalf("idx = %d, want 0", idx)
	}
}

func TestPreviewIsHiddenUntilInfoKey(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sim.Fini)

	events := make(chan tcell.Event, 1)
	events <- tcell.NewEventKey(tcell.KeyRune, 'i', tcell.ModNone)

	f := newFinder()
	f.term = &termImpl{screen: sim}
	f.termEventsChan = events
	options := defaultOption
	options.previewFunc = func(_, _, _ int) string { return "account" }
	if err := f.initFinder([]string{"personal"}, nil, options); err != nil {
		t.Fatal(err)
	}
	if f.state.preview {
		t.Fatal("preview is visible before i is pressed")
	}
	if err := f.readKey(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.state.preview {
		t.Fatal("preview is hidden after i is pressed")
	}
}
