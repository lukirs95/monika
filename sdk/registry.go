package sdk

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/lukirs95/monika/sdk/vocab"
)

var (
	registryMu sync.RWMutex
	registry   = map[string]Driver{}

	driverName = regexp.MustCompile(`^[a-z0-9]+$`)
	rawName    = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)
)

// Register meldet einen Treiber an; aufzurufen in init(). Ein ungültiger Katalog oder ein
// doppelter Name ist ein Programmierfehler und führt zu panic.
func Register(d Driver) {
	if err := d.Validate(); err != nil {
		panic(fmt.Sprintf("sdk.Register(%q): %v", d.Name, err))
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[d.Name]; dup {
		panic(fmt.Sprintf("sdk.Register(%q): driver registered twice", d.Name))
	}
	registry[d.Name] = d
}

// Lookup liefert den registrierten Treiber mit diesem Namen.
func Lookup(name string) (Driver, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	d, ok := registry[name]
	return d, ok
}

// Drivers liefert alle registrierten Treiber, sortiert nach Namen.
func Drivers() []Driver {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Driver, 0, len(registry))
	for _, d := range registry {
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b Driver) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Validate prüft den Katalog des Treibers; Register ruft es und reagiert mit panic.
func (d Driver) Validate() error {
	var errs []error
	if !driverName.MatchString(d.Name) {
		errs = append(errs, errors.New("name must match [a-z0-9]+ (package name)"))
	}
	if vocab.Measurement(d.Name) {
		errs = append(errs, fmt.Errorf("name %q is a measurement of the vocabulary", d.Name))
	}
	if d.New == nil {
		errs = append(errs, errors.New("missing constructor (Driver.New)"))
	}
	keys := map[string]bool{}
	for _, f := range d.Facts {
		name, ok := strings.CutPrefix(f.Key, d.Name+".")
		switch {
		case !ok || !rawName.MatchString(name):
			errs = append(errs, fmt.Errorf("fact %q: raw facts are %s.<snake_case>", f.Key, d.Name))
		case keys[f.Key]:
			errs = append(errs, fmt.Errorf("fact %q declared twice", f.Key))
		case !f.Type.Valid() || !f.Kind.Valid():
			errs = append(errs, fmt.Errorf("fact %q: invalid type %q or kind %q", f.Key, f.Type, f.Kind))
		case f.Kind == vocab.Counter && f.Type != vocab.Int:
			errs = append(errs, fmt.Errorf("fact %q: counters are int", f.Key))
		case strings.HasSuffix(f.Key, vocab.RateSuffix):
			errs = append(errs, fmt.Errorf("fact %q: rates are derived from counters, not declared", f.Key))
		}
		keys[f.Key] = true
	}
	actions := map[string]bool{}
	for _, a := range d.Actions {
		switch {
		case actions[a.Name]:
			errs = append(errs, fmt.Errorf("action %q declared twice", a.Name))
		case !vocab.Action(a.Name) && !strings.HasPrefix(a.Name, d.Name+"."):
			errs = append(errs, fmt.Errorf("action %q: raw actions are %s.<name>", a.Name, d.Name))
		case a.Timeout < 0:
			errs = append(errs, fmt.Errorf("action %q: negative timeout", a.Name))
		}
		actions[a.Name] = true
		for _, c := range a.Verify {
			if _, ok := d.FactType(c.Fact); !ok {
				errs = append(errs, fmt.Errorf("action %q: verify uses unknown fact %q", a.Name, c.Fact))
			}
		}
	}
	return errors.Join(errs...)
}

// FactType liefert den Typ eines Fakt-Schlüssels: aus dem Vokabular oder dem Katalog des
// Treibers, jeweils auch die Rate eines Zählers.
func (d Driver) FactType(key string) (FactType, bool) {
	if f, ok := vocab.Lookup(key); ok {
		return f.Type, true
	}
	base, rate := strings.CutSuffix(key, vocab.RateSuffix)
	for _, f := range d.Facts {
		switch {
		case f.Key == key:
			return f.Type, true
		case rate && f.Key == base && f.Kind == vocab.Counter:
			return vocab.Float, true
		}
	}
	return "", false
}

// CheckElement prüft ein gemeldetes Element gegen Vokabular und Katalog. Der Core ruft es für
// jedes Element eines Snapshots; sdktest.Conformance ebenso.
func (d Driver) CheckElement(e Element) error {
	var errs []error
	if e.ExternalID == "" {
		errs = append(errs, errors.New("external ID is empty (reserved for the access element)"))
	}
	switch {
	case vocab.ReservedElementKind(e.Kind):
		errs = append(errs, fmt.Errorf("kind %q is reserved", e.Kind))
	case !vocab.ElementKind(e.Kind):
		errs = append(errs, fmt.Errorf("unknown kind %q", e.Kind))
	}
	if !vocab.Transport(e.Transport) {
		errs = append(errs, fmt.Errorf("unknown transport %q", e.Transport))
	}
	if !vocab.Direction(e.Direction) {
		errs = append(errs, fmt.Errorf("unknown direction %q", e.Direction))
	}
	if e.GlobalRef != "" {
		if err := d.CheckGlobalRef(e.GlobalRef); err != nil {
			errs = append(errs, err)
		}
	}
	for _, a := range e.Actions {
		if !slices.ContainsFunc(d.Actions, func(def ActionDef) bool { return def.Name == a }) {
			errs = append(errs, fmt.Errorf("action %q is not in the catalog", a))
		}
	}
	for _, a := range e.CriticalActions {
		if !slices.Contains(e.Actions, a) {
			errs = append(errs, fmt.Errorf("critical action %q is not an action of the element", a))
		}
	}
	for key, s := range e.Settings {
		want, ok := FactTypeOf(s.Type)
		if !ok {
			errs = append(errs, fmt.Errorf("setting %q: unknown type %q", key, s.Type))
			continue
		}
		got, known := d.FactType(key)
		switch {
		case s.WriteOnly && known:
			errs = append(errs, fmt.Errorf("setting %q: write-only settings have no fact, but %q is one", key, key))
		case !s.WriteOnly && !known:
			errs = append(errs, fmt.Errorf("setting %q: no such fact", key))
		case !s.WriteOnly && got != want:
			errs = append(errs, fmt.Errorf("setting %q: type %s does not match fact type %s", key, s.Type, got))
		}
	}
	for action, params := range e.Params {
		for name, p := range params {
			if _, ok := FactTypeOf(p.Type); !ok {
				errs = append(errs, fmt.Errorf("param %q of %q: unknown type %q", name, action, p.Type))
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("element %q: %w", e.ExternalID, err)
	}
	return nil
}

// CheckGlobalRef prüft eine global_ref am eigenen Baum (Element.GlobalRef, Snapshot.GlobalRef):
// Sie hat die Form <namensraum>:<id>, und der Namensraum gehört diesem Treiber.
func (d Driver) CheckGlobalRef(ref string) error {
	ns, id, ok := strings.Cut(ref, ":")
	if !ok || ns == "" || id == "" {
		return fmt.Errorf("global ref %q: want <namespace>:<id>", ref)
	}
	if owner, ok := vocab.NamespaceOwner(ns); !ok || owner != d.Name {
		return fmt.Errorf("global ref %q: namespace %q does not belong to driver %q", ref, ns, d.Name)
	}
	return nil
}
