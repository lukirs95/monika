package influx

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lukirs95/monika/internal/core"
)

const (
	bufferAge    = time.Minute // so viel an Takten hält der Puffer
	batchLines   = 5000
	writeTimeout = 10 * time.Second
	stopTimeout  = 5 * time.Second // Phase 5 des Herunterfahrens: Rest schreiben
)

// Core ist, was der Schreiber vom Core braucht.
type Core interface {
	View() *core.View
	Post(m core.Message)
}

// Config ist die Verbindung zu InfluxDB und was der Schreiber sonst braucht.
type Config struct {
	URL      *url.URL
	Token    string
	Org      string
	Bucket   string
	Interval time.Duration
	Core     Core
	Field    Vocabulary // meist vocab.Lookup
	Gauges   Gauges     // optional
	Client   *http.Client
	Log      *slog.Logger
}

// Writer tastet im Takt die View ab und schreibt sie nach InfluxDB; ist InfluxDB nicht
// erreichbar, puffert er höchstens eine Minute an Takten.
type Writer struct {
	cfg Config

	mu      sync.Mutex
	buffer  []takt
	dropped int64
	lastErr string
	ok      bool
}

type takt struct {
	at    time.Time
	lines []string
}

// New liefert einen Schreiber.
func New(cfg Config) *Writer {
	if cfg.Client == nil {
		cfg.Client = &http.Client{}
	}
	return &Writer{cfg: cfg, ok: true}
}

// Run bildet jeden Takt einen Satz Zeilen und schreibt in eigener Goroutine, damit ein langsames
// Schreiben keinen Takt verhindert. Mit ctx schreibt er den Rest (höchstens 5 s) und kehrt zurück.
func (w *Writer) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()
	signal := make(chan struct{}, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-signal:
				w.flush(ctx)
			}
		}
	})
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
			w.flush(fctx)
			cancel()
			return nil
		case t := <-ticker.C:
			w.add(t)
			select {
			case signal <- struct{}{}:
			default:
			}
		}
	}
}

// add bildet die Zeilen eines Takts und hängt sie an; Takte älter als eine Minute fallen weg.
func (w *Writer) add(t time.Time) {
	w.mu.Lock()
	me := own{buffered: int64(len(w.buffer)), dropped: w.dropped}
	w.mu.Unlock()
	ls := lines(w.cfg.Core.View(), t, w.cfg.Field, me, w.cfg.Gauges)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer = append(w.buffer, takt{at: t, lines: ls})
	drop := 0
	for drop < len(w.buffer) && t.Sub(w.buffer[drop].at) > bufferAge {
		drop++
	}
	if drop > 0 {
		w.buffer = w.buffer[drop:]
		w.dropped += int64(drop)
		w.cfg.Log.Warn("influx buffer full, oldest intervals dropped", "dropped", drop)
	}
}

// flush schreibt die gepufferten Takte in Batches; gesendete fallen aus dem Puffer, bei einem
// Fehler bleibt der Rest für den nächsten Versuch.
func (w *Writer) flush(ctx context.Context) {
	w.mu.Lock()
	pending := append([]takt(nil), w.buffer...)
	w.mu.Unlock()
	sent := 0
	var err error
	for sent < len(pending) && err == nil {
		// Ganze Takte zu einem Batch bis batchLines; ein großer Takt wird geteilt.
		var batch []string
		n := 0
		for n < len(pending)-sent && (len(batch) == 0 || len(batch)+len(pending[sent+n].lines) <= batchLines) {
			batch = append(batch, pending[sent+n].lines...)
			n++
		}
		for i := 0; i < len(batch) && err == nil; i += batchLines {
			err = w.write(ctx, batch[i:min(i+batchLines, len(batch))])
		}
		if err == nil {
			sent += n
		}
	}
	w.mu.Lock()
	// Während des Schreibens kann add vorne Takte verworfen haben; entfernt wird nur, was noch da ist.
	for sent > 0 && len(w.buffer) > 0 && !w.buffer[0].at.After(pending[sent-1].at) {
		w.buffer = w.buffer[1:]
	}
	changed := w.ok != (err == nil)
	w.ok = err == nil
	if err != nil {
		w.lastErr = err.Error()
	} else {
		w.lastErr = ""
	}
	status := core.InfluxStatus{OK: w.ok, Buffered: len(w.buffer), Dropped: int(w.dropped), LastError: w.lastErr}
	w.mu.Unlock()
	if changed && err != nil {
		w.cfg.Log.Warn("influx write failed", "err", err)
	} else if changed {
		w.cfg.Log.Info("influx write recovered")
	}
	w.cfg.Core.Post(status)
}

// write sendet Zeilen per POST /api/v2/write, gzip-komprimiert, Zeitstempel in Millisekunden.
func (w *Writer) write(ctx context.Context, ls []string) error {
	var body bytes.Buffer
	zw := gzip.NewWriter(&body)
	if _, err := io.WriteString(zw, strings.Join(ls, "\n")); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	u := *w.cfg.URL
	u.Path = strings.TrimSuffix(u.Path, "/") + "/api/v2/write"
	q := url.Values{"bucket": {w.cfg.Bucket}, "precision": {"ms"}}
	if w.cfg.Org != "" {
		q.Set("org", w.cfg.Org)
	}
	u.RawQuery = q.Encode()
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Content-Encoding", "gzip")
	if w.cfg.Token != "" {
		req.Header.Set("Authorization", "Token "+w.cfg.Token)
	}
	resp, err := w.cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("influx: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
