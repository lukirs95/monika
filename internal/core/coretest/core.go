package coretest

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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

// Options beschreiben den Anfangsstand eines laufenden Core.
type Options struct {
	Snapshot inventory.Snapshot
	Rules    []evaluation.Rule
	Findings []evaluation.Finding // aktive Befunde
	Drivers  []sdk.Driver         // registrierte Treiber; ohne Angabe nur Driver ohne Katalog
	Secrets  core.Secrets         // für ReplaceSecret; ohne Angabe scheitert der Befehl
}

// Running ist ein laufender Core mit Inventar und Bewertung, aber ohne Datenbank, Supervisor
// und Secrets: für Tests der API und der Dienste gegen die echte Logik der Schleife.
type Running struct {
	*core.Core
	Stores *Stores
}

// Start startet einen Core mit dem Anfangsstand; er läuft bis zum Ende des Tests.
func Start(t testing.TB, o Options) *Running {
	t.Helper()
	if o.Drivers == nil {
		o.Drivers = []sdk.Driver{{Name: Driver}}
	}
	lookup := func(name string) (sdk.Driver, bool) {
		for _, d := range o.Drivers {
			if d.Name == name {
				return d, true
			}
		}
		return sdk.Driver{}, false
	}
	types := func(key string) (vocab.Type, bool) {
		if f, ok := vocab.Lookup(key); ok {
			return f.Type, true
		}
		for _, d := range o.Drivers {
			if typ, ok := d.FactType(key); ok {
				return typ, true
			}
		}
		return "", false
	}
	log := slog.New(slog.DiscardHandler)
	stores := &Stores{InUse: map[uuid.UUID][]inventory.PresetRef{}}
	inv := inventory.New(stores, lookup, log)
	inv.Load(o.Snapshot)
	ev := evaluation.New(stores, types, func(name string) bool { _, ok := lookup(name); return ok }, log)
	ev.Load(o.Rules, o.Findings, nil)

	if o.Secrets == nil {
		o.Secrets = noSecrets{}
	}
	c := core.New(core.Config{Inventory: inv, Evaluator: ev, Secrets: o.Secrets, Log: log})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("core: %v", err)
		}
	})
	for deadline := time.Now().Add(5 * time.Second); c.View() == nil; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("core did not publish a view")
		}
	}
	return &Running{Core: c, Stores: stores}
}

type noSecrets struct{}

func (noSecrets) Replace(context.Context, uuid.UUID, uuid.UUID, json.RawMessage) error {
	return errors.New("coretest: no secrets configured")
}

// Stores ist der Inventar- und der Evaluation-Store im Speicher: Jeder Schreibvorgang gelingt,
// gespeichert wird nichts; der Zustand liegt im Core.
type Stores struct {
	mu    sync.Mutex
	InUse map[uuid.UUID][]inventory.PresetRef // Gerät, Quelle oder Gruppe → Presets, die es verwenden
}

func (s *Stores) inUse(id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if refs := s.InUse[id]; len(refs) > 0 {
		return &inventory.InUseError{Presets: refs}
	}
	return nil
}

func (*Stores) Load(context.Context) (inventory.Snapshot, error) { return inventory.Snapshot{}, nil }
func (*Stores) ApplyDiscovery(context.Context, inventory.Discovery) ([]uuid.UUID, error) {
	return nil, nil
}
func (*Stores) CreateDevice(context.Context, uuid.UUID, inventory.Element) error { return nil }
func (s *Stores) DeleteDevice(_ context.Context, _ uuid.UUID, id uuid.UUID) error {
	return s.inUse(id)
}
func (*Stores) CreateSource(context.Context, uuid.UUID, inventory.Source, uuid.UUID) error {
	return nil
}
func (*Stores) UpdateSource(context.Context, uuid.UUID, inventory.Source) error { return nil }
func (s *Stores) DeleteSource(_ context.Context, _ uuid.UUID, id uuid.UUID) error {
	return s.inUse(id)
}
func (*Stores) CreateGroup(context.Context, uuid.UUID, inventory.Element) error { return nil }
func (s *Stores) DeleteGroup(_ context.Context, _ uuid.UUID, id uuid.UUID) error {
	return s.inUse(id)
}
func (*Stores) UpdateUserFields(context.Context, uuid.UUID, uuid.UUID, inventory.UserFields) error {
	return nil
}
func (*Stores) AddGroupMember(context.Context, uuid.UUID, inventory.Link) error    { return nil }
func (*Stores) UpdateGroupMember(context.Context, uuid.UUID, inventory.Link) error { return nil }
func (*Stores) RemoveGroupMember(context.Context, uuid.UUID, uuid.UUID) error      { return nil }

func (*Stores) ListRules(context.Context) ([]evaluation.Rule, error)                { return nil, nil }
func (*Stores) CreateRule(context.Context, uuid.UUID, evaluation.Rule) error        { return nil }
func (*Stores) UpdateRule(context.Context, uuid.UUID, evaluation.Rule) error        { return nil }
func (*Stores) DeleteRule(context.Context, uuid.UUID, uuid.UUID) error              { return nil }
func (*Stores) ListActiveFindings(context.Context) ([]evaluation.Finding, error)    { return nil, nil }
func (*Stores) RaiseFinding(context.Context, evaluation.Finding) error              { return nil }
func (*Stores) EndFinding(context.Context, uuid.UUID, evaluation.ClearReason) error { return nil }
func (*Stores) SetFindingCause(context.Context, uuid.UUID, uuid.UUID) error         { return nil }
func (*Stores) AckFinding(context.Context, uuid.UUID, uuid.UUID) error              { return nil }
func (*Stores) LoadHealth(context.Context) ([]evaluation.ElementHealth, error)      { return nil, nil }
func (*Stores) SaveHealth(context.Context, []evaluation.ElementHealth) error        { return nil }

var (
	_ inventory.Store  = (*Stores)(nil)
	_ evaluation.Store = (*Stores)(nil)
)
