package fuzzyfinder

import (
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
