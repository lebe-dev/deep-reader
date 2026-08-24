package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"deep-reader/internal/config"
	"deep-reader/internal/model"
	"deep-reader/internal/passkey"
	"deep-reader/internal/ports"
)

// testRPID is the relying-party id the passkey-enabled test servers run under.
const testRPID = "reader.example"

// newPasskeyServer builds a test server with a working WebAuthn relying party.
func newPasskeyServer(t *testing.T, st ports.Store) *Server {
	t.Helper()
	svc, err := passkey.New(&config.Config{PasskeyEnabled: true, PasskeyRPID: testRPID})
	if err != nil {
		t.Fatalf("passkey.New: %v", err)
	}
	return newTestServerCfg(t, st, nil, nil, WithPasskeys(svc))
}

// doJSON issues a request with a raw JSON body (so a test can send a shape the
// Go types cannot express) and an optional bearer token.
func doJSON(t *testing.T, srv *Server, method, path, token, body string) *http.Response {
	t.Helper()
	var payload any
	if body != "" {
		payload = json.RawMessage(body)
	}
	return doReq(t, srv, method, path, payload, token)
}

// decodeBody decodes a JSON response into v.
func decodeBody(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

// ── Feature flag ────────────────────────────────────────────────────────────

func TestConfig_ReportsPasskeyAvailability(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
	}{
		{name: "relying party configured", enabled: true},
		{name: "no relying party", enabled: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeStore{}
			var srv *Server
			if tc.enabled {
				srv = newPasskeyServer(t, st)
			} else {
				srv = newTestServer(t, st, nil)
			}

			resp := doJSON(t, srv, http.MethodGet, "/api/config", "", "")
			var got model.ConfigResponse
			decodeBody(t, resp, &got)

			if got.Auth.PasskeyEnabled != tc.enabled {
				t.Errorf("auth.passkey_enabled = %v, want %v", got.Auth.PasskeyEnabled, tc.enabled)
			}
		})
	}
}

func TestPasskeyRoutes_NotImplementedWhenUnconfigured(t *testing.T) {
	srv := newTestServer(t, &fakeStore{}, nil)

	cases := []struct {
		method string
		path   string
		token  string
		body   string
	}{
		{http.MethodGet, "/api/passkeys", testToken, ""},
		{http.MethodPost, "/api/passkeys/register/begin", testToken, "{}"},
		{http.MethodPost, "/api/passkeys/register/finish", testToken, `{"ceremony_id":"x","credential":{}}`},
		{http.MethodPatch, "/api/passkeys/pk-1", testToken, `{"name":"x"}`},
		{http.MethodDelete, "/api/passkeys/pk-1", testToken, ""},
		{http.MethodPost, "/api/passkeys/login/begin", "", "{}"},
		{http.MethodPost, "/api/passkeys/login/finish", "", `{"ceremony_id":"x","credential":{}}`},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			resp := doJSON(t, srv, tc.method, tc.path, tc.token, tc.body)
			if resp.StatusCode != http.StatusNotImplemented {
				t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotImplemented)
			}
		})
	}
}

func TestPasskeyManagementRoutes_RequireAuth(t *testing.T) {
	srv := newPasskeyServer(t, &fakeStore{})

	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/passkeys", ""},
		{http.MethodPost, "/api/passkeys/register/begin", "{}"},
		{http.MethodPost, "/api/passkeys/register/finish", `{"ceremony_id":"x","credential":{}}`},
		{http.MethodPatch, "/api/passkeys/pk-1", `{"name":"x"}`},
		{http.MethodDelete, "/api/passkeys/pk-1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			resp := doJSON(t, srv, tc.method, tc.path, "", tc.body)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", resp.StatusCode)
			}
		})
	}
}

// ── Listing ─────────────────────────────────────────────────────────────────

func TestListPasskeys_EmptyIsAnArrayNotNull(t *testing.T) {
	srv := newPasskeyServer(t, &fakeStore{})

	resp := doJSON(t, srv, http.MethodGet, "/api/passkeys", testToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	// A nil Go slice serializes as null, and the typed client throws during
	// render — a failure path Sentry never sees.
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("body = %q, want []", string(body))
	}
}

func TestListPasskeys_ReturnsStoredEntries(t *testing.T) {
	used := time.Now().UTC().Truncate(time.Second)
	st := &fakeStore{passkeys: []ports.Passkey{
		{ID: "pk-1", Name: "MacBook", CredentialID: []byte("c1"), Credential: []byte(`{}`), CreatedAt: used, LastUsedAt: &used},
		{ID: "pk-2", Name: "iPhone", CredentialID: []byte("c2"), Credential: []byte(`{}`), CreatedAt: used},
	}}
	srv := newPasskeyServer(t, st)

	resp := doJSON(t, srv, http.MethodGet, "/api/passkeys", testToken, "")
	var got []model.PasskeyView
	decodeBody(t, resp, &got)

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Name != "MacBook" || got[0].LastUsedAt == nil {
		t.Errorf("first entry = %+v", got[0])
	}
	// Never used yet must be an explicit null, not a zero timestamp the UI would
	// render as 1 January year 1.
	if got[1].LastUsedAt != nil {
		t.Errorf("second entry LastUsedAt = %v, want nil", got[1].LastUsedAt)
	}
}

