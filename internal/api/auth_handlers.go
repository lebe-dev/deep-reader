package api

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"deep-reader/internal/auth"
	"deep-reader/internal/model"
	"deep-reader/internal/ports"
)

// Credential bounds. The password upper bound is 72 *bytes* because bcrypt
// rejects longer inputs; enforcing it here turns a hashing error into a clear
// 400. The minimum is a pragmatic floor ("без паранойи").
const (
	minPasswordBytes = 8
	maxPasswordBytes = 72
	maxUsernameLen   = 64
)

// setup handles POST /api/setup — first-run creation of the single built-in
// account. It is open while the service is uninitialized and returns 409 once an
// account exists. On success it logs the new user in immediately by issuing a
// session token.
func (s *Server) setup(c fiber.Ctx) error {
	var req model.SetupRequest
	if err := c.Bind().Body(&req); err != nil {
		return sendError(c, fiber.StatusBadRequest, "invalid JSON body")
	}
	username := strings.TrimSpace(req.Username)
	if msg, ok := validateCredentials(username, req.Password); !ok {
		return sendError(c, fiber.StatusBadRequest, msg)
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return s.serverError(c, "hash password", err)
	}

	if err := s.store.CreateUser(c.Context(), username, hash); err != nil {
		if errors.Is(err, ports.ErrAlreadyInitialized) {
			return sendError(c, fiber.StatusConflict, "service is already initialized")
		}
		return s.serverError(c, "create user", err)
	}

	resp, err := s.issueSession(c.Context(), username)
	if err != nil {
		return s.serverError(c, "issue session", err)
	}
	s.log.Info("service initialized", slog.String("username", username))
	return c.Status(fiber.StatusCreated).JSON(resp)
}

// login handles POST /api/login — verifies credentials and issues a session
// token. To avoid leaking which part was wrong, every failure (unknown user or
// bad password) returns the same 401.
func (s *Server) login(c fiber.Ctx) error {
	// Brute-force guard: reject locked-out clients before touching the store or
	// running the (deliberately slow) bcrypt comparison.
	ip := c.IP()
	if locked, wait := s.loginGuard.retryAfter(ip); locked {
		c.Set(fiber.HeaderRetryAfter, strconv.Itoa(int(wait.Seconds())))
		s.log.Warn("login locked out", slog.String("ip", ip), slog.Duration("retry_after", wait))
		return sendError(c, fiber.StatusTooManyRequests, "too many login attempts; try again later")
	}

	var req model.LoginRequest
	if err := c.Bind().Body(&req); err != nil {
		return sendError(c, fiber.StatusBadRequest, "invalid JSON body")
	}

	user, err := s.store.GetUser(c.Context())
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			// Not initialized yet — the client should be on /setup. Still count
			// the attempt against the per-IP guard so a script hammering /login
			// pre-setup is throttled the same as a post-setup brute-force run,
			// and the uniform 401 keeps the pre/post-setup state from leaking.
			s.registerLoginFailure(ip)
			return sendError(c, fiber.StatusUnauthorized, "invalid username or password")
		}
		return s.serverError(c, "get user", err)
	}

	if strings.TrimSpace(req.Username) != user.Username || !auth.VerifyPassword(user.PasswordHash, req.Password) {
		s.registerLoginFailure(ip)
		return sendError(c, fiber.StatusUnauthorized, "invalid username or password")
	}

	s.loginGuard.recordSuccess(ip)
	resp, err := s.issueSession(c.Context(), user.Username)
	if err != nil {
		return s.serverError(c, "issue session", err)
	}
	return c.JSON(resp)
}

// registerLoginFailure records a failed login for the client IP and logs when
// the failure trips the lockout, so operators can see brute-force activity.
func (s *Server) registerLoginFailure(ip string) {
	if s.loginGuard.recordFailure(ip) {
		s.log.Warn("login lockout engaged",
			slog.String("ip", ip),
			slog.Int("max_attempts", s.cfg.LoginMaxAttempts),
			slog.Duration("lockout", s.cfg.LoginLockoutDuration),
		)
	}
}

// logout handles POST /api/logout — removes the current session. It is mounted
// behind requireAuth, so a valid bearer token is guaranteed present.
func (s *Server) logout(c fiber.Ctx) error {
	token, ok := bearerToken(c)
	if !ok {
		// Defensive: requireAuth already enforced this.
		return c.SendStatus(fiber.StatusNoContent)
	}
	if err := s.store.DeleteSession(c.Context(), auth.HashToken(token)); err != nil {
		return s.serverError(c, "delete session", err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// issueSession mints a fresh session token, persists its hash, and returns the
// client-facing AuthResponse carrying the plaintext token.
func (s *Server) issueSession(ctx context.Context, username string) (model.AuthResponse, error) {
	token, err := auth.NewSessionToken()
	if err != nil {
		return model.AuthResponse{}, err
	}
	if err := s.store.CreateSession(ctx, auth.HashToken(token), time.Now().UTC()); err != nil {
		return model.AuthResponse{}, err
	}
	return model.AuthResponse{Token: token, Username: username}, nil
}

// validateCredentials enforces the username/password bounds shared by setup.
// It returns a human-readable message and ok=false on the first violation.
func validateCredentials(username, password string) (string, bool) {
	if username == "" {
		return "username is required", false
	}
	if len(username) > maxUsernameLen {
		return "username is too long", false
	}
	if len(password) < minPasswordBytes {
		return "password must be at least 8 characters", false
	}
	if len(password) > maxPasswordBytes {
		return "password must be at most 72 bytes", false
	}
	return "", true
}
