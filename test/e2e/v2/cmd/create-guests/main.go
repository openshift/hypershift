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

// create-guests creates HA HostedClusters for v2 e2e lifecycle tests.
// It shells out to the hypershift CLI to create an Azure cluster, waits
// for the HostedCluster to become Available, and writes the cluster name
// to SHARED_DIR for downstream CI steps.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(hyperv1.AddToScheme(scheme))
}

func main() {
	// Required flags.
	name := flag.String("name", "", "Name of the HostedCluster to create (required)")
	releaseImage := flag.String("release-image", "", "OCP release image (required)")
	azureCreds := flag.String("azure-creds", "", "Path to Azure credentials JSON (required)")
	pullSecret := flag.String("pull-secret", "", "Path to pull secret file (required)")
	baseDomain := flag.String("base-domain", "", "DNS base domain (required)")

	// Optional flags with defaults.
	namespace := flag.String("namespace", "clusters", "Namespace for the HostedCluster")
	location := flag.String("location", "centralus", "Azure region")
	cpAvailabilityPolicy := flag.String("control-plane-availability-policy", "HighlyAvailable", "Control plane availability policy")
	nodePoolReplicas := flag.Int("node-pool-replicas", 3, "Number of node pool replicas")
	sharedDir := flag.String("shared-dir", os.Getenv("SHARED_DIR"), "SHARED_DIR to write the cluster name file to")
	outputFile := flag.String("output-file", "cluster-name-upgrade", "Filename in SHARED_DIR to write the cluster name to")
	hypershiftBinary := flag.String("hypershift-binary", "hypershift", "Path to the hypershift CLI binary")
	waitTimeout := flag.Duration("wait-timeout", 45*time.Minute, "Timeout for waiting for the cluster to become Available")

	// Azure-specific optional flags.
	oidcIssuerURL := flag.String("oidc-issuer-url", "", "Azure OIDC issuer URL")
	saTokenIssuerPrivateKeyPath := flag.String("sa-token-issuer-private-key-path", "", "Path to the SA token issuer private key")
	workloadIdentitiesFile := flag.String("workload-identities-file", "", "Path to the workload identities JSON file")
	dnsZoneRGName := flag.String("dns-zone-rg-name", "", "DNS zone resource group name")
	assignSPRoles := flag.Bool("assign-service-principal-roles", true, "Assign service principal roles")
	generateSSH := flag.Bool("generate-ssh", true, "Generate SSH key")
	etcdStorageClass := flag.String("etcd-storage-class", "", "Etcd storage class")
	externalDNSDomain := flag.String("external-dns-domain", "", "External DNS domain")

	flag.Parse()

	if *name == "" || *releaseImage == "" || *azureCreds == "" || *pullSecret == "" || *baseDomain == "" {
		log.Fatal("--name, --release-image, --azure-creds, --pull-secret, and --base-domain are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *waitTimeout+10*time.Minute)
	defer cancel()

	if err := run(ctx, runConfig{
		name:                        *name,
		releaseImage:                *releaseImage,
		azureCreds:                  *azureCreds,
		pullSecret:                  *pullSecret,
		baseDomain:                  *baseDomain,
		namespace:                   *namespace,
		location:                    *location,
		cpAvailabilityPolicy:        *cpAvailabilityPolicy,
		nodePoolReplicas:            *nodePoolReplicas,
		sharedDir:                   *sharedDir,
		outputFile:                  *outputFile,
		hypershiftBinary:            *hypershiftBinary,
		waitTimeout:                 *waitTimeout,
		oidcIssuerURL:               *oidcIssuerURL,
		saTokenIssuerPrivateKeyPath: *saTokenIssuerPrivateKeyPath,
		workloadIdentitiesFile:      *workloadIdentitiesFile,
		dnsZoneRGName:               *dnsZoneRGName,
		assignSPRoles:               *assignSPRoles,
		generateSSH:                 *generateSSH,
		etcdStorageClass:            *etcdStorageClass,
		externalDNSDomain:           *externalDNSDomain,
	}); err != nil {
		log.Fatalf("Error: %v", err)
	}
}

type runConfig struct {
	name                        string
	releaseImage                string
	azureCreds                  string
	pullSecret                  string
	baseDomain                  string
	namespace                   string
	location                    string
	cpAvailabilityPolicy        string
	nodePoolReplicas            int
	sharedDir                   string
	outputFile                  string
	hypershiftBinary            string
	waitTimeout                 time.Duration
	oidcIssuerURL               string
	saTokenIssuerPrivateKeyPath string
	workloadIdentitiesFile      string
	dnsZoneRGName               string
	assignSPRoles               bool
	generateSSH                 bool
	etcdStorageClass            string
	externalDNSDomain           string
}

func run(ctx context.Context, cfg runConfig) error {
	args := buildCLIArgs(cfg)

	log.Printf("Creating HostedCluster %s/%s with hypershift CLI", cfg.namespace, cfg.name)
	log.Printf("Running: %s %v", cfg.hypershiftBinary, args)

	cmd := exec.CommandContext(ctx, cfg.hypershiftBinary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hypershift create cluster azure failed: %w", err)
	}

	log.Printf("Waiting for HostedCluster %s/%s to become Available (timeout: %s)", cfg.namespace, cfg.name, cfg.waitTimeout)
	if err := waitForClusterAvailable(ctx, cfg.namespace, cfg.name, cfg.waitTimeout); err != nil {
		return fmt.Errorf("waiting for HostedCluster to become Available: %w", err)
	}

	log.Printf("HostedCluster %s/%s is Available", cfg.namespace, cfg.name)

	if cfg.sharedDir != "" {
		outputPath := filepath.Join(cfg.sharedDir, cfg.outputFile)
		if err := os.WriteFile(outputPath, []byte(cfg.name), 0600); err != nil {
			return fmt.Errorf("writing cluster name to %s: %w", outputPath, err)
		}
		log.Printf("Wrote cluster name %q to %s", cfg.name, outputPath)
	}

	return nil
}

func buildCLIArgs(cfg runConfig) []string {
	args := []string{
		"create", "cluster", "azure",
		"--name=" + cfg.name,
		"--namespace=" + cfg.namespace,
		"--release-image=" + cfg.releaseImage,
		"--azure-creds=" + cfg.azureCreds,
		"--pull-secret=" + cfg.pullSecret,
		"--base-domain=" + cfg.baseDomain,
		"--location=" + cfg.location,
		"--control-plane-availability-policy=" + cfg.cpAvailabilityPolicy,
		"--node-pool-replicas=" + strconv.Itoa(cfg.nodePoolReplicas),
	}

	if cfg.assignSPRoles {
		args = append(args, "--assign-service-principal-roles=true")
	}
	if cfg.generateSSH {
		args = append(args, "--generate-ssh")
	}

	// Append optional flags only when provided.
	if cfg.oidcIssuerURL != "" {
		args = append(args, "--oidc-issuer-url="+cfg.oidcIssuerURL)
	}
	if cfg.saTokenIssuerPrivateKeyPath != "" {
		args = append(args, "--sa-token-issuer-private-key-path="+cfg.saTokenIssuerPrivateKeyPath)
	}
	if cfg.workloadIdentitiesFile != "" {
		args = append(args, "--workload-identities-file="+cfg.workloadIdentitiesFile)
	}
	if cfg.dnsZoneRGName != "" {
		args = append(args, "--dns-zone-rg-name="+cfg.dnsZoneRGName)
	}
	if cfg.etcdStorageClass != "" {
		args = append(args, "--etcd-storage-class="+cfg.etcdStorageClass)
	}
	if cfg.externalDNSDomain != "" {
		args = append(args, "--external-dns-domain="+cfg.externalDNSDomain)
	}

	return args
}

func waitForClusterAvailable(ctx context.Context, namespace, name string, timeout time.Duration) error {
	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("getting management cluster kubeconfig: %w", err)
	}
	mgmtClient, err := crclient.New(restConfig, crclient.Options{
		Scheme: scheme,
	})
	if err != nil {
		return fmt.Errorf("creating management cluster client: %w", err)
	}

	hc := &hyperv1.HostedCluster{}
	return wait.PollUntilContextTimeout(ctx, 15*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		if err := mgmtClient.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: name}, hc); err != nil {
			log.Printf("Waiting for HostedCluster %s/%s: %v", namespace, name, err)
			return false, nil
		}
		for _, cond := range hc.Status.Conditions {
			if cond.Type == string(hyperv1.HostedClusterAvailable) && cond.Status == metav1.ConditionTrue {
				return true, nil
			}
		}
		desiredImage := "<unknown>"
		if hc.Status.Version != nil {
			desiredImage = hc.Status.Version.Desired.Image
		}
		log.Printf("HostedCluster %s/%s not yet Available, current desired image: %s", namespace, name, desiredImage)
		return false, nil
	})
}
