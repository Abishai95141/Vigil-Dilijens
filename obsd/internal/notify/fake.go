package notify

import (
	"context"
	"sync"
)

// FakeNotifier records what would be sent, for hermetic tests (no network). It can be
// made to fail (Err) to exercise the non-gating retry path.
type FakeNotifier struct {
	mu   sync.Mutex
	sent []Message
	Err  error // when set, Send returns it and records nothing
}

// Send records the message (or returns the injected error).
func (f *FakeNotifier) Send(_ context.Context, m Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	f.sent = append(f.sent, m)
	return nil
}

// Messages returns a copy of everything sent so far.
func (f *FakeNotifier) Messages() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Message, len(f.sent))
	copy(out, f.sent)
	return out
}

// Count returns how many messages were sent.
func (f *FakeNotifier) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}
