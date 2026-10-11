package influx

import (
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

var (
	device = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	access = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	port   = uuid.MustParse("00000000-0000-0000-0000-000000000003")
	source = uuid.MustParse("00000000-0000-0000-0000-0000000000aa")
	t0     = time.UnixMilli(1760097600000)
)

func testView(state core.SourceState) *core.View {
	inv := inventory.New(nil, nil, slog.New(slog.DiscardHandler))
	inv.Load(inventory.Snapshot{
		Sources: []inventory.Source{{ID: source, DeviceID: device, Driver: "videoxlink", Enabled: true, AccessElementID: access}},
		Elements: []inventory.Element{
			{ID: device, Kind: inventory.KindDevice, DeviceID: device},
			{ID: access, Kind: inventory.KindAccess, ParentID: device, DeviceID: device, SourceID: source},
			{ID: port, Kind: "port", ParentID: access, DeviceID: device, SourceID: source, Transport: "sdi"},
		},
	})
	return &core.View{
		Graph: inv.Graph(),
		Facts: map[uuid.UUID]sdk.Facts{port: {
			vocab.StateSignalPresent: true,
			"sdi.crc_errors":         int64(4),
			"sdi.crc_errors_per_s":   0.5, // die Rate fällt weg, weil der Zähler gemeldet ist
			"sdi.level":              `3G-"A"`,
			"videoxlink.raw":         int64(1), // roh: nie
			"call.called_at":         "x",      // nicht historisiert
		}},
		Health: map[uuid.UUID]evaluation.ElementHealth{
			port:   {ElementID: port, Health: evaluation.HealthWarning},
			access: {ElementID: access, Health: evaluation.HealthNone},
			device: {ElementID: device, Health: evaluation.HealthWarning, Rollup: &evaluation.Rollup{Warning: 1, Unchecked: 1}},
		},
		Sources: map[uuid.UUID]core.SourceStatus{source: {State: state}},
		DB:      core.DBStatus{OK: true},
		Stats:   core.Stats{LoopLag: 3 * time.Millisecond, Events: 42},
	}
}

func TestLines(t *testing.T) {
	got := lines(testView(core.StateLive), t0, vocab.Lookup, own{buffered: 2}, func() map[string]int64 { return map[string]int64{"api_streams": 5} })
	tags := "element_id=" + port.String() + ",device_id=" + device.String() + ",source_id=" + source.String() + ",driver=videoxlink,kind=port,transport=sdi"
	want := []string{
		"sdi," + tags + ` crc_errors=4i,level="3G-\"A\"" 1760097600000`,
		"state," + tags + " signal_present=true 1760097600000",
		"health,element_id=" + port.String() + ",device_id=" + device.String() + ",kind=port level=1i 1760097600000",
		"health,element_id=" + device.String() + ",device_id=" + device.String() + ",kind=device level=1i 1760097600000",
		"rollup,element_id=" + device.String() + ",kind=device ok=0i,warning=1i,error=0i,unknown=0i,unchecked=1i,unmonitored=0i,missing=0i 1760097600000",
		"monika api_streams=5i,db_ok=true,events=42i,influx_buffered=2i,influx_dropped=0i,loop_lag_ms=3i,loop_queue=0i,sources_live=1i,sources_not_live=0i,subscribers=0i,subscribers_dropped=0i 1760097600000",
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestLinesSourceNotLive(t *testing.T) {
	for _, l := range lines(testView(core.StateUnreachable), t0, vocab.Lookup, own{}, nil) {
		if strings.HasPrefix(l, "sdi,") || strings.HasPrefix(l, "state,") {
			t.Errorf("facts of a source that is not live: %s", l)
		}
	}
}

// fakeCore liefert die View und zeichnet InfluxStatus auf.
type fakeCore struct {
	mu     sync.Mutex
	view   *core.View
	status []core.InfluxStatus
}

func (f *fakeCore) View() *core.View { return f.view }
func (f *fakeCore) Post(m core.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := m.(core.InfluxStatus); ok {
		f.status = append(f.status, s)
	}
}

func (f *fakeCore) last() core.InfluxStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.status) == 0 {
		return core.InfluxStatus{}
	}
	return f.status[len(f.status)-1]
}

// server nimmt Schreibvorgänge an; fail lässt sie scheitern.
type server struct {
	mu       sync.Mutex
	fail     bool
	requests []*http.Request
	bodies   []string
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	zr, err := gzip.NewReader(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	b, _ := io.ReadAll(zr)
	s.requests = append(s.requests, r)
	s.bodies = append(s.bodies, string(b))
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) setFail(fail bool) {
	s.mu.Lock()
	s.fail = fail
	s.mu.Unlock()
}

func (s *server) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func newWriter(t *testing.T, srv *server) (*Writer, *fakeCore, func()) {
	t.Helper()
	ts := httptest.NewServer(srv)
	u, _ := url.Parse(ts.URL)
	fc := &fakeCore{view: testView(core.StateLive)}
	w := New(Config{URL: u, Token: "secret", Org: "monika", Bucket: "mon", Interval: 10 * time.Millisecond,
		Core: fc, Field: vocab.Lookup, Log: slog.New(slog.DiscardHandler)})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = w.Run(ctx)
		close(done)
	}()
	stop := sync.OnceFunc(func() {
		cancel()
		<-done
		ts.Close()
	})
	t.Cleanup(stop)
	return w, fc, stop
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); !ok(); {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWriterSends(t *testing.T) {
	srv := &server{}
	_, fc, _ := newWriter(t, srv)
	waitFor(t, "two writes", func() bool { return srv.count() >= 2 })
	srv.mu.Lock()
	r := srv.requests[0]
	body := srv.bodies[0]
	srv.mu.Unlock()
	if r.URL.Path != "/api/v2/write" || r.URL.Query().Get("bucket") != "mon" || r.URL.Query().Get("org") != "monika" ||
		r.URL.Query().Get("precision") != "ms" || r.Header.Get("Authorization") != "Token secret" {
		t.Errorf("request = %s %v", r.URL, r.Header)
	}
	if !strings.Contains(body, "state,element_id=") || !strings.Contains(body, "\nmonika ") {
		t.Errorf("body = %s", body)
	}
	if st := fc.last(); !st.OK || st.Buffered != 0 {
		t.Errorf("status = %+v", st)
	}
}

func TestWriterBuffersWhileDown(t *testing.T) {
	srv := &server{fail: true}
	_, fc, _ := newWriter(t, srv)
	waitFor(t, "failure reported", func() bool { st := fc.last(); return !st.OK && st.Buffered >= 3 })
	if !strings.Contains(fc.last().LastError, "503") {
		t.Errorf("last error = %q", fc.last().LastError)
	}
	srv.setFail(false)
	waitFor(t, "recovered", func() bool { st := fc.last(); return st.OK && st.Buffered == 0 })
	srv.mu.Lock()
	defer srv.mu.Unlock()
	takts := strings.Count(strings.Join(srv.bodies, "\n"), "\nmonika ")
	if takts < 3 {
		t.Errorf("only %d intervals written after recovery", takts)
	}
}

func TestBufferDropsOldest(t *testing.T) {
	w := New(Config{Core: &fakeCore{view: testView(core.StateLive)}, Field: vocab.Lookup, Log: slog.New(slog.DiscardHandler)})
	for i := range 70 {
		w.add(t0.Add(time.Duration(i) * time.Second))
	}
	if len(w.buffer) != 61 || w.dropped != 9 {
		t.Errorf("buffer = %d intervals, dropped %d; want 61 and 9", len(w.buffer), w.dropped)
	}
}

func TestShutdownFlushes(t *testing.T) {
	srv := &server{fail: true}
	_, _, stop := newWriter(t, srv)
	time.Sleep(50 * time.Millisecond)
	srv.setFail(false)
	before := srv.count()
	stop()
	if srv.count() == before {
		t.Error("buffered intervals not written on shutdown")
	}
}
