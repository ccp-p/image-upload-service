package main

import (
	"context"
	"regexp"
	"sync"
	"time"
)

// otpPattern matches 4-8 digit numeric codes.
var otpPattern = regexp.MustCompile(`^\d{4,8}$`)

// otpExtractPattern finds a 4-8 digit sequence inside longer text.
var otpExtractPattern = regexp.MustCompile(`\b(\d{4,8})\b`)

// isValidOTP returns true when s is exactly a 4-8 digit numeric code.
func isValidOTP(s string) bool {
	return otpPattern.MatchString(s)
}

// extractOTP returns the OTP embedded in s. If s is already a valid OTP it is
// returned as-is; otherwise the first 4-8 digit group is extracted.
func extractOTP(s string) (string, bool) {
	if isValidOTP(s) {
		return s, true
	}
	m := otpExtractPattern.FindStringSubmatch(s)
	if len(m) >= 2 {
		return m[1], true
	}
	return "", false
}

// otpEntry stores an OTP along with the time it was observed.
type otpEntry struct {
	value string
	at    time.Time
}

// OTPStore is a thread-safe holder for the most recent OTP. Callers that need
// an OTP (e.g. the SSH keyboard-interactive callback) can call WaitForOTP to
// block until a new code is available.
type OTPStore struct {
	clock func() time.Time

	mu        sync.Mutex
	current   otpEntry
	hasValue  bool
	notify    chan struct{}
	validator func(string) bool
}

// NewOTPStore creates a new OTPStore. If clock is nil time.Now is used.
func NewOTPStore(clock func() time.Time) *OTPStore {
	if clock == nil {
		clock = time.Now
	}
	return &OTPStore{
		clock:     clock,
		notify:    make(chan struct{}),
		validator: isValidOTP,
	}
}

// SetValidator overrides the default OTP validator. Useful for tests that need
// non-standard code formats.
func (s *OTPStore) SetValidator(fn func(string) bool) {
	s.mu.Lock()
	s.validator = fn
	s.mu.Unlock()
}

// Set stores otp as the latest code and wakes any goroutine blocked in
// WaitForOTP. Values that fail the validator are silently ignored.
func (s *OTPStore) Set(otp string) {
	s.mu.Lock()
	if s.validator != nil && !s.validator(otp) {
		s.mu.Unlock()
		return
	}
	s.current = otpEntry{value: otp, at: s.clock()}
	s.hasValue = true

	// Broadcast to all waiters by closing the old channel and creating a new one.
	old := s.notify
	s.notify = make(chan struct{})
	s.mu.Unlock()

	close(old)
}

// Latest returns the most recent OTP and its timestamp. The third return is
// false when no OTP has ever been stored.
func (s *OTPStore) Latest() (string, time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasValue {
		return "", time.Time{}, false
	}
	return s.current.value, s.current.at, true
}

// Clear removes the stored OTP so the next WaitForOTP blocks.
func (s *OTPStore) Clear() {
	s.mu.Lock()
	s.hasValue = false
	s.current = otpEntry{}
	old := s.notify
	s.notify = make(chan struct{})
	s.mu.Unlock()
	close(old)
}

// WaitForOTP blocks until an OTP newer than notBefore is available or ctx is
// cancelled. Passing the zero time accepts any stored value.
func (s *OTPStore) WaitForOTP(ctx context.Context, notBefore time.Time) (string, error) {
	// Fast path: a suitable value already exists.
	if otp, ts, ok := s.Latest(); ok && (notBefore.IsZero() || ts.After(notBefore)) {
		return otp, nil
	}

	for {
		s.mu.Lock()
		ch := s.notify
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ch:
			// Channel closed — a new OTP (or Clear) happened. Re-check.
			if otp, ts, ok := s.Latest(); ok && (notBefore.IsZero() || ts.After(notBefore)) {
				return otp, nil
			}
			// Not suitable; loop and wait on the new channel.
		}
	}
}
