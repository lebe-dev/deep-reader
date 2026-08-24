package api

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"deep-reader/internal/model"
	"deep-reader/internal/passkey"
	"deep-reader/internal/ports"
)

// defaultPasskeyName labels a passkey the client registered without naming it.
const defaultPasskeyName = "Passkey"

// listPasskeys handles GET /api/passkeys — the registered passkeys, newest
// first. The credential material never leaves the server; the client only needs
// enough to list, rename and revoke.
func (s *Server) listPasskeys(c fiber.Ctx) error {
	if s.passkey == nil {
		return sendPasskeysUnavailable(c)
	}
	stored, err := s.store.ListPasskeys(c.Context())
	if err != nil {
		return s.serverError(c, "list passkeys", err)
	}
	return c.JSON(passkeyViews(stored))
}

// beginPasskeyRegistration handles POST /api/passkeys/register/begin. It runs
// behind requireAuth: a passkey is added to an account the caller already
// controls, never as a way of claiming one.
func (s *Server) beginPasskeyRegistration(c fiber.Ctx) error {
	if s.passkey == nil {
		return sendPasskeysUnavailable(c)
	}
	user, err := s.webAuthnUser(c.Context())
	if err != nil {
		return s.serverError(c, "build webauthn user", err)
	}
	challenge, err := s.passkey.BeginRegistration(user)
	if err != nil {
		return s.serverError(c, "begin passkey registration", err)
	}
	return c.JSON(challenge)
}

// finishPasskeyRegistration handles POST /api/passkeys/register/finish: it
// validates the authenticator's response against the stored challenge and
// persists the credential.
func (s *Server) finishPasskeyRegistration(c fiber.Ctx) error {
	if s.passkey == nil {
		return sendPasskeysUnavailable(c)
	}
	var req model.PasskeyFinishRequest
	if err := c.Bind().Body(&req); err != nil {
		return sendError(c, fiber.StatusBadRequest, "invalid JSON body")
	}
	if req.CeremonyID == "" || len(req.Credential) == 0 {
		return sendError(c, fiber.StatusBadRequest, "ceremony_id and credential are required")
	}
	name, msg, ok := validatePasskeyName(req.Name, defaultPasskeyName)
	if !ok {
		return sendError(c, fiber.StatusBadRequest, msg)
	}

	user, err := s.webAuthnUser(c.Context())
	if err != nil {
		return s.serverError(c, "build webauthn user", err)
	}

	registration, err := s.passkey.FinishRegistration(user, req.CeremonyID, req.Credential)
	if err != nil {
		// Everything here is the client's ceremony going wrong — an expired
		// challenge, a mismatched origin, an authenticator the RP rejected. None
		// of it is a server fault, so it stays a 400 and out of Sentry.
		s.log.Warn("passkey registration rejected",
			slog.String("request_id", requestIDOf(c)),
			slog.Any("error", err),
		)
		return sendError(c, fiber.StatusBadRequest, passkeyCeremonyMessage(err))
	}

	created, err := s.store.CreatePasskey(c.Context(), ports.Passkey{
		Name:         name,
		CredentialID: registration.CredentialID,
		Credential:   registration.Credential,
	})
	if err != nil {
		if errors.Is(err, ports.ErrDuplicate) {
			return sendError(c, fiber.StatusConflict, "this passkey is already registered")
		}
		return s.serverError(c, "create passkey", err)
	}
	s.log.Info("passkey registered", slog.String("passkey_id", created.ID), slog.String("name", created.Name))
	return c.Status(fiber.StatusCreated).JSON(passkeyView(created))
}

// renamePasskey handles PATCH /api/passkeys/:id.
func (s *Server) renamePasskey(c fiber.Ctx) error {
	if s.passkey == nil {
		return sendPasskeysUnavailable(c)
	}
	var req model.PasskeyRenameRequest
	if err := c.Bind().Body(&req); err != nil {
		return sendError(c, fiber.StatusBadRequest, "invalid JSON body")
	}
	// A rename is an explicit edit: an empty value is a mistake, not a request
	// for the default label.
	name, msg, ok := validatePasskeyName(req.Name, "")
	if !ok {
		return sendError(c, fiber.StatusBadRequest, msg)
	}

	if err := s.store.RenamePasskey(c.Context(), c.Params("id"), name); err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return sendError(c, fiber.StatusNotFound, "passkey not found")
		}
		return s.serverError(c, "rename passkey", err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// deletePasskey handles DELETE /api/passkeys/:id. The account always keeps its
// password, so revoking the last passkey cannot lock the user out.
func (s *Server) deletePasskey(c fiber.Ctx) error {
	if s.passkey == nil {
		return sendPasskeysUnavailable(c)
	}
	id := c.Params("id")
	if err := s.store.DeletePasskey(c.Context(), id); err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return sendError(c, fiber.StatusNotFound, "passkey not found")
		}
		return s.serverError(c, "delete passkey", err)
	}
	s.log.Info("passkey revoked", slog.String("passkey_id", id))
	return c.SendStatus(fiber.StatusNoContent)
}

// beginPasskeyLogin handles POST /api/passkeys/login/begin — unauthenticated by
// necessity, since it runs before any session exists. No username is asked for:
// the challenge is discoverable, and the authenticator picks the resident
// credential itself.
func (s *Server) beginPasskeyLogin(c fiber.Ctx) error {
	if s.passkey == nil {
		return sendPasskeysUnavailable(c)
	}
	challenge, err := s.passkey.BeginLogin()
	if err != nil {
		return s.serverError(c, "begin passkey login", err)
	}
	return c.JSON(challenge)
}

