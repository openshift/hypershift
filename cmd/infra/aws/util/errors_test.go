package util

import (
	"errors"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/aws/aws-sdk-go-v2/config"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

func TestIsErrorRetryable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When error is a generic error it should be retryable",
			err:      errors.New("some transient error"),
			expected: true,
		},
		{
			name:     "When error is a credential load error it should not be retryable",
			err:      config.SharedConfigLoadError{},
			expected: false,
		},
		{
			name:     "When error is a wrapped credential load error it should not be retryable",
			err:      fmt.Errorf("loading config: %w", config.SharedConfigLoadError{}),
			expected: false,
		},
		{
			name:     "When aggregate has single generic error it should be retryable",
			err:      utilerrors.NewAggregate([]error{errors.New("transient")}),
			expected: true,
		},
		{
			name:     "When aggregate has single credential load error it should not be retryable",
			err:      utilerrors.NewAggregate([]error{config.SharedConfigLoadError{}}),
			expected: false,
		},
		{
			name: "When aggregate has only credential load errors it should not be retryable",
			err: utilerrors.NewAggregate([]error{
				config.SharedConfigLoadError{},
				config.SharedConfigLoadError{},
			}),
			expected: false,
		},
		{
			name: "When aggregate has mixed errors it should be retryable",
			err: utilerrors.NewAggregate([]error{
				config.SharedConfigLoadError{},
				errors.New("some other error"),
			}),
			expected: true,
		},
		{
			name:     "When wrapped aggregate has single generic error it should be retryable",
			err:      fmt.Errorf("operation failed: %w", utilerrors.NewAggregate([]error{errors.New("transient")})),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(IsErrorRetryable(tt.err)).To(Equal(tt.expected))
		})
	}
}
