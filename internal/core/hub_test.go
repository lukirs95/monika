package core

import (
	"testing"

	"github.com/google/uuid"
)

func TestHubDelivers(t *testing.T) {
	h := newHub()
	ch, cancel := h.subscribe(4)
	defer cancel()
	h.publish([]Event{withVersion(ElementsChanged{}, 1), withVersion(HealthChanged{}, 1)})
	h.publish([]Event{withVersion(FindingRaised{}, 2)})
	for _, want := range []uint64{1, 1, 2} {
		if e := <-ch; e.Version() != want {
			t.Fatalf("version = %d, want %d", e.Version(), want)
		}
	}
	if subs, dropped, events := h.stats(); subs != 1 || dropped != 0 || events != 3 {
		t.Errorf("stats = %d, %d, %d", subs, dropped, events)
	}
}

func TestHubDropsSlowSubscriber(t *testing.T) {
	h := newHub()
	slow, cancelSlow := h.subscribe(1)
	fast, cancelFast := h.subscribe(10)
	defer cancelFast()

	h.publish([]Event{withVersion(ElementsChanged{}, 1), withVersion(ElementsChanged{}, 1)})

	// Der langsame bekommt, was in seinen Puffer passte, dann ist sein Kanal zu.
	if e, ok := <-slow; !ok || e.Version() != 1 {
		t.Fatalf("first event = %v, %v", e, ok)
	}
	if _, ok := <-slow; ok {
		t.Fatal("slow subscriber still open")
	}
	if len(fast) != 2 {
		t.Errorf("fast subscriber got %d events, want 2", len(fast))
	}
	if subs, dropped, _ := h.stats(); subs != 1 || dropped != 1 {
		t.Errorf("subscribers = %d, dropped = %d", subs, dropped)
	}
	cancelSlow() // nach dem Abhängen ohne Wirkung
	cancelSlow()
	h.publish([]Event{withVersion(ElementsChanged{}, 2)}) // kein Senden auf geschlossenen Kanal
}

func TestWithVersion(t *testing.T) {
	events := []Event{
		ElementsChanged{}, FactsChanged{}, FindingRaised{}, FindingEnded{}, FindingAcked{},
		FindingCauseChanged{}, HealthChanged{}, SourceStatusChanged{}, RunChanged{},
	}
	for _, e := range events {
		if got := withVersion(e, 7).Version(); got != 7 {
			t.Errorf("%T: version = %d", e, got)
		}
	}
}

func TestViewState(t *testing.T) {
	live, down, stuck := uuid.New(), uuid.New(), uuid.New()
	v := &View{Sources: map[uuid.UUID]SourceStatus{
		live:  {State: StateLive, Heartbeat: 5e9},
		down:  {State: StateUnreachable, Error: "connection refused"},
		stuck: {State: StateStuck},
	}}
	s := viewState{v}
	if !s.Live(live) || s.Live(down) || s.Heartbeat(live) != 5e9 {
		t.Error("live or heartbeat wrong")
	}
	if r, ok := s.Unreachable(down); !ok || r != "connection refused" {
		t.Errorf("Unreachable(down) = %q, %v", r, ok)
	}
	if _, ok := s.Unreachable(stuck); ok {
		t.Error("stuck is not unreachable in the view")
	}
}
