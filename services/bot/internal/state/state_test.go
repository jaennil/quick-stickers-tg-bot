package state

import (
	"sync"
	"testing"
	"time"
)

// Reproduces the album bug: pressing "edit" on one picture, then having the
// rest of the album finish processing, used to redirect the correction to
// whichever media was saved last.
func TestIncomingMediaCannotStealAPendingEdit(t *testing.T) {
	m := NewManager(time.Minute)
	const user = int64(1)

	m.SetLastSticker(user, "spongebob")
	m.SetAwaitingEdit(user, "spongebob")

	// The two other pictures of the album land right after the button press.
	m.SetLastSticker(user, "orange-cat")
	m.SetLastSticker(user, "kitten")

	got, ok := m.TakeAwaitingEdit(user)
	if !ok {
		t.Fatal("pending edit was lost")
	}
	if got != "spongebob" {
		t.Fatalf("correction went to %q instead of the media the button belonged to", got)
	}
}

// The reply must be consumed exactly once, so a second message does not
// silently overwrite the same media again.
func TestPendingEditIsConsumedOnce(t *testing.T) {
	m := NewManager(time.Minute)
	const user = int64(1)

	m.SetAwaitingEdit(user, "sticker-1")
	if _, ok := m.TakeAwaitingEdit(user); !ok {
		t.Fatal("first take must succeed")
	}
	if id, ok := m.TakeAwaitingEdit(user); ok {
		t.Fatalf("second take must fail, got %q", id)
	}
	if mode := m.GetAwaitingMode(user); mode != ModeNone {
		t.Fatalf("awaiting mode must be cleared, got %v", mode)
	}
}

func TestTakeAwaitingEditWithoutPendingTarget(t *testing.T) {
	m := NewManager(time.Minute)
	const user = int64(1)

	if _, ok := m.TakeAwaitingEdit(user); ok {
		t.Fatal("unknown user must not yield a target")
	}
	// A bare mode switch carries no target, so it must not resolve either.
	m.SetAwaitingMode(user, ModeEdit)
	if _, ok := m.TakeAwaitingEdit(user); ok {
		t.Fatal("awaiting mode without a target must not yield one")
	}
	// And /edit still sees the most recent media, which is a separate slot.
	m.SetLastSticker(user, "recent")
	if got := m.GetLastSticker(user); got != "recent" {
		t.Fatalf("last sticker should stay independent, got %q", got)
	}
}

func TestClearAwaitingModeDropsPendingTarget(t *testing.T) {
	m := NewManager(time.Minute)
	const user = int64(1)

	m.SetAwaitingEdit(user, "sticker-1")
	m.ClearAwaitingMode(user)
	if _, ok := m.TakeAwaitingEdit(user); ok {
		t.Fatal("cleared mode must drop the pending target too")
	}
}

// State is written from the update handler and from background media workers at
// the same time, so every mutator must hold the lock.
func TestConcurrentStateAccessIsSafe(t *testing.T) {
	m := NewManager(time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func(n int) { defer wg.Done(); m.SetLastSticker(int64(n%5), "media") }(i)
		go func(n int) { defer wg.Done(); m.SetAwaitingEdit(int64(n%5), "target") }(i)
		go func(n int) { defer wg.Done(); m.TakeAwaitingEdit(int64(n % 5)) }(i)
	}
	wg.Wait()
}
