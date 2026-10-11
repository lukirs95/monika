package sdktest

import (
	"context"
	"time"

	"github.com/lukirs95/monika/sdk"
)

// Scheduler ist ein einfacher Scheduler für Tests: Er ruft fn sofort und danach im Abstand
// interval, ohne Versatz und ohne globale Obergrenze, bis ctx endet. Läuft der vorige Aufruf noch,
// entfällt der Takt.
type Scheduler struct{}

var _ sdk.Scheduler = Scheduler{}

func (Scheduler) Every(ctx context.Context, interval time.Duration, fn func(ctx context.Context) error) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			callCtx, cancel := context.WithTimeout(ctx, interval)
			_ = fn(callCtx) // Fehler protokolliert erst der echte Scheduler des Core
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}
