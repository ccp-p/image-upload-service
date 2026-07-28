package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

// --- isValidOTP tests ---

func TestIsValidOTP_FourDigits(t *testing.T) {
	if !isValidOTP("1234") {
		t.Error("1234 should be valid")
	}
}

func TestIsValidOTP_SixDigits(t *testing.T) {
	if !isValidOTP("123456") {
		t.Error("123456 should be valid")
	}
}

func TestIsValidOTP_EightDigits(t *testing.T) {
	if !isValidOTP("12345678") {
		t.Error("12345678 should be valid")
	}
}

func TestIsValidOTP_ThreeDigits(t *testing.T) {
	if isValidOTP("123") {
		t.Error("123 should be invalid (too short)")
	}
}

func TestIsValidOTP_NineDigits(t *testing.T) {
	if isValidOTP("123456789") {
		t.Error("123456789 should be invalid (too long)")
	}
}

func TestIsValidOTP_WithLetters(t *testing.T) {
	if isValidOTP("12a456") {
		t.Error("12a456 should be invalid")
	}
}

func TestIsValidOTP_Empty(t *testing.T) {
	if isValidOTP("") {
		t.Error("empty string should be invalid")
	}
}

// --- extractOTP tests ---

func TestExtractOTP_PureDigits(t *testing.T) {
	otp, ok := extractOTP("654321")
	if !ok || otp != "654321" {
		t.Errorf("extractOTP('654321') = %q, %v; want '654321', true", otp, ok)
	}
}

func TestExtractOTP_Embedded(t *testing.T) {
	otp, ok := extractOTP("Your verification code is 836491, valid for 5 minutes.")
	if !ok || otp != "836491" {
		t.Errorf("extractOTP() = %q, %v; want '836491', true", otp, ok)
	}
}

func TestExtractOTP_NoMatch(t *testing.T) {
	_, ok := extractOTP("no digits here at all")
	if ok {
		t.Error("should not extract from text without digit groups")
	}
}

func TestExtractOTP_Empty(t *testing.T) {
	_, ok := extractOTP("")
	if ok {
		t.Error("empty string should not extract")
	}
}

// --- OTPStore tests ---

func TestOTPStore_SetAndLatest(t *testing.T) {
	s := NewOTPStore(nil)
	s.Set("123456")
	otp, _, ok := s.Latest()
	if !ok {
		t.Fatal("should have a value after Set")
	}
	if otp != "123456" {
		t.Errorf("Latest() = %q, want '123456'", otp)
	}
}

func TestOTPStore_Latest_Empty(t *testing.T) {
	s := NewOTPStore(nil)
	_, _, ok := s.Latest()
	if ok {
		t.Error("Latest() should return false when nothing set")
	}
}

func TestOTPStore_Set_InvalidIgnored(t *testing.T) {
	s := NewOTPStore(nil)
	s.Set("abc") // not a digit code
	_, _, ok := s.Latest()
	if ok {
		t.Error("invalid OTP should not be stored")
	}
}

func TestOTPStore_Set_OverwritesPrevious(t *testing.T) {
	s := NewOTPStore(nil)
	s.Set("111111")
	s.Set("222222")
	otp, _, ok := s.Latest()
	if !ok || otp != "222222" {
		t.Errorf("Latest() = %q, %v; want '222222', true", otp, ok)
	}
}

func TestOTPStore_Clear(t *testing.T) {
	s := NewOTPStore(nil)
	s.Set("123456")
	s.Clear()
	_, _, ok := s.Latest()
	if ok {
		t.Error("Latest() should return false after Clear")
	}
}

func TestOTPStore_WaitForOTP_AlreadyAvailable(t *testing.T) {
	s := NewOTPStore(nil)
	s.Set("999888")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	otp, err := s.WaitForOTP(ctx, time.Time{})
	if err != nil {
		t.Fatalf("WaitForOTP error: %v", err)
	}
	if otp != "999888" {
		t.Errorf("got %q, want '999888'", otp)
	}
}

func TestOTPStore_WaitForOTP_BlocksUntilSet(t *testing.T) {
	s := NewOTPStore(nil)
	done := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		otp, err := s.WaitForOTP(ctx, time.Time{})
		if err != nil {
			t.Errorf("WaitForOTP error: %v", err)
			close(done)
			return
		}
		if otp != "654321" {
			t.Errorf("got %q, want '654321'", otp)
		}
		close(done)
	}()

	// Give the waiter time to block
	time.Sleep(50 * time.Millisecond)
	s.Set("654321")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForOTP did not return after Set")
	}
}

func TestOTPStore_WaitForOTP_ContextCancelled(t *testing.T) {
	s := NewOTPStore(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := s.WaitForOTP(ctx, time.Time{})
	if err == nil {
		t.Error("expected error on context cancel")
	}
}

func TestOTPStore_WaitForOTP_OlderValueIgnored(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	s := NewOTPStore(clock.Now)

	// Set an OTP at time T
	clock.advance(10 * time.Second)
	setTime := clock.Now()
	s.Set("111111")

	// Request an OTP newer than the set time — should block
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := s.WaitForOTP(ctx, setTime.Add(1*time.Second))
	if err == nil {
		t.Error("should timeout because stored OTP is not newer than notBefore")
	}
}

func TestOTPStore_WaitForOTP_NewerValueAccepted(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	s := NewOTPStore(clock.Now)

	clock.advance(10 * time.Second)
	notBefore := clock.Now()

	// Set an OTP after notBefore
	clock.advance(5 * time.Second)
	s.Set("222222")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	otp, err := s.WaitForOTP(ctx, notBefore)
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if otp != "222222" {
		t.Errorf("got %q, want '222222'", otp)
	}
}

func TestOTPStore_MultipleWaiters(t *testing.T) {
	s := NewOTPStore(nil)
	var wg sync.WaitGroup
	count := 3
	wg.Add(count)

	for i := 0; i < count; i++ {
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := s.WaitForOTP(ctx, time.Time{})
			if err != nil {
				t.Errorf("waiter error: %v", err)
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)
	s.Set("444333")

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("not all waiters received the OTP")
	}
}

func TestOTPStore_SetValidator(t *testing.T) {
	s := NewOTPStore(nil)
	// Custom validator: only 6-digit codes
	s.SetValidator(func(code string) bool {
		return len(code) == 6
	})

	s.Set("1234") // 4 digits, should be rejected
	_, _, ok := s.Latest()
	if ok {
		t.Error("4-digit code should be rejected by custom validator")
	}

	s.Set("123456") // 6 digits, should be accepted
	otp, _, ok := s.Latest()
	if !ok || otp != "123456" {
		t.Errorf("6-digit code should be accepted: %q, %v", otp, ok)
	}
}

// --- fakeClock helper ---

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}
