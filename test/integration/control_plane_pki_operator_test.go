package integration

import (
	"context"
	"os"
	"os/signal"
	"testing"
	"time"

	"github.com/openshift/hypershift/test/integration/framework"
)

func TestControlPlanePKIOperatorRevocation(t *testing.T) {
	cleanup, err := framework.SetupHostedCluster(testContext, log, globalOpts, t)
	defer func() {
		if err := cleanup(); err != nil {
			t.Errorf("failed to clean up: %v", err)
		}
	}()
	if err != nil {
		t.Fatalf("failed to set up hosted cluster: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Second)
	defer func() {
		cancel()
	}()
	signal.NotifyContext(ctx, os.Interrupt)
	<-ctx.Done()
}
