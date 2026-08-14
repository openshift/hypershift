package etcdcli

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"time"

	"k8s.io/klog/v2"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// EtcdClientPool fulfills these requirements:
// * cache clients to avoid re-creating them all the time (TLS handshakes are expensive after all)
// * return an exclusively unused client, no other can acquire the same client at that time
// * health checking a client before using it (using list), return a new one if unhealthy and closing the old one
// * update endpoints, to be always up to date with the changes
// * return a used client to the pool, making it available to consume again
type EtcdClientPool struct {
	pool                 chan *clientv3.Client
	availableOpenClients chan int

	newFunc       func() (*clientv3.Client, error)
	endpointsFunc func() ([]string, error)
	healthFunc    func(*clientv3.Client) error
	closeFunc     func(*clientv3.Client) error
}

const retries = 3

// have some small linear retries of 2s * retry in order to fail gracefully
const linearRetryBaseSleep = 2 * time.Second

// that controls the channel size, which controls how many unused clients we are keeping in buffer
const maxNumCachedClients = 5

// that controls how many clients are being created, you need to have a free message in the availableOpenClients channel to create a client
// this protects etcd from being hit by too many clients at once, eg when it is down or recovering or hit by lots of QPS
const maxNumOpenClients = 10
const maxAcquireTime = 5 * time.Second

// Get returns a client that can be used exclusively by the caller,
// the caller must not close the client but return it using Return.
// This is intentionally not a fast operation, Get will ensure the client returned will be healthy and retries on errors.
// If no client is available, this method will block intentionally to protect etcd from being overwhelmed by too many clients at once.
func (p *EtcdClientPool) Get() (*clientv3.Client, error) {
	desiredEndpoints, err := p.endpointsFunc()
	if err != nil {
		return nil, fmt.Errorf("getting cache client could not retrieve endpoints: %w", err)
	}

	// retrying this a few times until the caller gets a healthy client
	for i := 0; i < retries; i++ {
		if i != 0 {
			time.Sleep(linearRetryBaseSleep * time.Duration(i))
		}

		var client *clientv3.Client
		select {
		case client = <-p.pool:
		default:
			// blocks the creation when there are too many clients, after timeout we reject the request immediately without retry
			select {
			case <-p.availableOpenClients:
			case <-time.After(maxAcquireTime):
				return nil, fmt.Errorf("too many active cache clients, rejecting to create new one")
			}

			klog.Infof("creating a new cached client")
			c, err := p.newFunc()
			if err != nil {
				klog.Warningf("could not create a new cached client after %d tries, trying again. Err: %v", i, err)
				returnClosedClient(p.availableOpenClients)
				continue
			}

			client = c
		}

		// we're sorting as reflect.DeepEqual is depending on order
		sort.Strings(desiredEndpoints)
		currentEndpoints := client.Endpoints()
		// client returns a defensive copy, so should be fine to sort in-place
		sort.Strings(currentEndpoints)
		if !reflect.DeepEqual(desiredEndpoints, currentEndpoints) {
			klog.Warningf("cached client detected change in endpoints [%s] vs. [%s]", currentEndpoints, desiredEndpoints)
			// normally we could just set the endpoints directly, but this allows us to add some useful logging
			client.SetEndpoints(desiredEndpoints...)
		}

		err = p.healthFunc(client)
		if err != nil {
			klog.Warningf("cached client considered unhealthy after %d tries, trying again. Err: %v", i, err)
			// try to close the broken client and return the client to the pool
			returnClosedClient(p.availableOpenClients)
			err = p.closeFunc(client)
			if err != nil {
				klog.Errorf("could not close unhealthy cache client: %v", err)
			}
			continue
		}

		return client, nil
	}

	return nil, fmt.Errorf("giving up getting a cached client after %d tries", retries)
}

// Return will make the given client available for other callers through Get again.
// When the underlying pool is filled it will close the client instead of waiting for a free spot.
func (p *EtcdClientPool) Return(client *clientv3.Client) {
	if client == nil {
		return
	}

	select {
	case p.pool <- client:
	default:
		returnClosedClient(p.availableOpenClients)
		err := p.closeFunc(client)
		if err != nil {
			klog.Errorf("failed to close extra etcd client which is not being re-added in the client pool: %v", err)
		}
	}
}

// returnClosedClient will attempt to return a client to the channel, but will not block when the channel is at capacity
func returnClosedClient(channel chan int) {
	select {
	case channel <- 1:
	default:
	}
}

func NewDefaultEtcdClientPool(newFunc func() (*clientv3.Client, error), endpointsFunc func() ([]string, error)) *EtcdClientPool {
	healthFunc := func(client *clientv3.Client) error {
		if client == nil {
			return fmt.Errorf("cached client was nil")
		}
		ctx, cancel := context.WithTimeout(context.Background(), DefaultClientTimeout)
		defer cancel()
		_, err := client.MemberList(ctx)
		if err != nil {
			if clientv3.IsConnCanceled(err) {
				return fmt.Errorf("cache client health connection was canceled: %w", err)
			}
			return fmt.Errorf("error during cache client health connection check: %w", err)
		}
		return nil
	}

	closeFunc := func(client *clientv3.Client) error {
		if client == nil {
			return nil
		}
		klog.Infof("closing cached client")
		return client.Close()
	}

	return NewEtcdClientPool(newFunc, endpointsFunc, healthFunc, closeFunc)
}

func NewEtcdClientPool(
	newFunc func() (*clientv3.Client, error),
	endpointsFunc func() ([]string, error),
	healthFunc func(*clientv3.Client) error,
	closeFunc func(*clientv3.Client) error) *EtcdClientPool {

	// pre-populate clients for client creation
	availableOpenClients := make(chan int, maxNumOpenClients)
	for i := 0; i < maxNumOpenClients; i++ {
		availableOpenClients <- i
	}

	return &EtcdClientPool{
		pool:                 make(chan *clientv3.Client, maxNumCachedClients),
		availableOpenClients: availableOpenClients,
		newFunc:              newFunc,
		endpointsFunc:        endpointsFunc,
		healthFunc:           healthFunc,
		closeFunc:            closeFunc,
	}
}
