// Package mock serves the etcdctl replies for one reset-member script attempt.
package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
)

const ServerEnv = "FAKE_ETCDCTL_SERVER"

type Reply struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type Exchange struct {
	Args  []string
	Reply Reply
}

// Fails returns a copy with an unsuccessful exit status. A command can emit
// partial stdout before it fails, so the configured stdout is retained.
func (e Exchange) Fails(message string) Exchange {
	e.Reply.Stderr = message + "\n"
	e.Reply.ExitCode = 1
	return e
}

// Start matches requests in order until ctx is canceled or finish is called.
// Finish reports cancellation or the first mismatch or missing call.
func Start(ctx context.Context, expected []Exchange) (url string, finish func() error) {
	var mu sync.Mutex
	var next int
	var firstErr error
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		var args []string
		if firstErr == nil {
			err := json.NewDecoder(r.Body).Decode(&args)
			switch {
			case err != nil:
				firstErr = fmt.Errorf("decoding etcdctl arguments: %w", err)
			case next == len(expected):
				firstErr = fmt.Errorf("unexpected extra etcdctl call %q", args)
			case !slices.Equal(args, expected[next].Args):
				firstErr = fmt.Errorf("etcdctl call %d: got %q, want %q", next+1, args, expected[next].Args)
			}
		}
		if firstErr != nil {
			http.Error(w, firstErr.Error(), http.StatusConflict)
			return
		}
		if err := json.NewEncoder(w).Encode(expected[next].Reply); err != nil {
			firstErr = fmt.Errorf("sending etcdctl reply: %w", err)
			return
		}
		next++
	}))
	server.Config.BaseContext = func(net.Listener) context.Context { return ctx }
	server.Start()
	stop := context.AfterFunc(ctx, func() {
		// Closing active connections unblocks handlers waiting for a request body.
		_ = server.Config.Close()
	})

	return server.URL, func() error {
		server.Close() // Wait for all handlers before reading the final state.
		stop()
		if err := ctx.Err(); err != nil {
			return err
		}
		if firstErr != nil {
			return firstErr
		}
		if next < len(expected) {
			return fmt.Errorf("attempt ended before expected etcdctl %s (call %d of %d)",
				strings.Join(expected[next].Args, " "), next+1, len(expected))
		}
		return nil
	}
}
