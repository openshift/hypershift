//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"fmt"
	"net"
	"net/http"
	"time"

	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/client-go/transport"
)

const (
	kasDialTimeout   = 30 * time.Second
	kasKeepAlive     = 30 * time.Second
	kasMaxIdleConns  = 100
	kasIdleConnLimit = 10
)

// NewKASTransport creates the base HTTP transport used by management KAS
// clients and health checks. It preserves client-go's proxy, custom dialer,
// compression, and HTTP/2 defaults while using a TCP keep-alive suitable for
// Azure load balancers.
func NewKASTransport(config *transport.Config) (*http.Transport, error) {
	if config == nil {
		return nil, fmt.Errorf("transport config must not be nil")
	}

	tlsConfig, err := transport.TLSConfigFor(config)
	if err != nil {
		return nil, err
	}

	dialContext := (&net.Dialer{
		Timeout:   kasDialTimeout,
		KeepAlive: kasKeepAlive,
	}).DialContext
	if config.DialHolder != nil && config.DialHolder.Dial != nil {
		dialContext = config.DialHolder.Dial
	}

	baseTransport := &http.Transport{
		Proxy:                 config.Proxy,
		DialContext:           dialContext,
		TLSClientConfig:       tlsConfig,
		DisableCompression:    config.DisableCompression,
		MaxIdleConns:          kasMaxIdleConns,
		MaxIdleConnsPerHost:   kasIdleConnLimit,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	// This supplies http.ProxyFromEnvironment (including CIDR-aware NO_PROXY)
	// when config.Proxy is nil and configures HTTP/2 idle connection probes.
	return utilnet.SetTransportDefaults(baseTransport), nil
}
