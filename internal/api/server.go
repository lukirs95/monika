// Package api ist die HTTP-API: Anmeldung prüfen, Rechte prüfen, Fachpaket aufrufen, Fehler
// übersetzen (docs/contracts/api.md). Typen, Router und Server-Interface erzeugt oapi-codegen in
// internal/api/oapi (make api-gen).
package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/lukirs95/monika/internal/api/oapi"
	"github.com/lukirs95/monika/internal/auth"
)

// Prefix ist der Pfad, unter dem die API liegt (R3).
const Prefix = "/api/v1"

// Authenticator prüft Sitzungen und API-Tokens (auth.Service).
type Authenticator interface {
	Authenticate(ctx context.Context, sessionToken string) (auth.Principal, error)
	AuthenticateToken(ctx context.Context, bearer string) (auth.Principal, error)
}

// Config enthält, was die API braucht; verdrahtet wird in cmd/monika.
type Config struct {
	Auth      Authenticator
	PublicURL *url.URL // Herkunft für die Prüfung des Origin-Headers
	Log       *slog.Logger
}

// Server implementiert die Endpunkte der Spezifikation.
type Server struct {
	unimplemented

	cfg      Config
	router   routers.Router                // Routen der Spezifikation: Operation, Anmeldung, Prüfung
	security openapi3.SecurityRequirements // Vorgabe der Spezifikation für Operationen ohne eigenes security
	origin   string
}

var _ oapi.StrictServerInterface = (*Server)(nil)

// New liefert den Server.
func New(cfg Config) (*Server, error) {
	spec, err := oapi.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("load openapi spec: %w", err)
	}
	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		return nil, fmt.Errorf("build openapi router: %w", err)
	}
	return &Server{
		cfg: cfg, router: router, security: spec.Security,
		origin: cfg.PublicURL.Scheme + "://" + cfg.PublicURL.Host,
	}, nil
}

// Handler liefert die API für Pfade unter Prefix.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	strict := oapi.NewStrictHandlerWithOptions(s, nil, oapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  s.requestError,
		ResponseErrorHandlerFunc: s.responseError,
	})
	oapi.HandlerWithOptions(strict, oapi.StdHTTPServerOptions{BaseRouter: mux, ErrorHandlerFunc: s.requestError})
	return s.guard(http.StripPrefix(Prefix, mux))
}

// requestError beantwortet Anfragen, die der erzeugte Code nicht lesen kann; die Prüfung gegen
// die Spezifikation fängt die meisten vorher ab.
func (s *Server) requestError(w http.ResponseWriter, _ *http.Request, err error) {
	writeProblem(w, problem(http.StatusBadRequest, "invalid_request", err.Error()))
}

// responseError übersetzt den Fehler eines Handlers; unbekannte Fehler werden geloggt.
func (s *Server) responseError(w http.ResponseWriter, r *http.Request, err error) {
	p := toProblem(err)
	if p.Status >= http.StatusInternalServerError && p.Status != http.StatusNotImplemented {
		s.cfg.Log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	}
	writeProblem(w, p)
}
