package etcdcli

import (
	"time"
)

type ClientOptions struct {
	dialTimeout time.Duration
}

func newClientOpts(opts ...ClientOption) (*ClientOptions, error) {
	clientOpts := &ClientOptions{
		dialTimeout: DefaultDialTimeout,
	}
	clientOpts.applyOpts(opts)
	return clientOpts, nil
}

func (co *ClientOptions) applyOpts(opts []ClientOption) {
	for _, opt := range opts {
		opt(co)
	}
}

type ClientOption func(*ClientOptions)

func WithDialTimeout(timeout time.Duration) ClientOption {
	return func(co *ClientOptions) {
		co.dialTimeout = timeout
	}
}
