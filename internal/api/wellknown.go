package api

import (
	"github.com/gofiber/fiber/v3"
)

// Native passkeys require the server to vouch for the app that will use them:
// iOS reads /.well-known/apple-app-site-association and Android reads
// /.well-known/assetlinks.json, both over HTTPS on the RP ID's own domain, and
// both without following redirects or accepting a non-JSON content type.
//
// Serving them from the binary (rather than dropping files next to the reverse
// proxy) keeps a docker-only install complete: the same PASSKEY_* variables that
// configure the relying party also publish the association, so there is no
// second place to keep in sync when the app id or signing key changes.

// appleAppSiteAssociation is the /.well-known/apple-app-site-association
// document. Only the webcredentials service is declared — Deep Reader does not
// use universal links, and declaring applinks we do not handle would make iOS
// intercept ordinary reader URLs.
type appleAppSiteAssociation struct {
	WebCredentials appleWebCredentials `json:"webcredentials"`
}

type appleWebCredentials struct {
	Apps []string `json:"apps"`
}

// assetLink is one entry of the /.well-known/assetlinks.json array. The
// get_login_creds relation is what authorises the Android app to use passkeys
// registered for this domain; it is distinct from the handle_all_urls relation
// used for app links.
type assetLink struct {
	Relation []string        `json:"relation"`
	Target   assetLinkTarget `json:"target"`
}

type assetLinkTarget struct {
	Namespace              string   `json:"namespace"`
	PackageName            string   `json:"package_name"`
	SHA256CertFingerprints []string `json:"sha256_cert_fingerprints"`
}

// serveAppleAppSiteAssociation handles GET /.well-known/apple-app-site-association.
// It answers 404 when PASSKEY_IOS_APP_ID is unset, so a deployment that does not
// ship an iOS build publishes nothing.
//
// The document must be served as application/json with no .json extension in the
// path; Fiber's c.JSON sets the content type, and the route carries the exact
// well-known path.
func (s *Server) serveAppleAppSiteAssociation(c fiber.Ctx) error {
	appID := s.cfg.PasskeyIOSAppID
	if appID == "" {
		return sendError(c, fiber.StatusNotFound, "not found")
	}
	return c.JSON(appleAppSiteAssociation{
		WebCredentials: appleWebCredentials{Apps: []string{appID}},
	})
}

// serveAssetLinks handles GET /.well-known/assetlinks.json. It answers 404
// unless both the package name and at least one signing-certificate fingerprint
// are configured — a link statement missing either is useless to Android and
// only makes the failure harder to read.
func (s *Server) serveAssetLinks(c fiber.Ctx) error {
	pkg := s.cfg.PasskeyAndroidPackage
	fingerprints := s.cfg.PasskeyAndroidFingerprints
	if pkg == "" || len(fingerprints) == 0 {
		return sendError(c, fiber.StatusNotFound, "not found")
	}
	links := []assetLink{{
		Relation: []string{"delegate_permission/common.get_login_creds"},
		Target: assetLinkTarget{
			Namespace:              "android_app",
			PackageName:            pkg,
			SHA256CertFingerprints: fingerprints,
		},
	}}
	return c.JSON(links)
}
