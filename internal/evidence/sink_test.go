package evidence_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

var _ evidence.Sink = (*evidence.MemorySink)(nil)

func TestMemorySinkRefusesNilAndDoneContext(t *testing.T) {
	var sink evidence.MemorySink
	if err := sink.Append(context.Background(), nil); !errors.Is(err, evidence.ErrNoEvent) {
		t.Errorf("Append(nil) = %v, want ErrNoEvent", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sink.Append(ctx, &controlv1.Event{EventId: "e-1"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Append with a cancelled context = %v, want context.Canceled", err)
	}
	if got := sink.Events(); len(got) != 0 {
		t.Errorf("a refused append stored %d event(s)", len(got))
	}
}

func TestMemorySinkStoresClonesInOrder(t *testing.T) {
	var sink evidence.MemorySink
	first := &controlv1.Event{EventId: "e-1"}
	if err := sink.Append(context.Background(), first); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := sink.Append(context.Background(), &controlv1.Event{EventId: "e-2"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	first.EventId = "rewritten"
	got := sink.Events()
	if len(got) != 2 || got[0].GetEventId() != "e-1" || got[1].GetEventId() != "e-2" {
		t.Fatalf("Events() = %v, want e-1 then e-2 untouched by the caller's later write", got)
	}
	got[0].EventId = "rewritten again"
	if sink.Events()[0].GetEventId() != "e-1" {
		t.Errorf("Events() handed out the stored record rather than a clone")
	}
}

func TestMemorySinkIsSafeForConcurrentUse(t *testing.T) {
	var sink evidence.MemorySink
	var wg sync.WaitGroup
	const writers, each = 8, 50
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				if err := sink.Append(context.Background(), &controlv1.Event{EventId: "w", PrevEventId: strconv.Itoa(w)}); err != nil {
					t.Errorf("Append: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	if got := len(sink.Events()); got != writers*each {
		t.Errorf("stored %d events, want %d", got, writers*each)
	}
}
