package forward

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const sendTimeout = 5 * time.Second

// sender verschickt ein Ereignis an ein Ziel.
type sender struct {
	client   *http.Client
	dialer   *net.Dialer
	tls      *tls.Config
	hostname string
}

func newSender() sender {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "-"
	}
	return sender{client: &http.Client{Timeout: sendTimeout}, dialer: &net.Dialer{Timeout: sendTimeout}, hostname: host}
}

// send verschickt m an t; secret ist der Klartext des Secrets des Ziels (oder nil).
func (s sender) send(ctx context.Context, t Target, m message, secret map[string]any) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	switch t.Kind {
	case KindSyslog:
		c, err := parseSyslog(t.Config)
		if err != nil {
			return err
		}
		return s.syslog(ctx, c, m, body)
	case KindWebhook:
		c, err := parseWebhook(t.Config)
		if err != nil {
			return err
		}
		return s.webhook(ctx, c, body, secret)
	}
	return fmt.Errorf("unknown kind %q", t.Kind)
}

// syslog nach RFC 5424: APP-NAME monika, MSGID = Ereignis, keine Structured Data, das JSON in MSG;
// über tcp und tls mit Längenangabe (RFC 6587).
func (s sender) syslog(ctx context.Context, c syslogConfig, m message, body []byte) error {
	pri := facilities[c.Facility]*8 + m.syslogSeverity()
	msg := fmt.Sprintf("<%d>1 %s %s monika - %s - %s", pri, m.At.UTC().Format(time.RFC3339Nano), s.hostname, m.Event, body)
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	var conn net.Conn
	var err error
	switch c.Protocol {
	case "udp":
		conn, err = s.dialer.DialContext(ctx, "udp", addr)
	case "tcp":
		conn, err = s.dialer.DialContext(ctx, "tcp", addr)
		msg = strconv.Itoa(len(msg)) + " " + msg
	case "tls":
		d := tls.Dialer{NetDialer: s.dialer, Config: s.tls}
		conn, err = d.DialContext(ctx, "tcp", addr)
		msg = strconv.Itoa(len(msg)) + " " + msg
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(sendTimeout))
	_, err = io.WriteString(conn, msg)
	return err
}

// webhook schickt das Ereignis an die URL; mit Vorlage wird der Body daraus gebildet.
func (s sender) webhook(ctx context.Context, c webhookConfig, body []byte, secret map[string]any) error {
	var event map[string]any
	_ = json.Unmarshal(body, &event)
	u, err := render(c.URL, inURL, event, secret)
	if err != nil {
		return err
	}
	if c.Body != "" {
		b, err := render(c.Body, inBody, event, nil)
		if err != nil {
			return err
		}
		body = []byte(b)
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(c.Method), u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "monika")
	for k, v := range c.Headers {
		hv, err := render(v, inHeader, event, secret)
		if err != nil {
			return err
		}
		req.Header.Set(k, hv)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		// Ohne URL: Sie kann Werte aus dem Secret enthalten, und der Fehler landet in Log und
		// last_error.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return fmt.Errorf("webhook %s: %w", req.Method, uerr.Err)
		}
		return fmt.Errorf("webhook %s failed", req.Method)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webhook: %s", resp.Status)
	}
	return nil
}
