package util

import (
	"testing"
)

func TestIntentionallyFailing(t *testing.T) {
	t.Run("When testing the job-analyzer it should fail intentionally", func(t *testing.T) {
		t.Fatal("This test fails intentionally for job-analyzer testing purposes")
	})
}
