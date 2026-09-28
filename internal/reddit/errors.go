package reddit

import (
	"errors"
	"fmt"
	"math"
	"time"
)

var (
	// ErrNotFound means the post or subreddit does not exist, was removed, or is banned.
	ErrNotFound = errors.New("reddit: not found")
	// ErrForbidden means the content is private, quarantined or otherwise restricted.
	ErrForbidden = errors.New("reddit: forbidden")
	// ErrUnexpectedResponse means Reddit answered with something other than JSON.
	ErrUnexpectedResponse = errors.New("reddit: unexpected non-JSON response")
)

// InputError reports a caller mistake; Msg is safe to show to the model as is.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// RateLimitError reports an exhausted Reddit rate limit.
type RateLimitError struct{ RetryAfter time.Duration }

// Seconds returns RetryAfter rounded up to whole seconds.
func (e *RateLimitError) Seconds() int { return int(math.Ceil(e.RetryAfter.Seconds())) }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("reddit: rate limit exhausted, retry in %ds", e.Seconds())
}

// AuthError reports rejected or unusable API credentials.
type AuthError struct {
	Status int
	Reason string
}

func (e *AuthError) Error() string {
	s := fmt.Sprintf("reddit: authentication failed (HTTP %d)", e.Status)
	if e.Reason != "" {
		s += ": " + e.Reason
	}
	return s
}

// StatusError reports any other unexpected HTTP status.
type StatusError struct{ Status int }

func (e *StatusError) Error() string {
	return fmt.Sprintf("reddit: unexpected HTTP status %d", e.Status)
}
