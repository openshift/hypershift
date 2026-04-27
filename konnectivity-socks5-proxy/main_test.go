package konnectivitysocks5proxy

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestRetryWithBackoff(t *testing.T) {
	tests := []struct {
		name       string
		maxRetries int
		failCount  int
		wantErr    bool
		wantCalls  int
	}{
		{
			name:       "When function succeeds on first attempt, it should return immediately",
			maxRetries: 3,
			failCount:  0,
			wantErr:    false,
			wantCalls:  1,
		},
		{
			name:       "When function fails twice then succeeds, it should retry and return nil",
			maxRetries: 3,
			failCount:  2,
			wantErr:    false,
			wantCalls:  3,
		},
		{
			name:       "When function fails all attempts, it should return the last error",
			maxRetries: 3,
			failCount:  3,
			wantErr:    true,
			wantCalls:  3,
		},
		{
			name:       "When maxRetries is 1 and function fails, it should only call once",
			maxRetries: 1,
			failCount:  1,
			wantErr:    true,
			wantCalls:  1,
		},
		{
			name:       "When function succeeds on last attempt, it should return nil",
			maxRetries: 5,
			failCount:  4,
			wantErr:    false,
			wantCalls:  5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			calls := 0
			err := retryWithBackoff(tt.maxRetries, 1*time.Millisecond, func() error {
				calls++
				if calls <= tt.failCount {
					return fmt.Errorf("error on attempt %d", calls)
				}
				return nil
			})

			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(calls).To(Equal(tt.wantCalls))
		})
	}
}

func TestRetryWithBackoff_WhenAllAttemptsFail_ShouldReturnLastError(t *testing.T) {
	g := NewWithT(t)

	calls := 0
	err := retryWithBackoff(3, 1*time.Millisecond, func() error {
		calls++
		return fmt.Errorf("error on attempt %d", calls)
	})

	g.Expect(err).To(MatchError("error on attempt 3"))
}

func TestRetryWithBackoff_ShouldNotSleepAfterLastAttempt(t *testing.T) {
	g := NewWithT(t)

	start := time.Now()
	_ = retryWithBackoff(3, 50*time.Millisecond, func() error {
		return fmt.Errorf("fail")
	})
	elapsed := time.Since(start)

	// 3 attempts: sleep after attempt 1 and 2 (not after 3) = ~100ms total
	g.Expect(elapsed).To(BeNumerically("<", 150*time.Millisecond))
	g.Expect(elapsed).To(BeNumerically(">=", 90*time.Millisecond))
}

func TestStartupConstants(t *testing.T) {
	g := NewWithT(t)
	g.Expect(startupMaxRetries).To(Equal(30))
	g.Expect(startupRetryInterval).To(Equal(10 * time.Second))
}
