// export_test.go exposes internal symbols for the extract_test package only.
// This file is compiled only during 'go test'.
package extract

import (
	"net"

	"deep-reader/internal/config"
)

// NewForTest creates an Extractor that bypasses the SSRF guard for the
// specific allowedAddr (host:port). Used exclusively by tests that run an
// httptest.Server on 127.0.0.1.
func NewForTest(cfg *config.Config, allowedAddr string) *Extractor {
	return newWithOptions(cfg, allowedAddr)
}

// CheckConnectAddr exposes the connect-time SSRF guard (net.Dialer.Control body)
// so tests can assert the DNS-rebinding TOCTOU defense directly against a
// concrete resolved ip:port, independent of the global DNS resolver.
func CheckConnectAddr(address, allowedAddr string) error {
	return checkConnectAddr(address, allowedAddr)
}

// CheckIP exposes the per-IP SSRF range check for table tests.
func CheckIP(ip net.IP) error {
	return checkIP(ip)
}
