package mail

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorsAreSentinelErrors(t *testing.T) {
	sentinelErrors := []error{
		ErrNotConnected,
		ErrAuthFailed,
		ErrThreadNotFound,
		ErrMessageNotFound,
		ErrConnectionLost,
		ErrInvalidCredentials,
		ErrMailboxNotFound,
		ErrRateLimited,
		ErrTimeout,
	}

	for _, err := range sentinelErrors {
		if err == nil {
			t.Error("sentinel error should not be nil")
		}
		if err.Error() == "" {
			t.Error("sentinel error should have a message")
		}
	}
}

func TestErrorsAreDistinct(t *testing.T) {
	allErrors := []error{
		ErrNotConnected,
		ErrAuthFailed,
		ErrThreadNotFound,
		ErrMessageNotFound,
		ErrConnectionLost,
		ErrInvalidCredentials,
		ErrMailboxNotFound,
		ErrRateLimited,
		ErrTimeout,
	}

	for i, err1 := range allErrors {
		for j, err2 := range allErrors {
			if i != j && errors.Is(err1, err2) {
				t.Errorf("errors should be distinct: %v and %v", err1, err2)
			}
		}
	}
}

func TestErrorsCanBeWrapped(t *testing.T) {
	wrapped := fmt.Errorf("connection to server failed: %w", ErrConnectionLost)

	if !errors.Is(wrapped, ErrConnectionLost) {
		t.Error("wrapped error should match ErrConnectionLost with errors.Is")
	}

	// Verify the wrapped error message contains both parts
	msg := wrapped.Error()
	if msg != "connection to server failed: connection lost" {
		t.Errorf("unexpected wrapped error message: %q", msg)
	}
}

func TestErrorMessages(t *testing.T) {
	tests := []struct {
		err      error
		expected string
	}{
		{ErrNotConnected, "mail connector not connected"},
		{ErrAuthFailed, "authentication failed"},
		{ErrThreadNotFound, "thread not found"},
		{ErrMessageNotFound, "message not found"},
		{ErrConnectionLost, "connection lost"},
		{ErrInvalidCredentials, "invalid credentials"},
		{ErrMailboxNotFound, "mailbox not found"},
		{ErrRateLimited, "rate limited by mail provider"},
		{ErrTimeout, "operation timed out"},
	}

	for _, tc := range tests {
		if tc.err.Error() != tc.expected {
			t.Errorf("expected %q, got %q", tc.expected, tc.err.Error())
		}
	}
}

func TestErrorsIsComparison(t *testing.T) {
	// Test that errors.Is works correctly for each error
	tests := []struct {
		name   string
		err    error
		target error
		want   bool
	}{
		{"same error", ErrNotConnected, ErrNotConnected, true},
		{"wrapped error", fmt.Errorf("wrap: %w", ErrAuthFailed), ErrAuthFailed, true},
		{"different error", ErrThreadNotFound, ErrMessageNotFound, false},
		{"nil error", nil, ErrConnectionLost, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := errors.Is(tc.err, tc.target)
			if got != tc.want {
				t.Errorf("errors.Is(%v, %v) = %v, want %v", tc.err, tc.target, got, tc.want)
			}
		})
	}
}
