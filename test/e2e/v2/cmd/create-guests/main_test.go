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

package main

import (
	"context"
	"net"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestNewMgmtClient(t *testing.T) {
	t.Run("When discovery cannot connect, it should stop after the discovery timeout", func(t *testing.T) {
		client, err := newMgmtClientForConfigWithDiscoveryTimeout(&rest.Config{
			Host: "https://api.example.com",
			Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}, 20*time.Millisecond)
		if err != nil {
			t.Fatalf("failed to create management client: %v", err)
		}

		start := time.Now()
		err = client.Get(t.Context(), crclient.ObjectKey{Name: "test"}, &corev1.Namespace{})
		elapsed := time.Since(start)
		if err == nil {
			t.Fatal("expected discovery to fail")
		}
		if elapsed > time.Second {
			t.Fatalf("expected discovery to stop within 1s, took %v: %v", elapsed, err)
		}
	})
}
