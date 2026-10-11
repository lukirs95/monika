package sdktest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

// ErrUnreachable meldet Execute, solange das Gerät offline ist.
var ErrUnreachable = errors.New("sdktest: device unreachable")

// Fake ist ein programmierbarer Treiber für Tests des Core. Jede Quelle wird über ihre Adresse
// (SourceConfig.Address) einem Device zugeordnet.
type Fake struct {
	Name    string
	Actions []sdk.ActionDef // nil = start, stop, reboot (kritisch), set
	Facts   []sdk.FactDef

	mu      sync.Mutex
	devices map[string]*Device
}

// NewFake liefert einen Fake-Treiber mit diesem Namen.
func NewFake(name string) *Fake { return &Fake{Name: name, devices: map[string]*Device{}} }

// Device legt ein Testgerät unter address an; s ist sein anfänglicher Stand. Es ist online.
func (f *Fake) Device(address string, s sdk.Snapshot) *Device {
	d := &Device{snapshot: cloneSnapshot(s), online: true, heartbeat: time.Second}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.devices[address] = d
	return d
}

// Driver liefert den Treiber zur Registrierung oder direkten Verwendung.
func (f *Fake) Driver() sdk.Driver {
	actions := f.Actions
	if actions == nil {
		actions = []sdk.ActionDef{
			{Name: vocab.ActionStart, Verify: []sdk.Condition{{Fact: vocab.StateRunning, Op: "eq", Value: "true"}}, Timeout: 10 * time.Second},
			{Name: vocab.ActionStop, Verify: []sdk.Condition{{Fact: vocab.StateRunning, Op: "eq", Value: "false"}}, Timeout: 10 * time.Second},
			{Name: vocab.ActionReboot, Critical: true},
			{Name: vocab.ActionSet},
		}
	}
	return sdk.Driver{Name: f.Name, Actions: actions, Facts: f.Facts, New: f.newSource}
}

func (f *Fake) newSource(cfg sdk.SourceConfig) (sdk.Source, error) {
	f.mu.Lock()
	d, ok := f.devices[cfg.Address]
	f.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("sdktest: no device at %q", cfg.Address)
	}
	if err := d.take(&d.newErr); err != nil {
		return nil, err
	}
	return &source{dev: d}, nil
}

// Device ist ein Testgerät. Seine Methoden wirken sofort auf eine laufende Source.
type Device struct {
	mu        sync.Mutex
	snapshot  sdk.Snapshot
	heartbeat time.Duration
	online    bool
	events    chan event // nil, solange keine Source läuft
	running   int
	newErr    error
	runErr    error
	panicRun  bool
	hang      chan struct{} // nicht nil: Run ignoriert den Abbruch, bis Release
	execute   func(sdk.Command) (sdk.Result, error)
	commands  []sdk.Command
}

type eventKind int

const (
	evSnapshot eventKind = iota
	evUpdate
	evBeat
	evOffline
	evOnline
	evFail
	evPanic
)

type event struct {
	kind eventKind
	snap sdk.Snapshot
	upd  sdk.Update
	err  error
}

// SetHeartbeat legt den Wert von Source.Heartbeat fest (Standard 1 s); er gilt ab der nächsten Source.
func (d *Device) SetHeartbeat(h time.Duration) { d.with(func() { d.heartbeat = h }) }

// SetSnapshot ersetzt den Stand (Strukturänderung); eine verbundene Source meldet ihn.
func (d *Device) SetSnapshot(s sdk.Snapshot) {
	s = cloneSnapshot(s)
	d.with(func() { d.snapshot = s })
	d.send(event{kind: evSnapshot, snap: cloneSnapshot(s)})
}

// SetFacts ändert Fakten eines Elements ("" = Zugang); eine verbundene Source meldet ein Update.
func (d *Device) SetFacts(externalID string, f sdk.Facts) {
	d.with(func() {
		if d.snapshot.Facts == nil {
			d.snapshot.Facts = map[string]sdk.Facts{}
		}
		merged := maps.Clone(d.snapshot.Facts[externalID])
		if merged == nil {
			merged = sdk.Facts{}
		}
		maps.Copy(merged, f)
		d.snapshot.Facts[externalID] = merged
	})
	d.send(event{kind: evUpdate, upd: sdk.Update{Facts: map[string]sdk.Facts{externalID: maps.Clone(f)}}})
}

// SetAlarms ersetzt die Alarme eines Elements; eine verbundene Source meldet ein Update.
func (d *Device) SetAlarms(externalID string, alarms []sdk.Alarm) {
	alarms = append([]sdk.Alarm{}, alarms...)
	d.with(func() {
		if d.snapshot.Alarms == nil {
			d.snapshot.Alarms = map[string][]sdk.Alarm{}
		}
		d.snapshot.Alarms[externalID] = alarms
	})
	d.send(event{kind: evUpdate, upd: sdk.Update{Alarms: map[string][]sdk.Alarm{externalID: alarms}}})
}

// Beat lässt eine verbundene Source ein Lebenszeichen ohne Inhalt melden.
func (d *Device) Beat() { d.send(event{kind: evBeat}) }

// GoOffline trennt die Verbindung; die Source meldet Disconnected und wartet auf GoOnline.
func (d *Device) GoOffline(err error) {
	d.with(func() { d.online = false })
	d.send(event{kind: evOffline, err: err})
}

