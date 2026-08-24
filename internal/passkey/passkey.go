// Package passkey implements Deep Reader's WebAuthn relying party: it resolves
// the RP configuration from the environment, mints and validates the
// registration/login ceremonies, and keeps the short-lived challenge state.
//
// It is the only package that imports the WebAuthn library. Everything it hands
// back is either a domain type from internal/model or a small value type defined
// here, so the HTTP layer never has to understand WebAuthn structures and the
// library can be swapped without touching internal/api.
//
// Design notes:
//
//   - Logins are client-side discoverable ("usernameless"): the account is a
//     single built-in user, so asking for a username before the passkey prompt
//     would add a step that carries no information. Registration therefore
//     requires a resident key and user verification.
//
//   - Challenge state lives in memory rather than in SQLite. A ceremony is valid
//     for [ceremonyTTL] and is consumed on first use; losing the set on restart
//     costs the user one retry, and persisting it would add a table whose rows
//     are garbage within a minute.
//
//   - The login "begin" endpoint is unauthenticated, so the challenge cache is
//     capped at [maxLiveCeremonies] and evicts oldest-first. Without the cap a
//     remote caller could grow it without bound.
package passkey

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"deep-reader/internal/config"
	"deep-reader/internal/model"
	"deep-reader/internal/ports"
)

// Sentinel errors callers match with errors.Is.
var (
	// ErrDisabled is returned by New when the deployment has no usable relying
	// party: PASSKEY_ENABLED is off, or no RP ID could be resolved. Callers treat
	// it as "the feature is off", not as a startup failure.
	ErrDisabled = errors.New("passkey: disabled")

	// ErrCeremony is returned when the ceremony id of a "finish" call is unknown,
	// expired, already consumed, or belongs to the other ceremony kind. The HTTP
	// layer maps it to 400 — the client's remedy is to start over.
	ErrCeremony = errors.New("passkey: unknown or expired ceremony")
)

const (
	// ceremonyTTL bounds how long a challenge stays valid. It has to cover a
	// user reaching for a phone or a hardware key, and nothing more.
	ceremonyTTL = 5 * time.Minute

	// maxLiveCeremonies caps the in-memory challenge cache. Registration is
	// authenticated, but login is not, so this bounds what an anonymous caller
	// can allocate. A single user never has more than one ceremony in flight.
	maxLiveCeremonies = 64

	// defaultRPName is the human-readable relying-party name shown in the
	// platform's passkey prompt when PASSKEY_RP_NAME is unset.
	defaultRPName = "Deep Reader"
)

// ceremonyKind distinguishes the two flows so a challenge minted for one can
// never be completed through the other — finishing a registration challenge on
// the unauthenticated login endpoint would be a privilege escalation.
type ceremonyKind string

const (
	ceremonyRegister ceremonyKind = "register"
	ceremonyLogin    ceremonyKind = "login"
)

// ceremony is one in-flight challenge.
type ceremony struct {
	kind    ceremonyKind
	session webauthn.SessionData
	expires time.Time
}

// Service is the relying party. Construct it with New; a nil *Service means
// passkeys are disabled and every endpoint that needs one answers accordingly.
type Service struct {
	wa      *webauthn.WebAuthn
	rpID    string
	origins []string

	mu         sync.Mutex
	ceremonies map[string]*ceremony
}

// Registration is the outcome of a completed registration ceremony: the raw
// credential ID plus the JSON-marshalled credential to persist verbatim.
type Registration struct {
	CredentialID []byte
	Credential   []byte
}

// Assertion is the outcome of a completed login ceremony. Credential is the
// credential *after* validation — the library bumps its signature counter and
// backup flags — so the caller must write it back.
type Assertion struct {
	CredentialID []byte
	Credential   []byte
}

// CredentialLookup resolves the account that owns an asserted credential. It is
// called during a discoverable login with the raw credential ID and the user
// handle the authenticator returned; returning an error fails the ceremony.
type CredentialLookup func(credentialID, userHandle []byte) (*User, error)

