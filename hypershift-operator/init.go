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
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/capabilities"

	configapi "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

func NewInitCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Prepares pre-requisites that are required by the Hypershift Operator",
		Long:  "Prepares pre-requisites such as merging additional CA trust bundles that are required by the Hypershift Operator",
	}

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(ctrl.SetupSignalHandler())
		defer cancel()

		if err := runInit(ctx, ctrl.Log.WithName("init")); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}

	return cmd
}

func runInit(ctx context.Context, log logr.Logger) error {
	log.Info("Initializing environment for Hypershift Operator")
	client, err := util.GetClient()
	if err != nil {
		return err
	}
	config, err := ctrl.GetConfig()
	if err != nil {
		return err
	}
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return fmt.Errorf("unable to get discovery client: %w", err)
	}
	mgmtClusterCaps, err := capabilities.DetectManagementClusterCapabilities(discoveryClient)
	if err != nil {
		return fmt.Errorf("unable to detect management cluster capabilities: %w", err)
	}

	trustedCABundle := new(bytes.Buffer)
	var ocmTrustedCABundle []byte
	if _, err := os.Stat("/var/run/ca-trust/tls-ca-bundle.pem"); err == nil {
		ocmTrustedCABundle, err = os.ReadFile("/var/run/ca-trust/tls-ca-bundle.pem")
		if err != nil {
			return fmt.Errorf("unable to read ocm CA trust bundle file: %w", err)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		ocmTrustedCABundle, err = os.ReadFile("/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem")
		if err != nil {
			return fmt.Errorf("unable to read ocm CA trust bundle file: %w", err)
		}
	} else {
		return err
	}

	if _, err := trustedCABundle.Write(ocmTrustedCABundle); err != nil {
		return fmt.Errorf("unable to write ocm CA trust bundle to buffer: %w", err)
	}

	if mgmtClusterCaps.Has(capabilities.CapabilityImage) {
		// Adds user-specified image registry CA bundle to the full CA trust bundle if it exists
		imageRegistryCABundle, err := getImageRegistryCABundle(ctx, client)
		if err != nil {
			return fmt.Errorf("unable to get CA bundle for image mirror registries: %w", err)
		}
		if imageRegistryCABundle != nil {
			if _, err := trustedCABundle.Write(imageRegistryCABundle.Bytes()); err != nil {
				return fmt.Errorf("unable to write image registry CA to buffer: %w", err)
			}
		}
	}

	if err := os.WriteFile("/trust-bundle/tls-ca-bundle.pem", trustedCABundle.Bytes(), 0644); err != nil {
		return err
	}

	log.Info("Finished initializing environment for Hypershift Operator")
	return nil
}

// getImageRegistryCABundle retrieves the image registry CAs listed under image.config.openshift.io
func getImageRegistryCABundle(ctx context.Context, client crclient.Client) (*bytes.Buffer, error) {
	img := &configapi.Image{}
	if err := client.Get(ctx, types.NamespacedName{Name: "cluster"}, img); err != nil {
		return nil, err
	}
	if img.Spec.AdditionalTrustedCA.Name == "" {
		return nil, nil
	}
	configmap := &corev1.ConfigMap{}
	if err := client.Get(ctx, types.NamespacedName{Name: img.Spec.AdditionalTrustedCA.Name, Namespace: "openshift-config"}, configmap); err != nil {
		return nil, err
	}
	if configmap.Data != nil {
		var buf bytes.Buffer
		for _, crt := range configmap.Data {
			// Added a newline character to the end of each certificate to avoid bad concatenation
			// of certificates in the buffer using the UI.
			buf.WriteString(fmt.Sprintf("%s\n", crt))
		}
		if buf.Len() > 0 {
			return &buf, nil
		}
	}
	return nil, nil
}
