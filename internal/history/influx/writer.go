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
	cfg      Config
	enc      *encoder // nur in der Goroutine von Run
	lastSize int      // Größe des vorigen Takts, um den Puffer passend anzulegen

	mu      sync.Mutex
	buffer  []takt
	dropped int64
	lastErr string
	ok      bool
}

// takt ist ein Takt im Puffer, gzip-komprimiert in Stücken von höchstens batchLines Zeilen; so
// hält der Puffer auch bei 1000 Geräten eine Minute in wenigen hundert MB.
type takt struct {
	at     time.Time
	chunks [][]byte
}

// New liefert einen Schreiber.
func New(cfg Config) *Writer {
	if cfg.Client == nil {
		cfg.Client = &http.Client{}
	}
	return &Writer{cfg: cfg, enc: newEncoder(cfg.Field, cfg.Gauges), ok: true}
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
	tk, err := w.build(t, me)
	if err != nil {
		w.cfg.Log.Error("influx interval not built", "err", err)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer = append(w.buffer, tk)
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

// build bildet die Zeilen eines Takts und komprimiert sie in Stücken von höchstens batchLines.
func (w *Writer) build(t time.Time, me own) (takt, error) {
	data, _ := w.enc.encode(make([]byte, 0, w.lastSize), w.cfg.Core.View(), t, me)
	w.lastSize = len(data)
	tk := takt{at: t}
	for rest := data; len(rest) > 0; {
		chunk := cutLines(rest, batchLines)
		rest = rest[len(chunk):]
		z, err := compress(chunk)
		if err != nil {
			return takt{}, err
		}
		tk.chunks = append(tk.chunks, z)
	}
	return tk, nil
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
		// Ein Takt gilt erst als geschrieben, wenn alle Stücke angekommen sind; sonst wird er
		// ganz wiederholt (gleiche Serie und Zeit überschreiben sich in InfluxDB).
		for _, chunk := range pending[sent].chunks {
			if err = w.write(ctx, chunk); err != nil {
				break
			}
		}
		if err == nil {
			sent++
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

// cutLines liefert den Anfang von data mit höchstens n Zeilen.
func cutLines(data []byte, n int) []byte {
	end := 0
	for range n {
		i := bytes.IndexByte(data[end:], '\n')
		if i < 0 {
			return data
		}
		end += i + 1
	}
	return data[:end]
}

// compress komprimiert mit der schnellsten Stufe von gzip.
func compress(data []byte) ([]byte, error) {
	var body bytes.Buffer
	zw, err := gzip.NewWriterLevel(&body, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}

// write sendet ein komprimiertes Stück per POST /api/v2/write, Zeitstempel in Millisekunden.
func (w *Writer) write(ctx context.Context, gz []byte) error {
	u := *w.cfg.URL
	u.Path = strings.TrimSuffix(u.Path, "/") + "/api/v2/write"
	q := url.Values{"bucket": {w.cfg.Bucket}, "precision": {"ms"}}
	if w.cfg.Org != "" {
		q.Set("org", w.cfg.Org)
	}
	u.RawQuery = q.Encode()
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(gz))
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