// New builds the relying party from the environment. It returns ErrDisabled
// when passkeys are switched off or no RP ID can be resolved, which the caller
// treats as "feature unavailable" rather than a fatal error.
func New(cfg *config.Config) (*Service, error) {
	if !cfg.PasskeyEnabled {
		return nil, ErrDisabled
	}

	rpID := resolveRPID(cfg)
	if rpID == "" {
		return nil, ErrDisabled
	}

	origins, err := resolveOrigins(cfg, rpID)
	if err != nil {
		return nil, err
	}

	rpName := cfg.PasskeyRPName
	if rpName == "" {
		rpName = defaultRPName
	}

	wa, err := webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: rpName,
		RPOrigins:     origins,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			// Discoverable login is the whole point: without a resident key the
			// authenticator cannot offer the account without a username first.
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		},
		AttestationPreference: protocol.PreferNoAttestation,
	})
	if err != nil {
		return nil, fmt.Errorf("passkey: build relying party: %w", err)
	}

	return &Service{
		wa:         wa,
		rpID:       rpID,
		origins:    origins,
		ceremonies: make(map[string]*ceremony),
	}, nil
}

// RPID returns the resolved relying-party ID.
func (s *Service) RPID() string { return s.rpID }

// Origins returns the resolved list of accepted origins, native facets included.
func (s *Service) Origins() []string { return slices.Clone(s.origins) }

// ── Registration ────────────────────────────────────────────────────────────

// BeginRegistration mints a creation challenge for the given account. The
// account's existing credentials are sent as an exclusion list so the platform
// tells the user "already registered" instead of silently creating a duplicate.
func (s *Service) BeginRegistration(user *User) (model.PasskeyChallenge, error) {
	creation, session, err := s.wa.BeginRegistration(user,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(webauthn.Credentials(user.WebAuthnCredentials()).CredentialDescriptors()),
	)
	if err != nil {
		return model.PasskeyChallenge{}, fmt.Errorf("passkey: begin registration: %w", err)
	}

	id, err := s.storeCeremony(ceremonyRegister, *session)
	if err != nil {
		return model.PasskeyChallenge{}, err
	}
	return model.PasskeyChallenge{CeremonyID: id, Options: creation}, nil
}

// FinishRegistration validates the authenticator's response against the stored
// challenge and returns the credential to persist.
func (s *Service) FinishRegistration(user *User, ceremonyID string, body []byte) (Registration, error) {
	session, ok := s.takeCeremony(ceremonyRegister, ceremonyID)
	if !ok {
		return Registration{}, ErrCeremony
	}

	parsed, err := protocol.ParseCredentialCreationResponseBytes(body)
	if err != nil {
		return Registration{}, fmt.Errorf("passkey: parse registration response: %w", err)
	}
	credential, err := s.wa.CreateCredential(user, session, parsed)
	if err != nil {
		return Registration{}, fmt.Errorf("passkey: create credential: %w", err)
	}

	encoded, err := json.Marshal(credential)
	if err != nil {
		return Registration{}, fmt.Errorf("passkey: encode credential: %w", err)
	}
	return Registration{CredentialID: credential.ID, Credential: encoded}, nil
}

// ── Login ───────────────────────────────────────────────────────────────────

// BeginLogin mints a discoverable-login challenge. No user is named: the
// authenticator picks the resident credential itself.
func (s *Service) BeginLogin() (model.PasskeyChallenge, error) {
	assertion, session, err := s.wa.BeginDiscoverableLogin(
		webauthn.WithUserVerification(protocol.VerificationRequired),
	)
	if err != nil {
		return model.PasskeyChallenge{}, fmt.Errorf("passkey: begin login: %w", err)
	}

	id, err := s.storeCeremony(ceremonyLogin, *session)
	if err != nil {
		return model.PasskeyChallenge{}, err
	}
	return model.PasskeyChallenge{CeremonyID: id, Options: assertion}, nil
}

