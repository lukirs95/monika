package core

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
)

// ErrStopped bedeutet, dass die Schleife nicht mehr läuft.
var ErrStopped = errors.New("core stopped")

// Do übergibt einen Befehl der API an die Schleife und wartet, bis sie ihn verarbeitet und die
// neue View veröffentlicht hat. Endet ctx vorher, liefert Do ctx.Err(); der Befehl kann trotzdem
// ausgeführt werden.
func (c *Core) Do(ctx context.Context, cmd Command) error {
	reply := make(chan error, 1)
	select {
	case c.queue <- entry{at: c.now(), cmd: cmd, reply: reply}:
	case <-c.done:
		return ErrStopped
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-reply:
		return err
	case <-c.done:
		select {
		case err := <-reply:
			return err
		default:
			return ErrStopped
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

// handleCommand übersetzt einen Befehl in Methoden von Inventar und Evaluator (Entscheidung B)
// und beauftragt den Supervisor, wo Quellen betroffen sind.
func (c *Core) handleCommand(ctx context.Context, r *round, cmd Command) error {
	st := c.state(r.now)
	g := c.inv.Graph()
	switch cmd := cmd.(type) {
	case CreateDevice:
		diff, err := c.inv.CreateDevice(ctx, cmd.Actor, cmd.Device)
		return c.afterInventory(ctx, r, diff, err, diff.Added)
	case DeleteDevice:
		var sources []uuid.UUID
		for id, s := range g.Sources() {
			if s.DeviceID == cmd.ID {
				sources = append(sources, id)
			}
		}
		diff, err := c.inv.DeleteDevice(ctx, cmd.Actor, cmd.ID)
		if err == nil {
			c.forgetSources(r, sources)
		}
		return c.afterInventory(ctx, r, diff, err, diff.Deleted)
	case CreateSource:
		diff, err := c.inv.CreateSource(ctx, cmd.Actor, cmd.Source)
		if src, ok := c.inv.Source(cmd.Source.ID); err == nil && ok && src.Enabled {
			c.startSource(r, src)
		}
		return c.afterInventory(ctx, r, diff, err, diff.Added)
	case UpdateSource:
		diff, err := c.inv.UpdateSource(ctx, cmd.Actor, cmd.Source)
		if src, ok := c.inv.Source(cmd.Source.ID); err == nil && ok {
			c.restartSource(r, src)
		}
		return c.afterInventory(ctx, r, diff, err, c.sourceElements(cmd.Source.ID))
	case DeleteSource:
		diff, err := c.inv.DeleteSource(ctx, cmd.Actor, cmd.ID)
		if err == nil {
			c.forgetSources(r, []uuid.UUID{cmd.ID})
		}
		return c.afterInventory(ctx, r, diff, err, diff.Deleted)
	case CreateGroup:
		diff, err := c.inv.CreateGroup(ctx, cmd.Actor, cmd.Group)
		return c.afterInventory(ctx, r, diff, err, diff.Added)
	case DeleteGroup:
		diff, err := c.inv.DeleteGroup(ctx, cmd.Actor, cmd.ID)
		return c.afterInventory(ctx, r, diff, err, diff.Deleted)
	case UpdateUserFields:
		diff, err := c.inv.UpdateUserFields(ctx, cmd.Actor, cmd.ID, cmd.Fields)
		return c.afterInventory(ctx, r, diff, err, scopeOf(c.inv.Graph(), cmd.ID))
	case AddGroupMember:
		diff, err := c.inv.AddGroupMember(ctx, cmd.Actor, cmd.Link)
		return c.afterInventory(ctx, r, diff, err, scopeOf(c.inv.Graph(), cmd.Link.ToID))
	case UpdateGroupMember:
		diff, err := c.inv.UpdateGroupMember(ctx, cmd.Actor, cmd.Link)
		return c.afterInventory(ctx, r, diff, err, nil)
	case RemoveGroupMember:
		l, _ := g.Link(cmd.ID)
		diff, err := c.inv.RemoveGroupMember(ctx, cmd.Actor, cmd.ID)
		return c.afterInventory(ctx, r, diff, err, scopeOf(c.inv.Graph(), l.ToID))
	case CreateRule:
		changes, err := c.ev.CreateRule(ctx, r.now, cmd.Actor, cmd.Rule, st)
		return c.afterRule(r, changes, err)
	case UpdateRule:
		changes, err := c.ev.UpdateRule(ctx, r.now, cmd.Actor, cmd.Rule, st)
		return c.afterRule(r, changes, err)
	case DeleteRule:
		changes, err := c.ev.DeleteRule(ctx, r.now, cmd.Actor, cmd.ID, st)
		return c.afterRule(r, changes, err)
	case AckFinding:
		f, err := c.ev.AckFinding(ctx, r.now, cmd.Actor, cmd.ID)
		if err != nil {
			return c.commandErr(r, err)
		}
		c.findings.set(f.ID, f)
		r.events = append(r.events, FindingAcked{Finding: f})
	case ReplaceSecret:
		if err := c.secrets.Replace(ctx, cmd.Actor, cmd.ID, cmd.Value); err != nil {
			return err
		}
		for _, id := range slices.SortedFunc(maps.Keys(maps.Collect(g.Sources())), compareIDs) {
			if src, _ := c.inv.Source(id); src.SecretID == cmd.ID && src.Enabled {
				c.restartSource(r, src)
			}
		}
	}
	return nil
}

// afterInventory übernimmt eine Änderung am Inventar: Ereignis, Fakten gelöschter Elemente,
// Bewertung und Health der betroffenen Elemente.
func (c *Core) afterInventory(ctx context.Context, r *round, diff inventory.Diff, err error, affected []uuid.UUID) error {
	if err != nil {
		return c.commandErr(r, err)
	}
	c.inventoryChanged(r, diff)
	if len(c.facts.Drop(diff.Deleted)) > 0 {
		r.facts = true
	}
	changes, err := c.ev.Evaluate(ctx, r.now, affected, c.state(r.now))
	c.applyFindings(r, changes, err)
	for _, id := range affected {
		r.health[id] = true
	}
	return nil
}

// afterRule trennt das Ergebnis eines Regelbefehls: Gescheiterte Schreibvorgänge von Befunden
// betreffen die Datenbank, nicht den Befehl.
func (c *Core) afterRule(r *round, changes []evaluation.FindingChange, err error) error {
	if err != nil && !errors.Is(err, evaluation.ErrWrite) {
		return c.commandErr(r, err)
	}
	c.applyFindings(r, changes, err)
	return nil
}

// commandErr liefert den Fehler eines Befehls; ist er kein fachlicher, gilt die Datenbank als
// nicht in Ordnung.
func (c *Core) commandErr(r *round, err error) error {
	var inUse *inventory.InUseError
	domain := errors.As(err, &inUse)
	for _, e := range []error{
		inventory.ErrNotFound, inventory.ErrMemberCycle, inventory.ErrUnknownDriver, inventory.ErrNotAGroup,
		inventory.ErrMissingID, inventory.ErrInvalidConfig, inventory.ErrSourceExists, inventory.ErrMemberExists, inventory.ErrSecretNotFound,
		evaluation.ErrNotFound, evaluation.ErrInvalidRule, evaluation.ErrAlreadyActive,
	} {
		domain = domain || errors.Is(err, e)
	}
	if !domain {
		c.writeFailed(r, err)
	}
	return err
}

// startSource beauftragt den Supervisor, eine Quelle zu starten.
func (c *Core) startSource(r *round, src inventory.Source) {
	c.live.Starting(src.ID, r.now)
	if c.sup != nil {
		c.sup.Start(SourceSpec{
			SourceID: src.ID, DeviceID: src.DeviceID, Driver: src.Driver,
			Address: src.Address, Config: src.Config, SecretID: src.SecretID,
		})
	}
}

// restartSource stoppt eine Quelle und startet sie neu, wenn sie aktiviert ist (geänderte
// Quelle, ersetztes Secret).
func (c *Core) restartSource(r *round, src inventory.Source) {
	if c.sup != nil {
		c.sup.Stop(src.ID)
	}
	if src.Enabled {
		c.startSource(r, src)
		return
	}
	c.live.Stopping(src.ID, r.now)
}

// forgetSources stoppt gelöschte Quellen und vergisst alles, was die Schleife zu ihnen hält.
func (c *Core) forgetSources(r *round, ids []uuid.UUID) {
	for _, id := range ids {
		if c.sup != nil {
			c.sup.Stop(id)
		}
		c.live.Remove(id)
		delete(c.pending, id)
		delete(c.discoveryFailed, id)
		delete(c.warned, id)
		delete(c.wasLive, id)
		if len(c.facts.DropSource(id)) > 0 {
			r.facts = true
		}
		c.mbMu.Lock()
		mb := c.mailboxes[id]
		delete(c.mailboxes, id)
		c.mbMu.Unlock()
		mb.close()
	}
}

// scopeOf liefert alle Elemente im Geltungsbereich eines Elements; uuid.Nil = keine.
func scopeOf(g *inventory.Graph, id uuid.UUID) []uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return slices.Collect(g.ScopeElements(id))
}
