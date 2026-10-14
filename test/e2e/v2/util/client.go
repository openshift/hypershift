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
	"time"

	hyperapi "github.com/openshift/hypershift/support/api"

	"k8s.io/client-go/rest"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// GetConfig returns a REST config for the management cluster with client-side
// throttling disabled and a generous request timeout. The API server's Priority
// and Fairness provides server-side flow control; client-side limiting only
// produces misleading errors when test contexts expire.
func GetConfig() (*rest.Config, error) {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, err
	}
	cfg.QPS = -1
	cfg.Burst = -1
	cfg.Timeout = 5 * time.Minute
	return cfg, nil
}

// GetClient returns a controller-runtime client for the management cluster.
func GetClient() (crclient.Client, error) {
	config, err := GetConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes config: %w", err)
	}
	client, err := crclient.New(config, crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes client: %w", err)
	}
	return client, nil
}
