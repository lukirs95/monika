package state

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

func at(d time.Duration) time.Time { return t0.Add(d) }

type check struct {
	at          time.Duration
	phase       Phase
	live        bool
	unreachable bool
}

func expect(t *testing.T, l *Liveness, id uuid.UUID, checks ...check) {
	t.Helper()
	for _, c := range checks {
		st, _ := l.Status(id, at(c.at))
		unreachable := slices.Contains(l.Unreachable(at(c.at)), id)
		if l.IsUnreachable(id, at(c.at)) != unreachable {
			t.Errorf("at %s: IsUnreachable differs from Unreachable", c.at)
		}
		if st.Phase != c.phase || l.Live(id, at(c.at)) != c.live || unreachable != c.unreachable {
			t.Errorf("at %s: phase %s, live %v, unreachable %v; want %s, %v, %v",
				c.at, st.Phase, l.Live(id, at(c.at)), unreachable, c.phase, c.live, c.unreachable)
		}
	}
}

func TestLivenessStart(t *testing.T) {
	l, id := NewLiveness(), uuid.New()
	l.Starting(id, at(0))
	l.Started(id, 2*time.Second, at(0))
	// Bis zum Snapshot nicht Live; der Befund kommt erst nach 3 × Heartbeat ab dem Start.
	expect(t, l, id, check{5 * time.Second, PhaseConnecting, false, false}, check{6 * time.Second, PhaseUnreachable, false, true})

	l.Connected(id, at(7*time.Second))
	expect(t, l, id, check{7 * time.Second, PhaseConnecting, false, false})
	if l.HasSnapshot(id) {
		t.Error("snapshot before SnapshotApplied")
	}
	l.SnapshotApplied(id, at(7*time.Second))
	expect(t, l, id, check{7 * time.Second, PhaseLive, true, false})
	if !l.HasSnapshot(id) {
		t.Error("no snapshot after SnapshotApplied")
	}
}

func TestLivenessSilence(t *testing.T) {
	l, id := live(2 * time.Second)
	l.Touch(id, at(4*time.Second))
	// Lebenszeichen bei 4 s: Live bis 10 s, dann zugleich nicht Live und nicht erreichbar.
	expect(t, l, id, check{9 * time.Second, PhaseLive, true, false}, check{10 * time.Second, PhaseUnreachable, false, true})
	l.Touch(id, at(11*time.Second))
	expect(t, l, id, check{11 * time.Second, PhaseLive, true, false})
}

func TestLivenessDisconnect(t *testing.T) {
	l, id := live(2 * time.Second)
	l.Disconnected(id, errors.New("cable"), at(time.Second))
	// Sofort nicht Live (unknown), der Befund aber erst nach 3 × Heartbeat.
	expect(t, l, id, check{time.Second, PhaseConnecting, false, false}, check{7 * time.Second, PhaseUnreachable, false, true})
	l.Connected(id, at(3*time.Second))
	l.SnapshotApplied(id, at(3*time.Second))
	expect(t, l, id, check{8 * time.Second, PhaseLive, true, false})
	if st, _ := l.Status(id, at(8*time.Second)); st.Err != "cable" {
		t.Errorf("Err = %q, want cable", st.Err)
	}
}

func TestLivenessFailures(t *testing.T) {
	l, id := live(2 * time.Second)
	l.Failed(id, errors.New("panic"), at(time.Second))
	expect(t, l, id, check{time.Second, PhaseBackoff, false, false}, check{7 * time.Second, PhaseBackoff, false, true})

	l.Stopping(id, at(8*time.Second))
	expect(t, l, id, check{20 * time.Second, PhaseStopping, false, false})
	l.Stuck(id, at(18*time.Second))
	expect(t, l, id, check{20 * time.Second, PhaseStuck, false, true})

	l.Remove(id)
	if _, ok := l.Status(id, at(20*time.Second)); ok || len(l.Unreachable(at(20*time.Second))) != 0 {
		t.Fatal("removed source still tracked")
	}
}

func TestLivenessWithoutHeartbeat(t *testing.T) {
	l, id := live(0)
	// Ohne Überwachung der Lebenszeichen bleibt die Quelle Live, auch ohne Meldungen.
	expect(t, l, id, check{time.Hour, PhaseLive, true, false})
	l.Failed(id, errors.New("bad config"), at(time.Hour))
	expect(t, l, id, check{time.Hour, PhaseBackoff, false, true})
}

func live(heartbeat time.Duration) (*Liveness, uuid.UUID) {
	l, id := NewLiveness(), uuid.New()
	l.Starting(id, at(0))
	l.Started(id, heartbeat, at(0))
	l.Connected(id, at(0))
	l.SnapshotApplied(id, at(0))
	return l, id
}