// ── Registration ────────────────────────────────────────────────────────────

func TestBeginPasskeyRegistration_ReturnsChallenge(t *testing.T) {
	st := &fakeStore{user: &model.User{Username: "reader", WebAuthnUserHandle: []byte("0123456789abcdef")}}
	srv := newPasskeyServer(t, st)

	resp := doJSON(t, srv, http.MethodPost, "/api/passkeys/register/begin", testToken, "{}")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got struct {
		CeremonyID string `json:"ceremony_id"`
		Options    struct {
			PublicKey struct {
				Challenge string `json:"challenge"`
				RP        struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"rp"`
				AuthenticatorSelection struct {
					ResidentKey      string `json:"residentKey"`
					UserVerification string `json:"userVerification"`
				} `json:"authenticatorSelection"`
			} `json:"publicKey"`
		} `json:"options"`
	}
	decodeBody(t, resp, &got)

	if got.CeremonyID == "" {
		t.Error("ceremony_id is empty")
	}
	if got.Options.PublicKey.Challenge == "" {
		t.Error("challenge is empty")
	}
	if got.Options.PublicKey.RP.ID != testRPID {
		t.Errorf("rp.id = %q, want %q", got.Options.PublicKey.RP.ID, testRPID)
	}
	// Discoverable login is only possible with a resident key, and the account is
	// protected by user verification rather than a second factor.
	if got.Options.PublicKey.AuthenticatorSelection.ResidentKey != "required" {
		t.Errorf("residentKey = %q, want required", got.Options.PublicKey.AuthenticatorSelection.ResidentKey)
	}
	if got.Options.PublicKey.AuthenticatorSelection.UserVerification != "required" {
		t.Errorf("userVerification = %q, want required", got.Options.PublicKey.AuthenticatorSelection.UserVerification)
	}
}

func TestFinishPasskeyRegistration_BadRequests(t *testing.T) {
	st := &fakeStore{user: &model.User{Username: "reader", WebAuthnUserHandle: []byte("0123456789abcdef")}}
	srv := newPasskeyServer(t, st)

	cases := []struct {
		name string
		body string
	}{
		{name: "missing ceremony id", body: `{"credential":{"id":"x"}}`},
		{name: "missing credential", body: `{"ceremony_id":"abc"}`},
		{name: "unknown ceremony", body: `{"ceremony_id":"abc","credential":{"id":"x"}}`},
		{name: "name too long", body: `{"ceremony_id":"abc","credential":{"id":"x"},"name":"` + strings.Repeat("n", 65) + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doJSON(t, srv, http.MethodPost, "/api/passkeys/register/finish", testToken, tc.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

// ── Login ───────────────────────────────────────────────────────────────────

func TestBeginPasskeyLogin_NeedsNoSession(t *testing.T) {
	srv := newPasskeyServer(t, &fakeStore{})

	// The whole point of the flow: no bearer token, and no username either.
	resp := doJSON(t, srv, http.MethodPost, "/api/passkeys/login/begin", "", "{}")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got struct {
		CeremonyID string `json:"ceremony_id"`
		Options    struct {
			PublicKey struct {
				Challenge        string `json:"challenge"`
				RPID             string `json:"rpId"`
				UserVerification string `json:"userVerification"`
				AllowCredentials []any  `json:"allowCredentials"`
			} `json:"publicKey"`
		} `json:"options"`
	}
	decodeBody(t, resp, &got)

	if got.CeremonyID == "" || got.Options.PublicKey.Challenge == "" {
		t.Fatalf("incomplete challenge: %+v", got)
	}
	if got.Options.PublicKey.RPID != testRPID {
		t.Errorf("rpId = %q, want %q", got.Options.PublicKey.RPID, testRPID)
	}
	// A discoverable challenge must not name credentials — that would leak which
	// authenticators are registered to an unauthenticated caller.
	if len(got.Options.PublicKey.AllowCredentials) != 0 {
		t.Errorf("allowCredentials = %v, want empty", got.Options.PublicKey.AllowCredentials)
	}
}

func TestBeginPasskeyLogin_ChallengesAreUnique(t *testing.T) {
	srv := newPasskeyServer(t, &fakeStore{})

	seen := map[string]bool{}
	for range 3 {
		resp := doJSON(t, srv, http.MethodPost, "/api/passkeys/login/begin", "", "{}")
		var got model.PasskeyChallenge
		decodeBody(t, resp, &got)
		if seen[got.CeremonyID] {
			t.Fatalf("ceremony id %q was reused", got.CeremonyID)
		}
		seen[got.CeremonyID] = true
	}
}

func TestFinishPasskeyLogin_RejectsUnknownCeremony(t *testing.T) {
	st := &fakeStore{user: &model.User{Username: "reader", WebAuthnUserHandle: []byte("0123456789abcdef")}}
	srv := newPasskeyServer(t, st)

	resp := doJSON(t, srv, http.MethodPost, "/api/passkeys/login/finish", "",
		`{"ceremony_id":"nope","credential":{"id":"x","rawId":"eA","type":"public-key","response":{}}}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	// No session may be minted by a failed ceremony.
	if len(st.sessions) != 1 {
		t.Errorf("sessions = %d, want only the pre-seeded test session", len(st.sessions))
	}
}

func TestFinishPasskeyLogin_RejectsReplayedCeremony(t *testing.T) {
	st := &fakeStore{user: &model.User{Username: "reader", WebAuthnUserHandle: []byte("0123456789abcdef")}}
	srv := newPasskeyServer(t, st)

	begin := doJSON(t, srv, http.MethodPost, "/api/passkeys/login/begin", "", "{}")
	var challenge model.PasskeyChallenge
	decodeBody(t, begin, &challenge)

	body := `{"ceremony_id":"` + challenge.CeremonyID + `","credential":{"id":"x","rawId":"eA","type":"public-key","response":{}}}`

	// The first attempt fails on the (bogus) assertion, but it must still consume
	// the challenge — otherwise a captured challenge could be retried forever.
	first := doJSON(t, srv, http.MethodPost, "/api/passkeys/login/finish", "", body)
	if first.StatusCode != http.StatusUnauthorized {
		t.Fatalf("first status = %d, want 401", first.StatusCode)
	}
	second := doJSON(t, srv, http.MethodPost, "/api/passkeys/login/finish", "", body)
	if second.StatusCode != http.StatusUnauthorized {
		t.Fatalf("second status = %d, want 401", second.StatusCode)
	}
}

func TestPasskeyLogin_IsRateLimited(t *testing.T) {
	srv := newPasskeyServer(t, &fakeStore{})
	srv.passkeyMax = 2
	srv.app = srv.buildApp(testSiteFS())

	for i := range 3 {
		resp := doJSON(t, srv, http.MethodPost, "/api/passkeys/login/begin", "", "{}")
		if i < 2 && resp.StatusCode != http.StatusOK {
			t.Fatalf("call %d status = %d, want 200", i, resp.StatusCode)
		}
		if i == 2 && resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("call %d status = %d, want 429", i, resp.StatusCode)
		}
	}
}

// ── Rename / delete ─────────────────────────────────────────────────────────

func TestRenamePasskey(t *testing.T) {
	st := &fakeStore{passkeys: []ports.Passkey{
		{ID: "pk-1", Name: "old", CredentialID: []byte("c1"), Credential: []byte(`{}`)},
	}}
	srv := newPasskeyServer(t, st)

	resp := doJSON(t, srv, http.MethodPatch, "/api/passkeys/pk-1", testToken, `{"name":"  MacBook  "}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if st.renamedTo != "MacBook" {
		t.Errorf("stored name = %q, want the trimmed %q", st.renamedTo, "MacBook")
	}
}

func TestRenamePasskey_Errors(t *testing.T) {
	st := &fakeStore{passkeys: []ports.Passkey{
		{ID: "pk-1", Name: "old", CredentialID: []byte("c1"), Credential: []byte(`{}`)},
	}}
	srv := newPasskeyServer(t, st)

	cases := []struct {
		name string
		path string
		body string
		want int
	}{
		// A rename is an explicit edit, so an empty value is a mistake rather than
		// a request for the default label.
		{name: "empty name", path: "/api/passkeys/pk-1", body: `{"name":"   "}`, want: http.StatusBadRequest},
		{name: "too long", path: "/api/passkeys/pk-1", body: `{"name":"` + strings.Repeat("n", 65) + `"}`, want: http.StatusBadRequest},
		{name: "unknown id", path: "/api/passkeys/pk-9", body: `{"name":"x"}`, want: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doJSON(t, srv, http.MethodPatch, tc.path, testToken, tc.body)
			if resp.StatusCode != tc.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestDeletePasskey(t *testing.T) {
	st := &fakeStore{passkeys: []ports.Passkey{
		{ID: "pk-1", Name: "MacBook", CredentialID: []byte("c1"), Credential: []byte(`{}`)},
	}}
	srv := newPasskeyServer(t, st)

	resp := doJSON(t, srv, http.MethodDelete, "/api/passkeys/pk-1", testToken, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if len(st.passkeys) != 0 {
		t.Errorf("passkeys = %d, want 0", len(st.passkeys))
	}

	again := doJSON(t, srv, http.MethodDelete, "/api/passkeys/pk-1", testToken, "")
	if again.StatusCode != http.StatusNotFound {
		t.Errorf("second delete status = %d, want 404", again.StatusCode)
	}
}

// ── Native app association ──────────────────────────────────────────────────

func TestAppleAppSiteAssociation(t *testing.T) {
	t.Run("404 when unconfigured", func(t *testing.T) {
		srv := newTestServer(t, &fakeStore{}, nil)
		resp := doJSON(t, srv, http.MethodGet, "/.well-known/apple-app-site-association", "", "")
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("declares only webcredentials", func(t *testing.T) {
		srv := newTestServerCfg(t, &fakeStore{}, nil, func(cfg *config.Config) {
			cfg.PasskeyIOSAppID = "ABCDE12345.ru.tinyops.deepreader"
		})
		resp := doJSON(t, srv, http.MethodGet, "/.well-known/apple-app-site-association", "", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		var got map[string]json.RawMessage
		decodeBody(t, resp, &got)
		if _, ok := got["webcredentials"]; !ok {
			t.Error("missing webcredentials")
		}
		// Declaring applinks we do not handle would make iOS intercept ordinary
		// reader URLs and open the app instead of the browser.
		if _, ok := got["applinks"]; ok {
			t.Error("applinks must not be declared")
		}
	})
}

func TestAssetLinks(t *testing.T) {
	const fingerprint = "48:BC:5A:46:D9:09:F8:B0:31:7F:9A:D8:E2:4B:AC:4A:FD:B5:28:C7:A7:A9:E5:4A:A2:F1:47:31:3F:A1:3E:42"

	cases := []struct {
		name         string
		pkg          string
		fingerprints []string
		want         int
	}{
		{name: "unconfigured", want: http.StatusNotFound},
		{name: "package without fingerprint", pkg: "ru.tinyops.deepreader", want: http.StatusNotFound},
		{name: "fingerprint without package", fingerprints: []string{fingerprint}, want: http.StatusNotFound},
		{name: "complete", pkg: "ru.tinyops.deepreader", fingerprints: []string{fingerprint}, want: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServerCfg(t, &fakeStore{}, nil, func(cfg *config.Config) {
				cfg.PasskeyAndroidPackage = tc.pkg
				cfg.PasskeyAndroidFingerprints = tc.fingerprints
			})
			resp := doJSON(t, srv, http.MethodGet, "/.well-known/assetlinks.json", "", "")
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
			if tc.want != http.StatusOK {
				return
			}

			var got []assetLink
			decodeBody(t, resp, &got)
			if len(got) != 1 {
				t.Fatalf("len = %d, want 1", len(got))
			}
			// get_login_creds is the relation that authorises passkey sharing;
			// handle_all_urls (app links) would not enable passkeys at all.
			if got[0].Relation[0] != "delegate_permission/common.get_login_creds" {
				t.Errorf("relation = %v", got[0].Relation)
			}
			if got[0].Target.PackageName != tc.pkg {
				t.Errorf("package_name = %q, want %q", got[0].Target.PackageName, tc.pkg)
			}
			if got[0].Target.SHA256CertFingerprints[0] != fingerprint {
				t.Errorf("fingerprint = %q", got[0].Target.SHA256CertFingerprints[0])
			}
		})
	}
}

// ── Name validation ─────────────────────────────────────────────────────────

func TestValidatePasskeyName(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		def   string
		want  string
		valid bool
	}{
		{name: "trimmed", in: "  MacBook  ", def: "Passkey", want: "MacBook", valid: true},
		{name: "empty falls back to default", in: "", def: "Passkey", want: "Passkey", valid: true},
		{name: "empty is invalid without a default", in: "  ", def: "", valid: false},
		{name: "at the limit", in: strings.Repeat("n", 64), def: "Passkey", want: strings.Repeat("n", 64), valid: true},
		{name: "over the limit", in: strings.Repeat("n", 65), def: "Passkey", valid: false},
		// The bound counts runes, so a name of multi-byte characters is not
		// rejected for being under the limit in characters but over it in bytes.
		{name: "multibyte at the limit", in: strings.Repeat("я", 64), def: "Passkey", want: strings.Repeat("я", 64), valid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, ok := validatePasskeyName(tc.in, tc.def)
			if ok != tc.valid {
				t.Fatalf("ok = %v, want %v", ok, tc.valid)
			}
			if ok && got != tc.want {
				t.Errorf("name = %q, want %q", got, tc.want)
			}
		})
	}
}
