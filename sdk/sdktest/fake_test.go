package sdktest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

func gw4() sdk.Snapshot {
	return sdk.Snapshot{
		Identity: sdk.Identity{Vendor: "Acme", Model: "GW-4", Serial: "A1"},
		Elements: []sdk.Element{
			{ExternalID: "in/1", Kind: vocab.ElementPort, Transport: "sdi", Direction: vocab.DirectionIn},
			{ExternalID: "sender/1", Kind: vocab.ElementSender, Transport: "st2110-20", Actions: []string{vocab.ActionStart, vocab.ActionStop}},
		},
		Links: []sdk.Link{{Type: sdk.LinkFeeds, From: sdk.Endpoint{ExternalID: "in/1"}, To: sdk.Endpoint{ExternalID: "sender/1"}}},
		Facts: map[string]sdk.Facts{
			"":         {"system.uptime_s": int64(100)},
			"in/1":     {vocab.StateSignalPresent: true},
			"sender/1": {vocab.StateRunning: true},
		},
	}
}

// start lässt eine Source des Geräts laufen und liefert den Reporter und das Ende von Run.
func start(t *testing.T, f *Fake, address string) (*Reporter, context.CancelFunc, <-chan error) {
	t.Helper()
	src, err := f.Driver().New(sdk.SourceConfig{Address: address})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := NewReporter()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- src.Run(ctx, rec) }()
	return rec, cancel, done
}

func wait(t *testing.T, rec *Reporter, from int, kind CallKind) (Call, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	c, i, err := rec.WaitFor(ctx, from, Is(kind))
	if err != nil {
		t.Fatalf("waiting for %s: %v (calls: %+v)", kind, err, rec.Calls())
	}
	return c, i
}

func TestFakeLifecycle(t *testing.T) {
	f := NewFake("fake")
	dev := f.Device("10.0.0.1", gw4())
	rec, cancel, done := start(t, f, "10.0.0.1")

	_, i := wait(t, rec, 0, CallConnected)
	snap, i := wait(t, rec, i, CallSnapshot)
	if len(snap.Snapshot.Elements) != 2 {
		t.Fatalf("snapshot = %+v", snap.Snapshot)
	}

	dev.SetFacts("in/1", sdk.Facts{vocab.StateSignalPresent: false})
	upd, i := wait(t, rec, i, CallUpdate)
	if v := upd.Update.Facts["in/1"][vocab.StateSignalPresent]; v != false {
		t.Fatalf("update = %+v", upd.Update)
	}

	dev.GoOffline(errors.New("cable"))
	_, i = wait(t, rec, i, CallDisconnected)
	if _, err := mustSourceAt(t, f, "10.0.0.1").Execute(t.Context(), sdk.Command{ExternalID: "sender/1", Action: "start"}); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("Execute offline = %v, want ErrUnreachable", err)
	}
	dev.GoOnline()
	_, i = wait(t, rec, i, CallConnected)
	again, _ := wait(t, rec, i, CallSnapshot)
	if v := again.Snapshot.Facts["in/1"][vocab.StateSignalPresent]; v != false {
		t.Fatalf("snapshot after reconnect lost the update: %+v", again.Snapshot.Facts)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run = %v, want nil after cancel", err)
	}
	if dev.Running() != 0 {
		t.Fatalf("Running = %d after Run returned", dev.Running())
	}
}

func TestFakeFaults(t *testing.T) {
	boom := errors.New("boom")

	t.Run("New fails once", func(t *testing.T) {
		f := NewFake("fake")
		f.Device("a", gw4()).FailNew(boom)
		if _, err := f.Driver().New(sdk.SourceConfig{Address: "a"}); !errors.Is(err, boom) {
			t.Fatalf("New = %v, want boom", err)
		}
		if _, err := f.Driver().New(sdk.SourceConfig{Address: "a"}); err != nil {
			t.Fatalf("second New = %v", err)
		}
	})

	t.Run("Run fails while running", func(t *testing.T) {
		f := NewFake("fake")
		dev := f.Device("a", gw4())
		rec, cancel, done := start(t, f, "a")
		defer cancel()
		wait(t, rec, 0, CallSnapshot)
		dev.FailRun(boom)
		if err := <-done; !errors.Is(err, boom) {
			t.Fatalf("Run = %v, want boom", err)
		}
	})

	t.Run("Run panics", func(t *testing.T) {
		f := NewFake("fake")
		f.Device("a", gw4()).PanicRun()
		src := mustSourceAt(t, f, "a")
		defer func() {
			if recover() == nil {
				t.Fatal("Run did not panic")
			}
		}()
		_ = src.Run(t.Context(), NewReporter())
	})

	t.Run("hang until release", func(t *testing.T) {
		f := NewFake("fake")
		dev := f.Device("a", gw4())
		rec, cancel, done := start(t, f, "a")
		wait(t, rec, 0, CallSnapshot)
		dev.Hang()
		cancel()
		select {
		case <-done:
			t.Fatal("Run returned although the device hangs")
		case <-time.After(50 * time.Millisecond):
		}
		dev.Release()
		if err := <-done; err != nil {
			t.Fatalf("Run = %v", err)
		}
	})

	t.Run("execute answer", func(t *testing.T) {
		f := NewFake("fake")
		dev := f.Device("a", gw4())
		dev.OnExecute(func(sdk.Command) (sdk.Result, error) {
			return sdk.Result{Status: sdk.Rejected, RejectedFields: []string{"codec.bitrate_mbps"}}, nil
		})
		res, err := mustSourceAt(t, f, "a").Execute(t.Context(), sdk.Command{ExternalID: "sender/1", Action: "set"})
		if err != nil || res.Status != sdk.Rejected || len(dev.Commands()) != 1 {
			t.Fatalf("Execute = %+v, %v; commands %+v", res, err, dev.Commands())
		}
	})
}

func mustSourceAt(t *testing.T, f *Fake, address string) sdk.Source {
	t.Helper()
	src, err := f.Driver().New(sdk.SourceConfig{Address: address})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return src
}
