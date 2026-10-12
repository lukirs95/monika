// Package api ist die HTTP-API: Anmeldung prüfen, Rechte prüfen, Fachpaket aufrufen, Fehler
// übersetzen (docs/contracts/api.md). Typen, Router und Server-Interface erzeugt oapi-codegen in
// internal/api/oapi (make api-gen).
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action/executor"
	"github.com/lukirs95/monika/internal/api/oapi"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/forward"
	"github.com/lukirs95/monika/internal/preset"
	"github.com/lukirs95/monika/internal/secret"
	"github.com/lukirs95/monika/sdk"
)

// Prefix ist der Pfad, unter dem die API liegt (R3).
const Prefix = "/api/v1"

// Auth ist Anmeldung, Sitzungen und Rechte (auth.Service).
type Auth interface {
	Login(ctx context.Context, username, password string, meta auth.Meta) (auth.LoginStep, error)
	VerifyTOTP(ctx context.Context, challenge, code string, meta auth.Meta) (auth.IssuedSession, error)
	EnrollTOTP(ctx context.Context, challenge string) (auth.Enrollment, error)
	ConfirmTOTP(ctx context.Context, challenge, code string, meta auth.Meta) (auth.IssuedSession, []string, error)
	KioskLogin(ctx context.Context, bearer string, meta auth.Meta) (auth.IssuedSession, error)
	Authenticate(ctx context.Context, sessionToken string) (auth.Principal, error)
	AuthenticateToken(ctx context.Context, bearer string) (auth.Principal, error)
	Logout(ctx context.Context, sessionToken string) error
	ChangePassword(ctx context.Context, p auth.Principal, current, next string, meta auth.Meta) error
	ListSessions(ctx context.Context, user uuid.UUID) ([]auth.Session, error)
	RevokeSession(ctx context.Context, user, id uuid.UUID) error
	Grants(user uuid.UUID) []auth.Grant
	Allowed(v *core.View, p auth.Principal, perm auth.Permission, element uuid.UUID) bool
}

// Core liefert den aktuellen Stand und nimmt Befehle an (core.Core).
type Core interface {
	View() *core.View
	Do(ctx context.Context, cmd core.Command) error
}

// Executor führt Aktionen aus (executor.Executor).
type Executor interface {
	Targets(v *core.View, r executor.Request) ([]executor.Target, error)
	Submit(ctx context.Context, r executor.Request) (uuid.UUID, error)
	Cancel(ctx context.Context, ids []uuid.UUID) error
}

// Presets verwaltet und startet Presets (preset.Runner).
type Presets interface {
	List(ctx context.Context) ([]preset.Preset, error)
	Get(ctx context.Context, id uuid.UUID) (preset.Preset, error)
	CreatePreset(ctx context.Context, actor uuid.UUID, p preset.Preset) error
	UpdatePreset(ctx context.Context, actor uuid.UUID, p preset.Preset) error
	DeletePreset(ctx context.Context, actor, id uuid.UUID) error
	Start(ctx context.Context, p auth.Principal, presetID uuid.UUID) (uuid.UUID, error)
	Cancel(ctx context.Context, root uuid.UUID) error
}

// Forwarder verwaltet die Weiterleitungsziele (forward.Forwarder).
type Forwarder interface {
	ListTargets(ctx context.Context) ([]forward.Target, error)
	CreateTarget(ctx context.Context, actor uuid.UUID, t forward.Target) error
	UpdateTarget(ctx context.Context, actor uuid.UUID, t forward.Target) error
	DeleteTarget(ctx context.Context, actor uuid.UUID, id uuid.UUID) error
}

// Secrets legt Secrets an, listet und löscht sie (secret.Service); ersetzt wird über
// Core.Do(ReplaceSecret), weil Quellen neu starten.
type Secrets interface {
	List(ctx context.Context) ([]secret.Secret, error)
	Create(ctx context.Context, actor uuid.UUID, name, description string, value json.RawMessage) (secret.Secret, error)
	Delete(ctx context.Context, actor uuid.UUID, id uuid.UUID) error
}

// Config enthält, was die API braucht; verdrahtet wird in cmd/monika.
type Config struct {
	Auth           Auth
	Core           Core
	Executor       Executor
	Presets        Presets
	Forwarder      Forwarder
	Secrets        Secrets
	PublicURL      *url.URL            // Herkunft für die Prüfung des Origin-Headers; https = Cookie mit Secure
	TrustedProxies []netip.Prefix      // Proxys, deren X-Forwarded-For gilt
	Drivers        func() []sdk.Driver // registrierte Treiber (sdk.Drivers)
	Log            *slog.Logger
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