// FinishLogin validates an assertion against the stored challenge, resolving the
// account through lookup. The returned credential carries the updated signature
// counter and must be written back, or clone detection stays frozen at the
// value recorded during registration.
func (s *Service) FinishLogin(ceremonyID string, body []byte, lookup CredentialLookup) (Assertion, error) {
	session, ok := s.takeCeremony(ceremonyLogin, ceremonyID)
	if !ok {
		return Assertion{}, ErrCeremony
	}

	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		return Assertion{}, fmt.Errorf("passkey: parse assertion: %w", err)
	}

	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		return lookup(rawID, userHandle)
	}
	credential, err := s.wa.ValidateDiscoverableLogin(handler, session, parsed)
	if err != nil {
		return Assertion{}, fmt.Errorf("passkey: validate assertion: %w", err)
	}

	encoded, err := json.Marshal(credential)
	if err != nil {
		return Assertion{}, fmt.Errorf("passkey: encode credential: %w", err)
	}
	return Assertion{CredentialID: credential.ID, Credential: encoded}, nil
}

// ── Ceremony cache ──────────────────────────────────────────────────────────

// storeCeremony saves a challenge and returns its opaque handle. Expired entries
// are swept on the way in, and the cache is trimmed to maxLiveCeremonies.
func (s *Service) storeCeremony(kind ceremonyKind, session webauthn.SessionData) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("passkey: generate ceremony id: %w", err)
	}
	id := base64.RawURLEncoding.EncodeToString(raw[:])

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for key, c := range s.ceremonies {
		if now.After(c.expires) {
			delete(s.ceremonies, key)
		}
	}
	// Still over the cap after the sweep: drop the entries closest to expiry,
	// which are the oldest ones.
	for len(s.ceremonies) >= maxLiveCeremonies {
		oldest, oldestExpiry := "", time.Time{}
		for key, c := range s.ceremonies {
			if oldest == "" || c.expires.Before(oldestExpiry) {
				oldest, oldestExpiry = key, c.expires
			}
		}
		delete(s.ceremonies, oldest)
	}

	s.ceremonies[id] = &ceremony{kind: kind, session: session, expires: now.Add(ceremonyTTL)}
	return id, nil
}

// takeCeremony consumes the challenge with the given id and kind. It reports
// false when the id is unknown, expired, already used, or of the other kind —
// and consumes nothing in that case, so a mismatched kind cannot be used to
// burn a legitimate ceremony.
func (s *Service) takeCeremony(kind ceremonyKind, id string) (webauthn.SessionData, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.ceremonies[id]
	if !ok || c.kind != kind {
		return webauthn.SessionData{}, false
	}
	delete(s.ceremonies, id)
	if time.Now().After(c.expires) {
		return webauthn.SessionData{}, false
	}
	return c.session, true
}

// ── Configuration resolution ────────────────────────────────────────────────

