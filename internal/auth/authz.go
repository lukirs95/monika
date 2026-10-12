package auth

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core"
)

// authz sind Rollen und Rechte im Speicher: pro Benutzer die Vereinigung der Rechte seiner Rollen.
type authz struct {
	all    Authz
	byUser map[uuid.UUID][]Grant
}

func newAuthz(a Authz) authz {
	byRole := map[uuid.UUID][]Grant{}
	for _, g := range a.Grants {
		byRole[g.RoleID] = append(byRole[g.RoleID], g.Grant)
	}
	byUser := map[uuid.UUID][]Grant{}
	for _, m := range a.Members {
		byUser[m.UserID] = append(byUser[m.UserID], byRole[m.RoleID]...)
	}
	return authz{all: a, byUser: byUser}
}

// Restore lädt Rollen und Rechte beim Start.
func (s *Service) Restore(ctx context.Context) error { return s.reloadAuthz(ctx) }

// reloadAuthz lädt Rollen und Rechte neu, nach jeder Änderung.
func (s *Service) reloadAuthz(ctx context.Context) error {
	a, err := s.cfg.Store.LoadAuthz(ctx)
	if err != nil {
		return fmt.Errorf("load roles and grants: %w", err)
	}
	z := newAuthz(a)
	s.mu.Lock()
	s.authz = z
	s.mu.Unlock()
	return nil
}

// Allowed meldet, ob der Benutzer das Recht am Element hat (docs/contracts/auth.md): Ein Recht
// seiner Rollen mit Geltungsbereich S gilt, wenn S leer ist oder das Element umfasst
// (View.InScope). Globale Rechte fragt man mit element = uuid.Nil. Rechte schließen einander nicht
// ein (M1); für kritische Aktionen fragt der Aufrufer zusätzlich nach critical.
func (s *Service) Allowed(v *core.View, p Principal, perm Permission, element uuid.UUID) bool {
	s.mu.Lock()
	grants := s.authz.byUser[p.UserID]
	s.mu.Unlock()
	for _, g := range grants {
		if g.Permission != perm {
			continue
		}
		if g.ScopeElementID == uuid.Nil || (element != uuid.Nil && v.InScope(g.ScopeElementID, element)) {
			return true
		}
	}
	return false
}

// Grants liefert die Rechte eines Benutzers (für die API, etwa um die Sicht zu filtern).
func (s *Service) Grants(user uuid.UUID) []Grant {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.authz.byUser[user])
}

// Authz liefert Rollen, Rechte und Mitgliedschaften (für die Verwaltung).
func (s *Service) Authz() Authz {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.authz.all
	return Authz{Roles: slices.Clone(a.Roles), Grants: slices.Clone(a.Grants), Members: slices.Clone(a.Members)}
}
