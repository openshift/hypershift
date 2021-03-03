// +build e2e

package e2e

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"testing"
)

// GlobalTestContext should be used as the parent context for any test code, and will
// be cancelled if a SIGINT or SIGTERM is received.
var GlobalTestContext context.Context

func TestMain(m *testing.M) {
	ctx, cancel := context.WithCancel(context.Background())
	GlobalTestContext = ctx

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Printf("tests received shutdown signal and will be cancelled")
		cancel()
	}()
	flag.Parse()
	os.Exit(m.Run())
}