// resolveRPID returns the configured RP ID, falling back to the host of
// PUBLIC_BASE_URL. The RP ID is a bare registrable domain: a scheme or a port
// makes the browser reject the ceremony, so both are stripped.
func resolveRPID(cfg *config.Config) string {
	if cfg.PasskeyRPID != "" {
		return hostOnly(cfg.PasskeyRPID)
	}
	if cfg.PublicBaseURL == "" {
		return ""
	}
	u, err := url.Parse(cfg.PublicBaseURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// hostOnly strips a scheme and port from a value that should be a bare host, so
// PASSKEY_RP_ID=https://reader.example:8443 still yields "reader.example".
func hostOnly(v string) string {
	v = strings.TrimSpace(v)
	if u, err := url.Parse(v); err == nil && u.Host != "" {
		return u.Hostname()
	}
	if host, _, found := strings.Cut(v, ":"); found {
		return host
	}
	return v
}

// resolveOrigins builds the accepted-origin allowlist.
//
// An explicit PASSKEY_RP_ORIGINS is taken as-is — widening it with a guess would
// silently accept an origin the operator did not list. Only when it is empty do
// we derive https://<rp-id> plus the PUBLIC_BASE_URL origin. The Android facets
// are always appended: they are derived from the same fingerprints that go into
// assetlinks.json, and a native login is rejected without them.
func resolveOrigins(cfg *config.Config, rpID string) ([]string, error) {
	origins := slices.Clone(cfg.PasskeyRPOrigins)

	if len(origins) == 0 {
		origins = append(origins, "https://"+rpID)
		if cfg.PublicBaseURL != "" {
			if u, err := url.Parse(cfg.PublicBaseURL); err == nil && u.Host != "" {
				origins = appendUnique(origins, u.Scheme+"://"+u.Host)
			}
		}
	}

	android, err := AndroidOrigins(cfg.PasskeyAndroidFingerprints)
	if err != nil {
		return nil, err
	}
	for _, o := range android {
		origins = appendUnique(origins, o)
	}
	return origins, nil
}

// appendUnique appends v unless it is already present.
func appendUnique(list []string, v string) []string {
	if slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

// AndroidOrigins converts SHA-256 signing-certificate fingerprints — the same
// values that go into assetlinks.json, with or without colon separators — into
// the "facet" origins Android's Credential Manager reports for a native caller.
//
// A native Android app is not an https origin: clientDataJSON carries
// "android:apk-key-hash:<base64(sha256(signing cert))>". Both the URL-safe and
// the standard base64 alphabet are emitted because implementations differ on
// which one they send, and an unlisted origin fails validation outright.
func AndroidOrigins(fingerprints []string) ([]string, error) {
	if len(fingerprints) == 0 {
		return nil, nil
	}

	out := make([]string, 0, len(fingerprints)*2)
	for _, fp := range fingerprints {
		normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(fp), ":", ""))
		raw, err := hex.DecodeString(normalized)
		if err != nil {
			return nil, fmt.Errorf("passkey: invalid Android certificate fingerprint %q: %w", fp, err)
		}
		if len(raw) != 32 {
			return nil, fmt.Errorf("passkey: Android certificate fingerprint %q is %d bytes, want a 32-byte SHA-256", fp, len(raw))
		}
		out = appendUnique(out, "android:apk-key-hash:"+base64.RawURLEncoding.EncodeToString(raw))
		out = appendUnique(out, "android:apk-key-hash:"+base64.RawStdEncoding.EncodeToString(raw))
	}
	return out, nil
}

// ── webauthn.User adapter ───────────────────────────────────────────────────

// User adapts the single built-in account to the WebAuthn library's User
// interface. Build it with NewUser.
type User struct {
	handle      []byte
	name        string
	credentials []webauthn.Credential
}

// NewUser builds the WebAuthn view of the account: its stable user handle, its
// display name, and every passkey registered to it (needed both as the
// registration exclusion list and as the login credential set).
func NewUser(u *model.User, passkeys []ports.Passkey) (*User, error) {
	credentials := make([]webauthn.Credential, 0, len(passkeys))
	for _, p := range passkeys {
		var c webauthn.Credential
		if err := json.Unmarshal(p.Credential, &c); err != nil {
			return nil, fmt.Errorf("passkey: decode stored credential %s: %w", p.ID, err)
		}
		credentials = append(credentials, c)
	}
	return &User{handle: u.WebAuthnUserHandle, name: u.Username, credentials: credentials}, nil
}

// WebAuthnID returns the account's stable user handle.
func (u *User) WebAuthnID() []byte { return u.handle }

// WebAuthnName returns the account's username.
func (u *User) WebAuthnName() string { return u.name }

// WebAuthnDisplayName returns the name shown in the platform's passkey prompt.
func (u *User) WebAuthnDisplayName() string { return u.name }

// WebAuthnCredentials returns every passkey registered to the account.
func (u *User) WebAuthnCredentials() []webauthn.Credential { return u.credentials }