// GoOnline macht das Gerät wieder erreichbar; die Source meldet Connected und einen Snapshot.
func (d *Device) GoOnline() {
	d.with(func() { d.online = true })
	d.send(event{kind: evOnline})
}

// FailNew lässt das nächste Driver.New mit err scheitern.
func (d *Device) FailNew(err error) { d.with(func() { d.newErr = err }) }

// FailRun lässt Run mit err enden: sofort, wenn eine Source läuft, sonst beim nächsten Start.
func (d *Device) FailRun(err error) {
	if !d.send(event{kind: evFail, err: err}) {
		d.with(func() { d.runErr = err })
	}
}

// PanicRun löst in Run eine Panic aus: sofort, wenn eine Source läuft, sonst beim nächsten Start.
func (d *Device) PanicRun() {
	if !d.send(event{kind: evPanic}) {
		d.with(func() { d.panicRun = true })
	}
}

// Hang lässt Run den Abbruch seines ctx ignorieren, bis Release gerufen wird.
func (d *Device) Hang() { d.with(func() { d.hang = make(chan struct{}) }) }

// Release beendet Hang.
func (d *Device) Release() {
	d.with(func() {
		if d.hang != nil {
			close(d.hang)
			d.hang = nil
		}
	})
}

// OnExecute legt die Antwort auf Execute fest; ohne sie nimmt das Gerät jeden Befehl an.
func (d *Device) OnExecute(fn func(sdk.Command) (sdk.Result, error)) {
	d.with(func() { d.execute = fn })
}

// Commands liefert alle Befehle, die Execute erhalten hat.
func (d *Device) Commands() []sdk.Command {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]sdk.Command(nil), d.commands...)
}

// Running liefert die Zahl laufender Run-Aufrufe (mehr als 1 hieße: zwei Instanzen).
func (d *Device) Running() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running
}

func (d *Device) with(fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	fn()
}

// send stellt ein Ereignis an die laufende Source zu und meldet, ob eine lief.
func (d *Device) send(e event) bool {
	d.mu.Lock()
	ch := d.events
	d.mu.Unlock()
	if ch == nil {
		return false
	}
	ch <- e
	return true
}

func (d *Device) take(p *error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	err := *p
	*p = nil
	return err
}

type source struct{ dev *Device }

func (s *source) Heartbeat() time.Duration {
	s.dev.mu.Lock()
	defer s.dev.mu.Unlock()
	return s.dev.heartbeat
}

func (s *source) Run(ctx context.Context, r sdk.Reporter) error {
	d := s.dev
	events := make(chan event, 64)
	d.mu.Lock()
	d.events, d.running = events, d.running+1
	runErr, panicRun := d.runErr, d.panicRun
	d.runErr, d.panicRun = nil, false
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		if d.events == events {
			d.events = nil
		}
		d.running--
		d.mu.Unlock()
	}()
	if panicRun {
		panic("sdktest: device panic")
	}
	if runErr != nil {
		return runErr
	}

	connected := false
	connect := func() {
		d.mu.Lock()
		online, snap := d.online, cloneSnapshot(d.snapshot)
		d.mu.Unlock()
		if online {
			r.Connected()
			r.Snapshot(snap)
			connected = true
		}
	}
	connect()
	for {
		select {
		case <-ctx.Done():
			d.mu.Lock()
			hang := d.hang
			d.mu.Unlock()
			if hang != nil {
				<-hang
			}
			return nil
		case e := <-events:
			switch e.kind {
			case evSnapshot:
				if connected {
					r.Snapshot(e.snap)
				}
			case evUpdate:
				if connected {
					r.Update(e.upd)
				}
			case evBeat:
				if connected {
					r.Heartbeat()
				}
			case evOffline:
				if connected {
					r.Disconnected(e.err)
					connected = false
				}
			case evOnline:
				if !connected {
					connect()
				}
			case evFail:
				return e.err
			case evPanic:
				panic("sdktest: device panic")
			}
		}
	}
}

func (s *source) Execute(_ context.Context, c sdk.Command) (sdk.Result, error) {
	d := s.dev
	d.mu.Lock()
	d.commands = append(d.commands, c)
	online, fn := d.online, d.execute
	d.mu.Unlock()
	switch {
	case !online:
		return sdk.Result{}, ErrUnreachable
	case fn != nil:
		return fn(c)
	}
	return sdk.Result{Status: sdk.Accepted}, nil
}

// cloneSnapshot kopiert die Maps und Listen eines Snapshots, damit Tests und Core nicht auf
// denselben Daten arbeiten.
func cloneSnapshot(s sdk.Snapshot) sdk.Snapshot {
	s.Elements = append([]sdk.Element(nil), s.Elements...)
	s.Links = append([]sdk.Link(nil), s.Links...)
	if s.Facts != nil {
		facts := make(map[string]sdk.Facts, len(s.Facts))
		for k, v := range s.Facts {
			facts[k] = maps.Clone(v)
		}
		s.Facts = facts
	}
	if s.Alarms != nil {
		alarms := make(map[string][]sdk.Alarm, len(s.Alarms))
		for k, v := range s.Alarms {
			alarms[k] = append([]sdk.Alarm(nil), v...)
		}
		s.Alarms = alarms
	}
	return s
}