// finishPasskeyLogin handles POST /api/passkeys/login/finish: it validates the
// assertion and, on success, issues a session token exactly like a password
// login would.
func (s *Server) finishPasskeyLogin(c fiber.Ctx) error {
	if s.passkey == nil {
		return sendPasskeysUnavailable(c)
	}
	var req model.PasskeyFinishRequest
	if err := c.Bind().Body(&req); err != nil {
		return sendError(c, fiber.StatusBadRequest, "invalid JSON body")
	}
	if req.CeremonyID == "" || len(req.Credential) == 0 {
		return sendError(c, fiber.StatusBadRequest, "ceremony_id and credential are required")
	}

	ctx := c.Context()
	assertion, err := s.passkey.FinishLogin(req.CeremonyID, req.Credential, s.lookupPasskeyUser(ctx))
	if err != nil {
		// A rejected assertion is not a guessable credential failure, so it does
		// not feed the per-IP password lockout — that would let anyone lock the
		// account out of its password by spamming bad assertions. The endpoint's
		// own rate limiter is what bounds the cost.
		s.log.Warn("passkey login rejected",
			slog.String("ip", c.IP()),
			slog.String("request_id", requestIDOf(c)),
			slog.Any("error", err),
		)
		return sendError(c, fiber.StatusUnauthorized, "passkey authentication failed")
	}

	// Persist the credential state the library just updated (signature counter,
	// backup flags) and stamp "last used". A failure here must not cost the user
	// a login they legitimately completed, so it is logged, not returned.
	if stored, getErr := s.store.GetPasskeyByCredentialID(ctx, assertion.CredentialID); getErr != nil {
		s.log.Error("passkey lookup after login failed", slog.Any("error", getErr))
	} else if touchErr := s.store.TouchPasskey(ctx, stored.ID, assertion.Credential, time.Now().UTC()); touchErr != nil {
		s.log.Error("passkey touch failed", slog.String("passkey_id", stored.ID), slog.Any("error", touchErr))
	}

	user, err := s.store.GetUser(ctx)
	if err != nil {
		return s.serverError(c, "get user", err)
	}
	resp, err := s.issueSession(ctx, user.Username)
	if err != nil {
		return s.serverError(c, "issue session", err)
	}
	// A successful passkey login proves control of the account, so it clears any
	// password lockout the same IP had accumulated.
	s.loginGuard.recordSuccess(c.IP())
	return c.JSON(resp)
}

// lookupPasskeyUser builds the discoverable-login resolver: given the asserted
// credential and the user handle the authenticator returned, it produces the
// account view the WebAuthn library validates against.
func (s *Server) lookupPasskeyUser(ctx context.Context) passkey.CredentialLookup {
	return func(credentialID, userHandle []byte) (*passkey.User, error) {
		if _, err := s.store.GetPasskeyByCredentialID(ctx, credentialID); err != nil {
			return nil, err
		}
		user, err := s.store.GetUser(ctx)
		if err != nil {
			return nil, err
		}
		// The handle binds the credential to this account. A mismatch means the
		// authenticator offered a credential minted for a different install (for
		// example after the database was recreated), and it must not authenticate.
		if !bytes.Equal(user.WebAuthnUserHandle, userHandle) {
			return nil, errors.New("api: passkey user handle mismatch")
		}
		all, err := s.store.ListPasskeys(ctx)
		if err != nil {
			return nil, err
		}
		return passkey.NewUser(user, all)
	}
}

// webAuthnUser loads the account plus its passkeys as the library's user view.
func (s *Server) webAuthnUser(ctx context.Context) (*passkey.User, error) {
	user, err := s.store.GetUser(ctx)
	if err != nil {
		return nil, err
	}
	stored, err := s.store.ListPasskeys(ctx)
	if err != nil {
		return nil, err
	}
	return passkey.NewUser(user, stored)
}

// sendPasskeysUnavailable is the single answer for every passkey route when the
// deployment has no relying party configured. 501 rather than 404 keeps it
// distinguishable from a wrong path, and GET /api/config already tells the
// client not to offer the flow at all.
func sendPasskeysUnavailable(c fiber.Ctx) error {
	return sendError(c, fiber.StatusNotImplemented, "passkeys are not configured on this server")
}

// passkeyCeremonyMessage picks the client-facing message for a failed ceremony,
// separating "start over" from "your authenticator was rejected".
func passkeyCeremonyMessage(err error) string {
	if errors.Is(err, passkey.ErrCeremony) {
		return "the passkey challenge expired; please try again"
	}
	return "the passkey could not be verified"
}

// validatePasskeyName trims and bounds a user-supplied label, substituting def
// when the input is empty (an empty def means the value is required).
func validatePasskeyName(name, def string) (string, string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		if def == "" {
			return "", "name is required", false
		}
		return def, "", true
	}
	if len([]rune(name)) > model.MaxPasskeyNameLen {
		return "", "name is too long", false
	}
	return name, "", true
}

// passkeyViews maps stored passkeys to their client-facing shape. It always
// returns a non-nil slice: a nil Go slice serializes as JSON null and the typed
// client throws while rendering.
func passkeyViews(stored []ports.Passkey) []model.PasskeyView {
	out := make([]model.PasskeyView, 0, len(stored))
	for _, p := range stored {
		out = append(out, passkeyView(p))
	}
	return out
}

// passkeyView maps one stored passkey to its client-facing shape.
func passkeyView(p ports.Passkey) model.PasskeyView {
	return model.PasskeyView{
		ID:         p.ID,
		Name:       p.Name,
		CreatedAt:  p.CreatedAt,
		LastUsedAt: p.LastUsedAt,
	}
}
