package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/lukirs95/monika/internal/api/oapi"
	"github.com/lukirs95/monika/internal/auth"
)

// Anmeldung, eigene Sitzungen und eigenes Passwort (docs/contracts/auth.md).

func (s *Server) Login(ctx context.Context, rq oapi.LoginRequestObject) (oapi.LoginResponseObject, error) {
	step, err := s.cfg.Auth.Login(ctx, rq.Body.Username, rq.Body.Password, requestOf(ctx).meta)
	if err != nil {
		return nil, err
	}
	return oapi.Login200JSONResponse{Challenge: step.Challenge, NeedsEnrollment: step.NeedsEnrollment}, nil
}

func (s *Server) LoginTOTP(ctx context.Context, rq oapi.LoginTOTPRequestObject) (oapi.LoginTOTPResponseObject, error) {
	issued, err := s.cfg.Auth.VerifyTOTP(ctx, rq.Body.Challenge, rq.Body.Code, requestOf(ctx).meta)
	if err != nil {
		return nil, err
	}
	return oapi.LoginTOTP204Response{Headers: oapi.SessionCreatedResponseHeaders{SetCookie: s.sessionCookie(issued)}}, nil
}

func (s *Server) EnrollTOTP(ctx context.Context, rq oapi.EnrollTOTPRequestObject) (oapi.EnrollTOTPResponseObject, error) {
	e, err := s.cfg.Auth.EnrollTOTP(ctx, rq.Body.Challenge)
	if err != nil {
		return nil, err
	}
	return oapi.EnrollTOTP200JSONResponse{OtpauthURL: e.OTPAuthURL, Secret: e.Secret}, nil
}

func (s *Server) ConfirmTOTP(ctx context.Context, rq oapi.ConfirmTOTPRequestObject) (oapi.ConfirmTOTPResponseObject, error) {
	issued, codes, err := s.cfg.Auth.ConfirmTOTP(ctx, rq.Body.Challenge, rq.Body.Code, requestOf(ctx).meta)
	if err != nil {
		return nil, err
	}
	return oapi.ConfirmTOTP200JSONResponse{
		Body:    oapi.RecoveryCodes{RecoveryCodes: codes},
		Headers: oapi.ConfirmTOTP200ResponseHeaders{SetCookie: s.sessionCookie(issued)},
	}, nil
}

func (s *Server) KioskLogin(ctx context.Context, rq oapi.KioskLoginRequestObject) (oapi.KioskLoginResponseObject, error) {
	issued, err := s.cfg.Auth.KioskLogin(ctx, rq.Body.Token, requestOf(ctx).meta)
	if err != nil {
		return nil, err
	}
	return oapi.KioskLogin204Response{Headers: oapi.SessionCreatedResponseHeaders{SetCookie: s.sessionCookie(issued)}}, nil
}

func (s *Server) Logout(ctx context.Context, _ oapi.LogoutRequestObject) (oapi.LogoutResponseObject, error) {
	if err := s.cfg.Auth.Logout(ctx, requestOf(ctx).session); err != nil {
		return nil, err
	}
	gone := s.cookie("", time.Unix(0, 0))
	gone.MaxAge = -1
	return oapi.Logout204Response{Headers: oapi.Logout204ResponseHeaders{SetCookie: new(gone.String())}}, nil
}

func (s *Server) GetMe(ctx context.Context, _ oapi.GetMeRequestObject) (oapi.GetMeResponseObject, error) {
	p := principal(ctx)
	grants := []oapi.Grant{}
	for _, g := range s.cfg.Auth.Grants(p.UserID) {
		grants = append(grants, oapi.Grant{Permission: oapi.GrantPermission(g.Permission), ScopeElementID: optionalID(g.ScopeElementID)})
	}
	return oapi.GetMe200JSONResponse{
		UserID: p.UserID, Username: p.Username, DisplayName: optional(p.DisplayName),
		Service: p.Service, ReadOnly: p.ReadOnly, Grants: grants,
	}, nil
}

func (s *Server) ChangeOwnPassword(ctx context.Context, rq oapi.ChangeOwnPasswordRequestObject) (oapi.ChangeOwnPasswordResponseObject, error) {
	r := requestOf(ctx)
	err := s.cfg.Auth.ChangePassword(ctx, r.principal, rq.Body.CurrentPassword, rq.Body.NewPassword, r.meta)
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		// 422 statt 401: Die Sitzung gilt weiter, nur das aktuelle Passwort stimmt nicht.
		return nil, problem(http.StatusUnprocessableEntity, "wrong_password", "current password is wrong")
	case err != nil:
		return nil, err
	}
	return oapi.ChangeOwnPassword204Response{}, nil
}

func (s *Server) ListOwnSessions(ctx context.Context, _ oapi.ListOwnSessionsRequestObject) (oapi.ListOwnSessionsResponseObject, error) {
	p := principal(ctx)
	sessions, err := s.cfg.Auth.ListSessions(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	out := oapi.ListOwnSessions200JSONResponse{}
	for _, sess := range sessions {
		out = append(out, oapi.SessionInfo{
			ID: sess.ID, Kind: oapi.SessionInfoKind(sess.Kind), Current: sess.ID == p.SessionID,
			CreatedAt: sess.CreatedAt.UTC(), LastSeenAt: sess.LastSeenAt.UTC(), ExpiresAt: sess.ExpiresAt.UTC(),
			UserAgent: optional(sess.UserAgent), RemoteAddr: optional(sess.RemoteAddr),
		})
	}
	return out, nil
}

func (s *Server) RevokeOwnSession(ctx context.Context, rq oapi.RevokeOwnSessionRequestObject) (oapi.RevokeOwnSessionResponseObject, error) {
	if err := s.cfg.Auth.RevokeSession(ctx, principal(ctx).UserID, rq.SessionID); err != nil {
		return nil, err
	}
	return oapi.RevokeOwnSession204Response{}, nil
}

// GetHealthz antwortet ohne Anmeldung nach View.DB (api.md); ohne View läuft der Core noch nicht.
func (s *Server) GetHealthz(context.Context, oapi.GetHealthzRequestObject) (oapi.GetHealthzResponseObject, error) {
	if v := s.cfg.Core.View(); v == nil || !v.DB.OK {
		return oapi.GetHealthz503Response{}, nil
	}
	return oapi.GetHealthz200Response{}, nil
}

// sessionCookie ist das Cookie einer neuen Sitzung: HttpOnly, SameSite=Strict, Secure bei https.
func (s *Server) sessionCookie(issued auth.IssuedSession) *string {
	c := s.cookie(issued.Token, issued.ExpiresAt)
	return new(c.String())
}

func (s *Server) cookie(value string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name: SessionCookie, Value: value, Path: "/", Expires: expires,
		HttpOnly: true, Secure: s.cfg.PublicURL.Scheme == "https", SameSite: http.SameSiteStrictMode,
	}
}
