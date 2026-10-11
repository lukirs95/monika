package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/sdktest"
	"github.com/lukirs95/monika/sdk/vocab"
)

// fakeCore zeichnet Nachrichten auf und gibt jeder Instanz einen eigenen Reporter.
type fakeCore struct {
	mu        sync.Mutex
	msgs      []core.Message
	reporters []*sdktest.Reporter
	changed   chan struct{}
}

func newFakeCore() *fakeCore { return &fakeCore{changed: make(chan struct{}, 1)} }

func (f *fakeCore) Reporter(uuid.UUID) sdk.Reporter {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := sdktest.NewReporter()
	f.reporters = append(f.reporters, r)
	return r
}

func (f *fakeCore) Post(m core.Message) {
	f.mu.Lock()
	f.msgs = append(f.msgs, m)
	f.mu.Unlock()
	select {
	case f.changed <- struct{}{}:
	default:
	}
}

// summary beschreibt die Nachrichten kurz: started, stopped, failed, panicked, stuck.
func (f *fakeCore) summary() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.msgs {
		switch m := m.(type) {
		case core.SourceStarted:
			out = append(out, "started")
		case core.SourceStopped:
			out = append(out, "stopped")
		case core.SourceFailed:
			if m.Panic {
				out = append(out, "panicked")
			} else {
				out = append(out, "failed")
			}
		case core.SourceStuck:
			out = append(out, "stuck")
		}
	}
	return strings.Join(out, " ")
}

func (f *fakeCore) lastFailure() core.SourceFailed {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.msgs) - 1; i >= 0; i-- {
		if m, ok := f.msgs[i].(core.SourceFailed); ok {
			return m
		}
	}
	return core.SourceFailed{}
}

// wait wartet, bis die Nachrichten mit want beginnen.
func (f *fakeCore) wait(t *testing.T, want string) {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for !strings.HasPrefix(f.summary(), want) {
		select {
		case <-f.changed:
		case <-timeout:
			t.Fatalf("messages = %q, want prefix %q", f.summary(), want)
		}
	}
}

type fakeSecrets struct{ err error }

func (f fakeSecrets) Value(context.Context, uuid.UUID) (json.RawMessage, error) {
	return json.RawMessage(`{"password":"x"}`), f.err
}

type bench struct {
	t    *testing.T
	sup  *Supervisor
	core *fakeCore
	dev  *sdktest.Device
	spec core.SourceSpec
	stop func()
}

// newBench liefert einen Supervisor mit kurzen Zeiten; run startet ihn.
func newBench(t *testing.T, secrets fakeSecrets) *bench {
	t.Helper()
	fake := sdktest.NewFake("fake")
	b := &bench{t: t, core: newFakeCore(), stop: func() {}}
	b.dev = fake.Device("10.0.0.1", sdk.Snapshot{Elements: []sdk.Element{{ExternalID: "p", Kind: vocab.ElementPort}}})
	b.spec = core.SourceSpec{SourceID: uuid.New(), DeviceID: uuid.New(), Driver: "fake", Address: "10.0.0.1"}
	b.sup = New(Config{
		Secrets: secrets, Scheduler: NewScheduler(4), Log: discard(),
		Drivers: func(name string) (sdk.Driver, bool) { return fake.Driver(), name == "fake" },
	})
	b.sup.minBackoff, b.sup.maxBackoff, b.sup.stableAfter, b.sup.stopTimeout = 20*time.Millisecond, 80*time.Millisecond, time.Hour, 100*time.Millisecond
	t.Cleanup(func() {
		b.dev.Release()
		b.stop()
	})
	return b
}

// run startet Run; stop beendet es und wartet darauf.
func (b *bench) run() *bench {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = b.sup.Run(ctx, b.core)
		close(done)
	}()
	b.stop = sync.OnceFunc(func() {
		cancel()
		<-done
	})
	return b
}

func TestStartAndStop(t *testing.T) {
	b := newBench(t, fakeSecrets{}).run()
	b.sup.Start(b.spec)
	b.core.wait(t, "started")
	if _, _, err := b.core.reporters[0].WaitFor(t.Context(), 0, sdktest.Is(sdktest.CallSnapshot)); err != nil {
		t.Fatalf("no snapshot: %v", err)
	}
	if res, err := b.sup.Execute(t.Context(), b.spec.SourceID, sdk.Command{Action: "start"}); err != nil || res.Status != sdk.Accepted {
		t.Errorf("Execute = %+v, %v", res, err)
	}
	b.sup.Stop(b.spec.SourceID)
	b.core.wait(t, "started stopped")
	if b.dev.Running() != 0 {
		t.Error("source still running")
	}
	if _, err := b.sup.Execute(t.Context(), b.spec.SourceID, sdk.Command{}); !errors.Is(err, ErrSourceNotRunning) {
		t.Errorf("Execute after stop = %v", err)
	}
	workers := func() int {
		b.sup.mu.Lock()
		defer b.sup.mu.Unlock()
		return len(b.sup.workers)
	}
	for deadline := time.Now().Add(time.Second); workers() != 0 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	if n := workers(); n != 0 {
		t.Errorf("workers = %d after stop, want 0", n)
	}
}

