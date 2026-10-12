package api

import (
	"context"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/api/oapi"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/forward"
	"github.com/lukirs95/monika/internal/secret"
)

// Verwaltung: Weiterleitung und Secrets (admin; Secrets auflisten auch mit source_edit, um sie
// Quellen zuzuweisen). Werte von Secrets gehen nie zurück und nie ins Log.

// requireGlobal prüft globale Rechte (eines davon genügt) und liefert den Benutzer. Globale
// Rechte brauchen keinen Geltungsbereich und damit keine View; die Verwaltung geht auch, wenn
// der Core (noch) nichts veröffentlicht hat.
func (s *Server) requireGlobal(ctx context.Context, perms ...auth.Permission) (auth.Principal, error) {
	p := principal(ctx)
	for _, perm := range perms {
		if s.cfg.Auth.Allowed(s.cfg.Core.View(), p, perm, uuid.Nil) {
			return p, nil
		}
	}
	return p, errForbidden
}

// requireAdmin prüft das Recht admin.
func (s *Server) requireAdmin(ctx context.Context) (auth.Principal, error) {
	return s.requireGlobal(ctx, auth.PermAdmin)
}

func (s *Server) ListForwardTargets(ctx context.Context, _ oapi.ListForwardTargetsRequestObject) (oapi.ListForwardTargetsResponseObject, error) {
	if _, err := s.requireAdmin(ctx); err != nil {
		return nil, err
	}
	targets, err := s.cfg.Forwarder.ListTargets(ctx)
	if err != nil {
		return nil, err
	}
	out := oapi.ListForwardTargets200JSONResponse{}
	for _, t := range targets {
		out = append(out, toForwardTarget(t))
	}
	return out, nil
}

// forwardTarget liest ein Ziel nach dem Schreiben, mit dem Ergebnis des letzten Versands.
func (s *Server) forwardTarget(ctx context.Context, id uuid.UUID) (oapi.ForwardTarget, error) {
	targets, err := s.cfg.Forwarder.ListTargets(ctx)
	if err != nil {
		return oapi.ForwardTarget{}, err
	}
	for _, t := range targets {
		if t.ID == id {
			return toForwardTarget(t), nil
		}
	}
	return oapi.ForwardTarget{}, errNotFound
}

func (s *Server) CreateForwardTarget(ctx context.Context, rq oapi.CreateForwardTargetRequestObject) (oapi.CreateForwardTargetResponseObject, error) {
	p, err := s.requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	t := fromForwardTargetInput(newID(), *rq.Body)
	if err := s.cfg.Forwarder.CreateTarget(ctx, p.UserID, t); err != nil {
		return nil, err
	}
	out, err := s.forwardTarget(ctx, t.ID)
	return oapi.CreateForwardTarget201JSONResponse(out), err
}

func (s *Server) UpdateForwardTarget(ctx context.Context, rq oapi.UpdateForwardTargetRequestObject) (oapi.UpdateForwardTargetResponseObject, error) {
	p, err := s.requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.cfg.Forwarder.UpdateTarget(ctx, p.UserID, fromForwardTargetInput(rq.TargetID, *rq.Body)); err != nil {
		return nil, err
	}
	out, err := s.forwardTarget(ctx, rq.TargetID)
	return oapi.UpdateForwardTarget200JSONResponse(out), err
}

func (s *Server) DeleteForwardTarget(ctx context.Context, rq oapi.DeleteForwardTargetRequestObject) (oapi.DeleteForwardTargetResponseObject, error) {
	p, err := s.requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.cfg.Forwarder.DeleteTarget(ctx, p.UserID, rq.TargetID); err != nil {
		return nil, err
	}
	return oapi.DeleteForwardTarget204Response{}, nil
}

func (s *Server) ListSecrets(ctx context.Context, _ oapi.ListSecretsRequestObject) (oapi.ListSecretsResponseObject, error) {
	if _, err := s.requireGlobal(ctx, auth.PermAdmin, auth.PermSourceEdit); err != nil {
		return nil, err
	}
	secrets, err := s.cfg.Secrets.List(ctx)
	if err != nil {
		return nil, err
	}
	out := oapi.ListSecrets200JSONResponse{}
	for _, sec := range secrets {
		out = append(out, toSecret(sec))
	}
	return out, nil
}

func (s *Server) CreateSecret(ctx context.Context, rq oapi.CreateSecretRequestObject) (oapi.CreateSecretResponseObject, error) {
	p, err := s.requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	sec, err := s.cfg.Secrets.Create(ctx, p.UserID, rq.Body.Name, value(rq.Body.Description), rq.Body.Value)
	if err != nil {
		return nil, err
	}
	return oapi.CreateSecret201JSONResponse(toSecret(sec)), nil
}

func (s *Server) DeleteSecret(ctx context.Context, rq oapi.DeleteSecretRequestObject) (oapi.DeleteSecretResponseObject, error) {
	p, err := s.requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.cfg.Secrets.Delete(ctx, p.UserID, rq.SecretID); err != nil {
		return nil, err
	}
	return oapi.DeleteSecret204Response{}, nil
}

func (s *Server) ReplaceSecretValue(ctx context.Context, rq oapi.ReplaceSecretValueRequestObject) (oapi.ReplaceSecretValueResponseObject, error) {
	p, err := s.requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.cfg.Core.Do(ctx, core.ReplaceSecret{Actor: p.UserID, ID: rq.SecretID, Value: rq.Body.Value}); err != nil {
		return nil, err
	}
	return oapi.ReplaceSecretValue204Response{}, nil
}

func fromForwardTargetInput(id uuid.UUID, in oapi.ForwardTargetInput) forward.Target {
	t := forward.Target{
		ID: id, Name: in.Name, Kind: forward.Kind(in.Kind), Enabled: in.Enabled, Config: in.Config,
		SecretID: valueID(in.SecretID), ScopeElementID: valueID(in.ScopeElementID),
	}
	for _, e := range in.Events {
		t.Events = append(t.Events, string(e))
	}
	if in.MinSeverity != nil {
		t.MinSeverity = evaluation.Severity(*in.MinSeverity)
	}
	return t
}

func toForwardTarget(t forward.Target) oapi.ForwardTarget {
	out := oapi.ForwardTarget{
		ID: t.ID, Name: t.Name, Kind: oapi.ForwardTargetKind(t.Kind), Enabled: t.Enabled, Config: t.Config,
		SecretID: optionalID(t.SecretID), ScopeElementID: optionalID(t.ScopeElementID), Events: []oapi.ForwardTargetEvents{},
		LastSentAt: optionalTime(t.LastSentAt), LastError: optional(t.LastError),
	}
	for _, e := range t.Events {
		out.Events = append(out.Events, oapi.ForwardTargetEvents(e))
	}
	if t.MinSeverity != "" {
		out.MinSeverity = new(oapi.ForwardTargetMinSeverity(t.MinSeverity))
	}
	return out
}

func toSecret(s secret.Secret) oapi.Secret {
	return oapi.Secret{ID: s.ID, Name: s.Name, Description: optional(s.Description), CreatedAt: s.CreatedAt.UTC(), UpdatedAt: s.UpdatedAt.UTC()}
}
