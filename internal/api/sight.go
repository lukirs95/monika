package api

import (
	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/inventory"
)

// sight ist, was ein Benutzer in einer View sieht (api.md, Sichtbarkeit): Elemente im
// Geltungsbereich seiner view-Rechte, aufgelöst wie View.InScope.
type sight struct {
	v       *core.View
	all     bool               // view global
	visible map[uuid.UUID]bool // sonst: Elemente mit eigenem view-Recht
	presets bool               // preset_run oder preset_edit: Wurzel-Läufe von Presets
}

func (s *Server) sight(v *core.View, p auth.Principal) sight {
	si := sight{v: v, visible: map[uuid.UUID]bool{}}
	for _, g := range s.cfg.Auth.Grants(p.UserID) {
		switch g.Permission {
		case auth.PermPresetRun, auth.PermPresetEdit:
			si.presets = true
		case auth.PermView:
			if g.ScopeElementID == uuid.Nil {
				si.all = true
				continue
			}
			for id := range v.Graph.ScopeElements(g.ScopeElementID) {
				si.visible[id] = true
			}
		}
	}
	return si
}

// sees meldet, ob das Element sichtbar ist; Gerüst-Elemente sind es nicht.
func (si sight) sees(id uuid.UUID) bool {
	if _, ok := si.v.Graph.Element(id); !ok {
		return false
	}
	return si.all || si.visible[id]
}

// skeleton liefert die Vorfahren sichtbarer Elemente, die selbst nicht sichtbar sind.
func (si sight) skeleton() []uuid.UUID {
	if si.all {
		return nil
	}
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	for id := range si.visible {
		for a := range si.v.Graph.Ancestors(id) {
			if si.visible[a] || seen[a] {
				break // weiter oben ist schon alles erfasst
			}
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

// seesLink meldet, ob ein Link sichtbar ist: Beide Enden sind sichtbar. Ein unaufgelöstes Ende
// (nur global_ref) ist kein Objekt in Monika und zählt nicht.
func (si sight) seesLink(l inventory.Link) bool {
	return (l.FromID == uuid.Nil || si.sees(l.FromID)) && (l.ToID == uuid.Nil || si.sees(l.ToID)) &&
		(l.FromID != uuid.Nil || l.ToID != uuid.Nil)
}

// seesRun meldet, ob ein Lauf sichtbar ist: sein Ziel, bei Wurzeln von Presets das Recht dazu.
func (si sight) seesRun(elementID uuid.UUID) bool {
	if elementID == uuid.Nil {
		return si.presets
	}
	return si.sees(elementID)
}
