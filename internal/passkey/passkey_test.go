package passkey

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"deep-reader/internal/config"
)

func TestNew_Disabled(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
	}{
		{
			name: "explicitly disabled",
			cfg:  &config.Config{PasskeyEnabled: false, PasskeyRPID: "reader.example"},
		},
		{
			// Without an RP ID there is nothing to bind credentials to, and every
			// ceremony would fail in the browser. Staying off is the honest answer.
			name: "no rp id and no public base url",
			cfg:  &config.Config{PasskeyEnabled: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, err := New(tc.cfg)
			if !errors.Is(err, ErrDisabled) {
				t.Fatalf("New error = %v, want ErrDisabled", err)
			}
			if svc != nil {
				t.Error("New returned a service alongside ErrDisabled")
			}
		})
	}
}

func TestNew_DerivesRPIDFromPublicBaseURL(t *testing.T) {
	svc, err := New(&config.Config{
		PasskeyEnabled: true,
		PublicBaseURL:  "https://reader.example:8443",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// The RP ID is a bare registrable domain — a port makes the browser reject
	// the ceremony outright.
	if got := svc.RPID(); got != "reader.example" {
		t.Errorf("RPID() = %q, want %q", got, "reader.example")
	}
	if got := svc.Origins(); !slices.Contains(got, "https://reader.example:8443") {
		t.Errorf("Origins() = %v, want it to contain the public base URL origin", got)
	}
}

func TestNew_DerivesOriginFromRPID(t *testing.T) {
	svc, err := New(&config.Config{
		PasskeyEnabled: true,
		PasskeyRPID:    "reader.example",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := svc.Origins(); !slices.Contains(got, "https://reader.example") {
		t.Errorf("Origins() = %v, want it to contain https://reader.example", got)
	}
}

func TestNew_ExplicitOriginsWin(t *testing.T) {
	svc, err := New(&config.Config{
		PasskeyEnabled:   true,
		PasskeyRPID:      "localhost",
		PasskeyRPOrigins: []string{"http://localhost:4200", "http://localhost:18080"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := svc.Origins()
	// An explicit list must not be silently widened with an https:// guess the
	// operator did not ask for.
	if slices.Contains(got, "https://localhost") {
		t.Errorf("Origins() = %v, want no derived https origin when the list is explicit", got)
	}
	for _, want := range []string{"http://localhost:4200", "http://localhost:18080"} {
		if !slices.Contains(got, want) {
			t.Errorf("Origins() = %v, want it to contain %q", got, want)
		}
	}
}

func TestNew_AppendsAndroidOrigins(t *testing.T) {
	// The SHA-256 fingerprint of a signing certificate, as printed by keytool and
	// as written into assetlinks.json.
	const fingerprint = "48:BC:5A:46:D9:09:F8:B0:31:7F:9A:D8:E2:4B:AC:4A:FD:B5:28:C7:A7:A9:E5:4A:A2:F1:47:31:3F:A1:3E:42"

	svc, err := New(&config.Config{
		PasskeyEnabled:             true,
		PasskeyRPID:                "reader.example",
		PasskeyAndroidPackage:      "ru.tinyops.deepreader",
		PasskeyAndroidFingerprints: []string{fingerprint},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Android's Credential Manager reports the caller as an apk-key-hash facet,
	// not as an https origin; without it every native login fails origin
	// validation on the server.
	const want = "android:apk-key-hash:SLxaRtkJ-LAxf5rY4kusSv21KMenqeVKovFHMT-hPkI"
	if got := svc.Origins(); !slices.Contains(got, want) {
		t.Errorf("Origins() = %v, want it to contain %q", got, want)
	}
}

func TestAndroidOrigins(t *testing.T) {
	const fingerprint = "48:BC:5A:46:D9:09:F8:B0:31:7F:9A:D8:E2:4B:AC:4A:FD:B5:28:C7:A7:A9:E5:4A:A2:F1:47:31:3F:A1:3E:42"

	cases := []struct {
		name         string
		fingerprints []string
		want         []string
		wantErr      bool
	}{
		{
			name:         "colon separated uppercase hex",
			fingerprints: []string{fingerprint},
			want: []string{
				"android:apk-key-hash:SLxaRtkJ-LAxf5rY4kusSv21KMenqeVKovFHMT-hPkI",
				"android:apk-key-hash:SLxaRtkJ+LAxf5rY4kusSv21KMenqeVKovFHMT+hPkI",
			},
		},
		{
			name:         "bare lowercase hex is accepted too",
			fingerprints: []string{"48bc5a46d909f8b0317f9ad8e24bac4afdb528c7a7a9e54aa2f147313fa13e42"},
			want: []string{
				"android:apk-key-hash:SLxaRtkJ-LAxf5rY4kusSv21KMenqeVKovFHMT-hPkI",
				"android:apk-key-hash:SLxaRtkJ+LAxf5rY4kusSv21KMenqeVKovFHMT+hPkI",
			},
		},
		{
			name:         "empty input yields nothing",
			fingerprints: nil,
		},
		{
			name:         "not hex",
			fingerprints: []string{"nonsense"},
			wantErr:      true,
		},
		{
			name:         "wrong length is not a sha-256",
			fingerprints: []string{"48:BC:5A"},
			wantErr:      true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := AndroidOrigins(tc.fingerprints)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("AndroidOrigins: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("AndroidOrigins() = %v, want %v", got, tc.want)
			}
		})
	}
}

// ── Ceremony cache ──────────────────────────────────────────────────────────

func newTestService(t *testing.T) *Service {
	t.Helper()
	svc, err := New(&config.Config{PasskeyEnabled: true, PasskeyRPID: "reader.example"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

func TestCeremony_RoundTrip(t *testing.T) {
	svc := newTestService(t)

	id, err := svc.storeCeremony(ceremonyLogin, sessionFixture("challenge-a"))
	if err != nil {
		t.Fatalf("storeCeremony: %v", err)
	}
	if id == "" {
		t.Fatal("storeCeremony returned an empty id")
	}

	sd, ok := svc.takeCeremony(ceremonyLogin, id)
	if !ok {
		t.Fatal("takeCeremony did not find the ceremony")
	}
	if sd.Challenge != "challenge-a" {
		t.Errorf("Challenge = %q, want %q", sd.Challenge, "challenge-a")
	}
}

func TestCeremony_IsSingleUse(t *testing.T) {
	svc := newTestService(t)

	id, err := svc.storeCeremony(ceremonyLogin, sessionFixture("challenge-a"))
	if err != nil {
		t.Fatalf("storeCeremony: %v", err)
	}
	if _, ok := svc.takeCeremony(ceremonyLogin, id); !ok {
		t.Fatal("first take failed")
	}
	// Replaying a completed ceremony must not re-validate an old assertion.
	if _, ok := svc.takeCeremony(ceremonyLogin, id); ok {
		t.Error("takeCeremony returned the same ceremony twice")
	}
}

func TestCeremony_KindIsEnforced(t *testing.T) {
	svc := newTestService(t)

	id, err := svc.storeCeremony(ceremonyRegister, sessionFixture("challenge-a"))
	if err != nil {
		t.Fatalf("storeCeremony: %v", err)
	}
	// A registration challenge finished through the (unauthenticated) login
	// endpoint would be a privilege escalation, so the kind is part of the key.
	if _, ok := svc.takeCeremony(ceremonyLogin, id); ok {
		t.Error("a registration ceremony was accepted by the login endpoint")
	}
	if _, ok := svc.takeCeremony(ceremonyRegister, id); !ok {
		t.Error("the registration ceremony was consumed by the mismatched take")
	}
}

func TestCeremony_Expires(t *testing.T) {
	svc := newTestService(t)

	id, err := svc.storeCeremony(ceremonyLogin, sessionFixture("challenge-a"))
	if err != nil {
		t.Fatalf("storeCeremony: %v", err)
	}
	svc.mu.Lock()
	svc.ceremonies[id].expires = time.Now().Add(-time.Second)
	svc.mu.Unlock()

	if _, ok := svc.takeCeremony(ceremonyLogin, id); ok {
		t.Error("an expired ceremony was accepted")
	}
}

func TestCeremony_CapIsEnforced(t *testing.T) {
	svc := newTestService(t)

	// The login "begin" endpoint is unauthenticated, so an unbounded cache is a
	// remote memory-growth vector. The oldest entries are evicted instead.
	for i := 0; i < maxLiveCeremonies+10; i++ {
		if _, err := svc.storeCeremony(ceremonyLogin, sessionFixture("challenge")); err != nil {
			t.Fatalf("storeCeremony(%d): %v", i, err)
		}
	}
	svc.mu.Lock()
	live := len(svc.ceremonies)
	svc.mu.Unlock()

	if live > maxLiveCeremonies {
		t.Errorf("live ceremonies = %d, want <= %d", live, maxLiveCeremonies)
	}
}

// sessionFixture builds a minimal SessionData for cache tests; only the
// challenge is read back, the ceremony cache is otherwise opaque about it.
func sessionFixture(challenge string) webauthn.SessionData {
	return webauthn.SessionData{Challenge: challenge, Expires: time.Now().Add(time.Minute)}
}
