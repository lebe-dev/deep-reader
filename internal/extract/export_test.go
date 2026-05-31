// export_test.go exposes internal symbols for the extract_test package only.
// This file is compiled only during 'go test'.
package extract

import "deep-reader/internal/config"

// NewForTest creates an Extractor that bypasses the SSRF guard for the
// specific allowedAddr (host:port). Used exclusively by tests that run an
// httptest.Server on 127.0.0.1.
func NewForTest(cfg *config.Config, allowedAddr string) *Extractor {
	return newWithOptions(cfg, allowedAddr)
}