func TestStartBeforeRun(t *testing.T) {
	b := newBench(t, fakeSecrets{})
	b.sup.Start(b.spec)
	time.Sleep(20 * time.Millisecond)
	if b.core.summary() != "" {
		t.Fatal("started before Run")
	}
	b.run()
	b.core.wait(t, "started")
}

func TestFailuresAndBackoff(t *testing.T) {
	tests := []struct {
		name       string
		prepare    func(*bench)
		afterStart func(*bench)
		want       string
		panic      bool
		err        string
	}{
		{"New fails", func(b *bench) { b.dev.FailNew(errors.New("bad config")) }, nil, "failed started", false, "bad config"},
		{"Run fails", func(b *bench) { b.dev.FailRun(errors.New("lost")) }, nil, "started failed started", false, "lost"},
		{"Run panics", func(b *bench) { b.dev.PanicRun() }, nil, "started panicked started", true, "panic in Run"},
		{"Run returns nil", func(*bench) {}, func(b *bench) { b.dev.FailRun(nil) }, "started failed started", false, "without being stopped"},
		{"secret fails", nil, nil, "failed", false, "secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secrets := fakeSecrets{}
			if tt.prepare == nil {
				secrets.err = errors.New("locked")
			}
			b := newBench(t, secrets).run()
			b.spec.SecretID = uuid.New()
			if tt.prepare != nil {
				tt.prepare(b)
			}
			b.sup.Start(b.spec)
			if tt.afterStart != nil {
				b.core.wait(t, "started")
				if _, _, err := b.core.reporters[0].WaitFor(t.Context(), 0, sdktest.Is(sdktest.CallSnapshot)); err != nil {
					t.Fatal(err)
				}
				tt.afterStart(b)
			}
			b.core.wait(t, tt.want)
			if f := b.core.lastFailure(); f.Panic != tt.panic || !strings.Contains(fmt.Sprint(f.Err), tt.err) {
				t.Errorf("SourceFailed = %+v", f)
			}
		})
	}
}

func TestBackoffGrows(t *testing.T) {
	b := newBench(t, fakeSecrets{}).run()
	b.dev.FailNew(errors.New("bad"))
	start := time.Now()
	b.sup.Start(b.spec)
	b.core.wait(t, "failed")
	b.dev.FailNew(errors.New("bad"))
	b.core.wait(t, "failed failed")
	b.core.wait(t, "failed failed started")
	// Wartezeiten 20 ms, dann 40 ms.
	if d := time.Since(start); d < 60*time.Millisecond {
		t.Errorf("restarted after %v, want backoff of 20 ms + 40 ms", d)
	}
}

func TestRestartAndStuck(t *testing.T) {
	b := newBench(t, fakeSecrets{}).run()
	b.sup.Start(b.spec)
	b.core.wait(t, "started")
	b.sup.Start(b.spec) // Neustart
	b.core.wait(t, "started stopped started")

	b.dev.Hang()
	b.sup.Stop(b.spec.SourceID)
	b.core.wait(t, "started stopped started stuck")
	b.sup.Start(b.spec)
	time.Sleep(30 * time.Millisecond)
	if b.dev.Running() != 1 {
		t.Fatalf("running = %d while the old instance hangs, want 1", b.dev.Running())
	}
	b.dev.Release()
	b.core.wait(t, "started stopped started stuck stopped started")
}

func TestExecutePanic(t *testing.T) {
	b := newBench(t, fakeSecrets{}).run()
	b.dev.OnExecute(func(sdk.Command) (sdk.Result, error) { panic("boom") })
	b.sup.Start(b.spec)
	b.core.wait(t, "started")
	if _, err := b.sup.Execute(t.Context(), b.spec.SourceID, sdk.Command{}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Execute = %v", err)
	}
	b.core.wait(t, "started panicked started")
}

func TestShutdown(t *testing.T) {
	b := newBench(t, fakeSecrets{}).run()
	b.sup.Start(b.spec)
	b.core.wait(t, "started")
	b.stop()
	if got := b.core.summary(); got != "started stopped" {
		t.Errorf("messages = %q", got)
	}

	b = newBench(t, fakeSecrets{}).run()
	b.dev.Hang()
	b.sup.Start(b.spec)
	b.core.wait(t, "started")
	begin := time.Now()
	b.stop()
	if d := time.Since(begin); d > time.Second {
		t.Errorf("shutdown took %v", d)
	}
	if got := b.core.summary(); got != "started stuck" {
		t.Errorf("messages = %q", got)
	}
}
