// export_test.go exposes private helpers for whitebox testing.
package enrich

import (
	"time"

	"deep-reader/internal/model"
)

// ValidateEnrichment exposes the unexported validateEnrichment for unit tests.
func ValidateEnrichment(e *model.Enrichment, tokenCount int) error {
	return validateEnrichment(e, tokenCount)
}

// BackoffDuration exposes the unexported backoffDuration for unit tests.
func BackoffDuration(attempt int, base, cap time.Duration) time.Duration {
	return backoffDuration(attempt, base, cap)
}

// IsRetryable exposes the unexported isRetryable for unit tests.
func IsRetryable(err error) bool {
	return isRetryable(err)
}
