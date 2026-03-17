//go:build e2ev2 && backuprestore

package backuprestore

import (
	"context"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	"golang.org/x/sync/errgroup"
)

// Prober is the interface for a prober.
type Prober interface {
	// Stop terminates the prober.
	Stop() error
}

type prober struct {
	errGrp *errgroup.Group
	cancel context.CancelFunc
}

// prober implements Prober
var _ Prober = (*prober)(nil)

// Stop implements Prober
func (p *prober) Stop() error {
	// Stop all probing.
	p.cancel()

	return p.errGrp.Wait()
}

// ProberManager is the interface for spawning probers.
type ProberManager interface {
	// The ProberManager should expose a way to collectively reason about spawned
	// probes as a sort of aggregating Prober.
	Prober

	// Spawn creates a new Prober. The function passed as argument will be
	// executed in a separate goroutine repeatedly until the context is cancelled.
	// It can call verification functions from the Gomega library such as Expect or Fail,
	// or return an error. The error will be returned by the ProberManager.Stop() method.
	// Using functions from Gomega library such as Expect or Fail will log the error inside
	// the Ginkgo subject node that is currently running (possibly not the one that
	// spawned the prober). Returning an error, on the other hand, will allow the error
	// to be propagated to the ProberManager.Stop() method.
	Spawn(func() error) Prober
}

type manager struct {
	m        sync.RWMutex
	interval time.Duration
	probes   []Prober
}

var _ ProberManager = (*manager)(nil)

// Spawn implements ProberManager. It spawns a new Prober, adds it to the manager pool and returns it.
func (m *manager) Spawn(f func() error) Prober {
	m.m.Lock()
	defer m.m.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	errGrp, ctx := errgroup.WithContext(ctx)

	p := &prober{
		errGrp: errGrp,
		cancel: cancel,
	}
	m.probes = append(m.probes, p)

	errGrp.Go(func() error {
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			// Allow early exit if the context was cancelled
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			fWithRecover := func() error {
				defer GinkgoRecover()
				return f()
			}
			if err := fWithRecover(); err != nil {
				return err
			}
			// Wait for the next tick and continue the loop
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
		}
	})
	return p
}

// Stop implements ProberManager. It stops all probers in the manager pool.
func (m *manager) Stop() error {
	m.m.Lock()
	defer m.m.Unlock()

	errGrp := errgroup.Group{}
	for _, prober := range m.probes {
		errGrp.Go(prober.Stop)
	}
	return errGrp.Wait()
}

// NewProberManager creates a new manager for probes.
func NewProberManager(interval time.Duration) ProberManager {
	if interval <= 0 {
		interval = time.Second
	}
	m := manager{
		interval: interval,
		probes:   make([]Prober, 0),
	}
	return &m
}
