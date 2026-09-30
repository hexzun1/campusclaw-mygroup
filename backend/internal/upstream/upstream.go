// Package upstream carries the "dependency unavailable" contract shared by
// every external client (embedding gateway, chat gateway, vector store).
//
// Business handlers translate it into 503. The message of an
// UnavailableError is deliberately sanitized: it never contains the upstream
// address, a credential or a raw transport error, because those must not reach
// a client (spec: 依赖不可用时的降级). The original error stays available to
// server-side logging through Cause.
package upstream

import "errors"

// ErrUnavailable is the sentinel every external-service failure wraps, so
// callers can branch with errors.Is(err, upstream.ErrUnavailable).
var ErrUnavailable = errors.New("upstream dependency unavailable")

// UnavailableError is a sanitized external-service failure.
type UnavailableError struct {
	reason string
	cause  error
}

// Error returns the sanitized reason.
func (e *UnavailableError) Error() string { return e.reason }

// Unwrap reports ErrUnavailable, which is what callers test for.
func (e *UnavailableError) Unwrap() error { return ErrUnavailable }

// Cause exposes the underlying transport or decoding error for logs. It MUST
// NOT be written into an HTTP response.
func (e *UnavailableError) Cause() error { return e.cause }

// Unavailable builds a sanitized dependency failure. reason is shown to
// operators and may be logged; cause is kept for diagnostics only.
func Unavailable(reason string, cause error) error {
	return &UnavailableError{reason: reason, cause: cause}
}

// IsUnavailable reports whether err is a dependency failure.
func IsUnavailable(err error) bool {
	return errors.Is(err, ErrUnavailable)
}
