package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"

	"github.com/lukirs95/monika/internal/auth"
)

// SessionCookie ist das Cookie der Sitzung (Spezifikation, securitySchemes.session).
const SessionCookie = "monika_session"

// readOnlyAllowed sind Operationen, die ein Principal mit read_only trotz POST darf: Sie ändern
// nichts am Zustand (api.md, Ändernde Anfragen). Die eingebettete Spezifikation trägt die
// operationId so, wie oapi-codegen sie als Go-Namen schreibt.
var readOnlyAllowed = map[string]bool{"Logout": true, "Watch": true}

// guard ist der Weg jeder Anfrage vor dem Handler: Operation nach der Spezifikation finden,
// anmelden (außer bei öffentlichen Operationen), read_only und Origin prüfen, Anfrage gegen die
// Spezifikation prüfen (R9).
func (s *Server) guard(next http.Handler) http.Handler {
	// Das Format uuid prüft kin-openapi nicht; das übernimmt der erzeugte Code beim Lesen von
	// Parametern und Body (ebenfalls 400 invalid_request).
	opts := &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, params, err := s.router.FindRoute(r)
		switch {
		case errors.Is(err, routers.ErrMethodNotAllowed):
			writeProblem(w, problem(http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed"))
			return
		case err != nil:
			writeProblem(w, problem(http.StatusNotFound, "not_found", "unknown path"))
			return
		}
		ctx, session := r.Context(), false
		if !public(route.Operation) {
			p, viaSession, err := s.authenticate(r)
			if err == nil && !s.schemeAllowed(route.Operation, viaSession) {
				err = errUnauthenticated
			}
			if err != nil {
				s.responseError(w, r, err)
				return
			}
			if p.ReadOnly && changes(r.Method) && !readOnlyAllowed[route.Operation.OperationID] {
				writeProblem(w, errReadOnly)
				return
			}
			ctx, session = withPrincipal(ctx, p), viaSession
		}
		if !s.originOK(r, session) {
			writeProblem(w, errOrigin)
			return
		}
		in := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: opts}
		if err := openapi3filter.ValidateRequest(ctx, in); err != nil {
			writeProblem(w, problem(http.StatusBadRequest, "invalid_request", validationDetail(err)))
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// public meldet Operationen ohne Anmeldung (security: [] in der Spezifikation).
func public(op *openapi3.Operation) bool { return op.Security != nil && len(*op.Security) == 0 }

// schemeAllowed meldet, ob die Operation das verwendete Verfahren (Sitzung oder API-Token)
// zulässt: ihr eigenes security, sonst das der Spezifikation (logout etwa nur die Sitzung).
func (s *Server) schemeAllowed(op *openapi3.Operation, session bool) bool {
	reqs := s.security
	if op.Security != nil {
		reqs = *op.Security
	}
	scheme := "bearer"
	if session {
		scheme = "session"
	}
	for _, req := range reqs {
		if _, ok := req[scheme]; ok {
			return true
		}
	}
	return false
}

// changes meldet ändernde Methoden.
func changes(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// authenticate meldet die Anfrage an: API-Token aus Authorization, sonst die Sitzung aus dem
// Cookie. Ein falsches Token fällt nicht auf das Cookie zurück.
func (s *Server) authenticate(r *http.Request) (p auth.Principal, session bool, err error) {
	if h := r.Header.Get("Authorization"); h != "" {
		bearer, ok := strings.CutPrefix(h, "Bearer ")
		if !ok {
			return auth.Principal{}, false, errUnauthenticated
		}
		p, err := s.cfg.Auth.AuthenticateToken(r.Context(), bearer)
		return p, false, err
	}
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return auth.Principal{}, false, errUnauthenticated
	}
	p, err = s.cfg.Auth.Authenticate(r.Context(), c.Value)
	return p, true, err
}

// originOK prüft den Origin-Header ändernder Anfragen (auth.md, A4): Ist er gesetzt, muss er die
// Herkunft der öffentlichen URL sein. Fehlt er, ist das nur ohne Sitzung erlaubt (API-Token,
// Werkzeuge wie curl); Browser senden ihn bei ändernden Anfragen immer.
func (s *Server) originOK(r *http.Request, session bool) bool {
	if !changes(r.Method) {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return !session
	}
	return origin == s.origin
}

// validationDetail beschreibt, was an der Anfrage nicht passt, ohne den Wert zu nennen: Er kann
// ein Passwort oder Secret sein.
func validationDetail(err error) string {
	re, ok := errors.AsType[*openapi3filter.RequestError](err)
	if !ok {
		return "request does not match the specification"
	}
	where := "request body"
	if re.Parameter != nil {
		where = re.Parameter.In + " parameter " + re.Parameter.Name
	}
	if se, ok := errors.AsType[*openapi3.SchemaError](re.Err); ok {
		if ptr := se.JSONPointer(); len(ptr) > 0 {
			where += " /" + strings.Join(ptr, "/")
		}
		return where + ": " + se.Reason
	}
	if re.Reason != "" {
		return where + ": " + re.Reason
	}
	return where + " is invalid"
}

type principalKey struct{}

func withPrincipal(ctx context.Context, p auth.Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// principal liefert den angemeldeten Benutzer; bei öffentlichen Operationen den leeren.
func principal(ctx context.Context) auth.Principal {
	p, _ := ctx.Value(principalKey{}).(auth.Principal)
	return p
}
