package gcp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	gcpinfra "github.com/openshift/hypershift/cmd/infra/gcp"
	hyperapi "github.com/openshift/hypershift/support/api"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestResolveDestroyOptions(t *testing.T) {
	const otherAccount = "other@openshift-gcp.iam.gserviceaccount.com"
	for _, tc := range []struct {
		name   string
		mutate func(*hyperv1.GCPPlatformSpec)
		err    string
	}{
		{"When provisioning IDs differ, it should resolve complete independent options", func(*hyperv1.GCPPlatformSpec) {}, ""},
		{"When the network is missing, it should reject cleanup", func(p *hyperv1.GCPPlatformSpec) { p.NetworkConfig.Network.Name = "" }, "network name"},
		{"When the network is custom, it should reject cleanup", func(p *hyperv1.GCPPlatformSpec) { p.NetworkConfig.Network.Name = "shared-vpc" }, "network name"},
		{"When the network prefix is empty, it should reject cleanup", func(p *hyperv1.GCPPlatformSpec) { p.NetworkConfig.Network.Name = "-network" }, "network name"},
		{"When the PSC subnet differs, it should reject cleanup", func(p *hyperv1.GCPPlatformSpec) { p.NetworkConfig.PrivateServiceConnectSubnet.Name = "other-subnet" }, "PSC subnet"},
		{"When the project is missing, it should reject cleanup", func(p *hyperv1.GCPPlatformSpec) { p.Project = "" }, "--project-id"},
		{"When the region is missing, it should reject cleanup", func(p *hyperv1.GCPPlatformSpec) { p.Region = "" }, "--region"},
		{"When the pool is custom, it should reject cleanup", func(p *hyperv1.GCPPlatformSpec) { p.WorkloadIdentity.PoolID = "shared-pool" }, "pool and provider"},
		{"When the pool is missing, it should reject cleanup", func(p *hyperv1.GCPPlatformSpec) { p.WorkloadIdentity.PoolID = "" }, "pool and provider"},
		{"When the pool prefix is empty, it should reject cleanup", func(p *hyperv1.GCPPlatformSpec) { p.WorkloadIdentity.PoolID = "-wi-pool" }, "pool and provider"},
		{"When the provider differs, it should reject cleanup", func(p *hyperv1.GCPPlatformSpec) { p.WorkloadIdentity.ProviderID = "other-k8s-provider" }, "pool and provider"},
		{"When the nodepool-mgmt account differs, it should reject cleanup", func(p *hyperv1.GCPPlatformSpec) { p.WorkloadIdentity.ServiceAccountsEmails.NodePool = otherAccount }, "service account for nodepool-mgmt"},
		{"When the ctrlplane-op account differs, it should reject cleanup", func(p *hyperv1.GCPPlatformSpec) { p.WorkloadIdentity.ServiceAccountsEmails.ControlPlane = otherAccount }, "service account for ctrlplane-op"},
		{"When the cloud-controller account differs, it should reject cleanup", func(p *hyperv1.GCPPlatformSpec) {
			p.WorkloadIdentity.ServiceAccountsEmails.CloudController = otherAccount
		}, "service account for cloud-controller"},
		{"When the gcp-pd-csi account differs, it should reject cleanup", func(p *hyperv1.GCPPlatformSpec) { p.WorkloadIdentity.ServiceAccountsEmails.Storage = otherAccount }, "service account for gcp-pd-csi"},
		{"When the image-registry account differs, it should reject cleanup", func(p *hyperv1.GCPPlatformSpec) {
			p.WorkloadIdentity.ServiceAccountsEmails.ImageRegistry = otherAccount
		}, "service account for image-registry"},
		{"When the cloud-network account differs, it should reject cleanup", func(p *hyperv1.GCPPlatformSpec) { p.WorkloadIdentity.ServiceAccountsEmails.Network = otherAccount }, "service account for cloud-network"},
		{"When multiple accounts differ, it should report the first in a stable order", func(p *hyperv1.GCPPlatformSpec) {
			p.WorkloadIdentity.ServiceAccountsEmails = hyperv1.GCPServiceAccountsEmails{}
		}, "service account for nodepool-mgmt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			p := destroyTestPlatform()
			tc.mutate(p)
			infra, iam, err := resolveDestroyOptions(p)
			if tc.err != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.err)))
				g.Expect(infra).To(BeNil())
				g.Expect(iam).To(BeNil())
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(infra).To(Equal(&gcpinfra.DestroyInfraOptions{ProjectID: p.Project, Region: p.Region, InfraID: "network-a"}))
			g.Expect(iam).To(Equal(&gcpinfra.DestroyIAMOptions{ProjectID: p.Project, InfraID: "iam-b"}))
		})
	}
}

func TestNewDestroyCommand(t *testing.T) {
	g := NewGomegaWithT(t)
	opts := &core.DestroyOptions{}
	cmd := NewDestroyCommand(opts)
	g.Expect(opts.GCPPlatform.PreserveInfra).To(BeFalse())
	g.Expect(cmd.Flags().Set("preserve-infra", "true")).To(Succeed())
	g.Expect(opts.GCPPlatform.PreserveInfra).To(BeTrue())
}

const (
	destroyTestNetworkPath = "/compute/v1/projects/openshift-gcp/global/networks/network-a-network"
	destroyTestNetworkLink = "https://www.googleapis.com" + destroyTestNetworkPath
)

type destroyClusterTestCase struct {
	name                                            string
	missing, wrongPlatform, missingGCP, invalidRefs bool
	preserve, force, foreignProject, unowned        bool
	cleanup                                         bool
	networkStatus, projectStatus                    int
	cleanupNetworkStatus                            int
	unownedDuringCleanup                            bool
	foreignChild                                    string
	foreignChildDuringCleanup                       bool
	infraFailure, iamFailure                        bool
	preserveOnRetry                                 bool
	err                                             string
}

func TestDestroyCluster(t *testing.T) {
	for _, tc := range []destroyClusterTestCase{
		{name: "When the cluster is missing, it should explain orphan cleanup", missing: true, err: "orphan cleanup"},
		{name: "When the platform differs, it should reject cleanup", wrongPlatform: true, err: "not a GCP cluster"},
		{name: "When GCP settings are absent, it should reject cleanup", missingGCP: true, err: "not a GCP cluster"},
		{name: "When references are invalid, it should keep the cluster", invalidRefs: true, err: "--preserve-infra"},
		{name: "When force is set with invalid references, it should keep the cluster", invalidRefs: true, force: true, err: "cannot resolve"},
		{name: "When infrastructure is preserved, it should bypass cloud validation", invalidRefs: true, preserve: true},
		{name: "When resources are valid, it should clean up using their original IDs", cleanup: true},
		{name: "When cloud resources are already absent, it should finish cleanup", networkStatus: http.StatusNotFound, cleanup: true},
		{name: "When the VPC disappears during teardown, it should skip networking and clean up IAM", cleanupNetworkStatus: http.StatusNotFound, cleanup: true},
		{name: "When VPC validation is forbidden after teardown, it should attempt IAM and allow retry", cleanupNetworkStatus: http.StatusForbidden, cleanup: true, err: "cannot validate GCP networking"},
		{name: "When the VPC description changes during teardown, it should skip networking and allow retry", unownedDuringCleanup: true, cleanup: true, err: "not recognized"},
		{name: "When the router belongs to another VPC, it should keep the cluster", foreignChild: "routers", err: "does not belong"},
		{name: "When the subnet belongs to another VPC, it should keep the cluster", foreignChild: "subnetworks", err: "does not belong"},
		{name: "When the firewall belongs to another VPC, it should keep the cluster", foreignChild: "firewalls", err: "does not belong"},
		{name: "When a child VPC reference changes during teardown, it should skip networking and allow retry", foreignChild: "routers", foreignChildDuringCleanup: true, cleanup: true, err: "does not belong"},
		{name: "When the network is unrecognized, it should keep the cluster", unowned: true, err: "not recognized"},
		{name: "When network lookup is forbidden, it should keep the cluster", networkStatus: http.StatusForbidden, err: "cannot validate"},
		{name: "When project lookup is forbidden, it should keep the cluster", projectStatus: http.StatusForbidden, err: "project number"},
		{name: "When WIF belongs to another project, it should keep the cluster", foreignProject: true, err: "must belong to project"},
		{name: "When infrastructure cleanup fails, it should attempt IAM and allow retry", infraFailure: true, cleanup: true, err: "failed to destroy GCP infrastructure"},
		{name: "When IAM cleanup fails, it should retain references for retry", iamFailure: true, cleanup: true, err: "failed to destroy GCP IAM"},
		{name: "When infrastructure cleanup fails, it should allow retry preserving infrastructure", infraFailure: true, preserveOnRetry: true, cleanup: true, err: "failed to destroy GCP infrastructure"},
		{name: "When IAM cleanup fails, it should allow retry preserving infrastructure", iamFailure: true, preserveOnRetry: true, cleanup: true, err: "failed to destroy GCP IAM"},
		{name: "When both cleanups fail, it should report both errors", infraFailure: true, iamFailure: true, cleanup: true, err: "failed to destroy GCP infrastructure"},
		{name: "When force overrides cleanup failure, it should remove the cluster", infraFailure: true, force: true, cleanup: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "gcp-demo", Namespace: "clusters"},
				Spec:       hyperv1.HostedClusterSpec{InfraID: "cluster-c", Platform: hyperv1.PlatformSpec{Type: hyperv1.GCPPlatform, GCP: destroyTestPlatform()}},
			}
			if tc.wrongPlatform {
				hc.Spec.Platform.Type = hyperv1.AWSPlatform
			}
			if tc.invalidRefs {
				hc.Spec.Platform.GCP.NetworkConfig.Network.Name = "shared-vpc"
			}
			if tc.missingGCP {
				hc.Spec.Platform.GCP = nil
			}
			if tc.foreignProject {
				hc.Spec.Platform.GCP.WorkloadIdentity.ProjectNumber = "999999999999"
			}
			builder := fake.NewClientBuilder().WithScheme(hyperapi.Scheme)
			if !tc.missing {
				builder.WithObjects(hc)
			}
			c := builder.Build()
			opts := &core.DestroyOptions{Name: hc.Name, Namespace: hc.Namespace, ClusterGracePeriod: time.Second, ForceDestroy: tc.force}
			opts.GCPPlatform.PreserveInfra = tc.preserve
			api := &destroyTestAPI{tc: tc}
			fakeGoogleAPI(t, api.handler(t, c, hc))
			err := DestroyCluster(t.Context(), opts, c)
			if tc.err != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.err)), "initial destroy failure")
			} else {
				g.Expect(err).NotTo(HaveOccurred(), "initial destroy")
			}
			if tc.iamFailure {
				g.Expect(err).To(MatchError(ContainSubstring("failed to destroy GCP IAM")))
			}
			current := &hyperv1.HostedCluster{}
			getErr := c.Get(t.Context(), client.ObjectKeyFromObject(hc), current)
			if tc.err == "" || tc.missing {
				g.Expect(apierrors.IsNotFound(getErr)).To(BeTrue(), "cluster removed")
			} else {
				g.Expect(getErr).NotTo(HaveOccurred(), "cluster retained for retry")
			}
			api.mu.Lock()
			calls := append([]string(nil), api.mutations...)
			requestCount := api.requests
			childRequests := api.networkChildRequests
			api.mu.Unlock()
			if tc.preserve {
				g.Expect(requestCount).To(BeZero())
			}
			if !tc.cleanup {
				g.Expect(calls).To(BeEmpty())
				if !tc.missing && !tc.preserve {
					g.Expect(current.DeletionTimestamp.IsZero()).To(BeTrue())
				}
				return
			}
			g.Expect(calls).NotTo(BeEmpty())
			g.Expect(opts.InfraID).To(Equal("cluster-c"))
			g.Expect(strings.Join(calls, "\n")).NotTo(ContainSubstring("cluster-c"))
			g.Expect(calls).To(
				ContainElement("DELETE /v1/projects/openshift-gcp/serviceAccounts/iam-b-nodepool-mgmt@openshift-gcp.iam.gserviceaccount.com"),
				"attempt IAM cleanup after networking failure")
			networkSkipped := tc.networkStatus == http.StatusNotFound || tc.cleanupNetworkStatus != 0 || tc.unownedDuringCleanup || tc.foreignChildDuringCleanup
			if networkSkipped {
				g.Expect(strings.Join(calls, "\n")).NotTo(ContainSubstring("/compute/"), "no unvalidated network mutations")
				if tc.networkStatus == http.StatusNotFound {
					g.Expect(childRequests).To(BeZero(), "no child lookups for missing VPC")
				}
			}
			if !tc.infraFailure && !tc.iamFailure {
				networkCalls := 5
				if networkSkipped {
					networkCalls = 0
				}
				g.Expect(calls).To(HaveLen(networkCalls + 8))
				if !networkSkipped {
					g.Expect(calls[networkCalls-1]).To(Equal("DELETE " + destroyTestNetworkPath))
					g.Expect(calls).To(ContainElement("DELETE /compute/v1/projects/openshift-gcp/regions/us-central1/subnetworks/network-a-subnet"))
					g.Expect(calls).To(ContainElement("DELETE /compute/v1/projects/openshift-gcp/global/firewalls/network-a-allow-kubelet"))
				}
				g.Expect(calls[len(calls)-1]).To(Equal("DELETE /v1/projects/openshift-gcp/locations/global/workloadIdentityPools/iam-b-wi-pool"))
			}
			if tc.err != "" {
				g.Expect(current.DeletionTimestamp.IsZero()).To(BeFalse())
				g.Expect(current.Finalizers).To(ContainElement("openshift.io/destroy-cluster"), "retain finalizer after failure")
				api.mu.Lock()
				api.retrying = true
				api.mu.Unlock()
				opts.GCPPlatform.PreserveInfra = tc.preserveOnRetry
				g.Expect(DestroyCluster(t.Context(), opts, c)).To(Succeed(), "retry cluster destruction")
				g.Expect(apierrors.IsNotFound(c.Get(t.Context(), client.ObjectKeyFromObject(hc), current))).To(BeTrue(), "cluster removed on retry")
				if tc.iamFailure && !tc.infraFailure {
					api.mu.Lock()
					childRequestsAfterRetry := api.networkChildRequests
					api.mu.Unlock()
					g.Expect(childRequestsAfterRetry).To(Equal(childRequests), "skip deleted VPC on retry")
				}
				if tc.preserveOnRetry {
					api.mu.Lock()
					requestsAfterRetry := api.requests
					api.mu.Unlock()
					g.Expect(requestsAfterRetry).To(Equal(requestCount), "no cloud calls when preserving on retry")
				}
			}
		})
	}
}

type destroyTestAPI struct {
	mu                       sync.Mutex
	tc                       destroyClusterTestCase
	mutations                []string
	requests, networkLookups int
	networkChildRequests     int
	networkDeleted, retrying bool
}

func (api *destroyTestAPI) handler(t *testing.T, c client.Client, hc *hyperv1.HostedCluster) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		defer api.mu.Unlock()
		api.requests++
		tc := api.tc
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		if strings.Contains(path, "/compute/") && !(path == destroyTestNetworkPath && r.Method == http.MethodGet) {
			api.networkChildRequests++
		}
		body := `{}`
		switch {
		case path == "/token":
			body = `{"access_token":"unit-test","token_type":"Bearer","expires_in":3600}`
		case path == destroyTestNetworkPath && r.Method == http.MethodGet:
			response, status := api.networkResponse()
			if status != 0 {
				http.Error(w, "network lookup failed", status)
				return
			}
			body = response
		case path == "/v1/projects/openshift-gcp" && r.Method == http.MethodGet:
			if tc.projectStatus != 0 {
				http.Error(w, "project lookup failed", tc.projectStatus)
				return
			}
			body = `{"projectNumber":"123456789012"}`
		case r.Method == http.MethodDelete || r.Method == http.MethodPatch:
			api.mutations = append(api.mutations, r.Method+" "+path)
			current := &hyperv1.HostedCluster{}
			if err := c.Get(r.Context(), client.ObjectKeyFromObject(hc), current); err != nil || current.DeletionTimestamp.IsZero() {
				t.Errorf("cloud cleanup started before cluster teardown: %v", err)
			}
			if !api.retrying && ((tc.infraFailure && strings.Contains(path, "/compute/")) || (tc.iamFailure && strings.Contains(path, "/serviceAccounts/"))) {
				http.Error(w, "cleanup forbidden", http.StatusForbidden)
				return
			}
			if tc.networkStatus == http.StatusNotFound {
				http.NotFound(w, r)
				return
			}
			if path == destroyTestNetworkPath && r.Method == http.MethodDelete {
				api.networkDeleted = true
			}
			body = `{"name":"projects/openshift-gcp/locations/global/workloadIdentityPools/iam-b-wi-pool/operations/cleanup"}`
		case strings.Contains(path, "/operations/"):
			body = `{"status":"DONE","done":true}`
		case r.Method == http.MethodGet && (strings.Contains(path, "/routers/") || strings.Contains(path, "/subnetworks/") || strings.Contains(path, "/firewalls/")):
			body = api.childResponse(path)
		case strings.HasSuffix(path, ":getIamPolicy"):
		default:
			t.Errorf("unexpected Google API request: %s %s", r.Method, path)
		}
		if _, err := fmt.Fprint(w, body); err != nil {
			t.Errorf("failed to write fake Google API response: %v", err)
		}
	}
}

func (api *destroyTestAPI) networkResponse() (string, int) {
	api.networkLookups++
	status := api.tc.networkStatus
	if api.networkDeleted {
		status = http.StatusNotFound
	} else if !api.retrying && api.networkLookups > 1 && api.tc.cleanupNetworkStatus != 0 {
		status = api.tc.cleanupNetworkStatus
	}
	description := "HyperShift VPC for cluster network-a"
	if api.tc.unowned || (!api.retrying && api.networkLookups > 1 && api.tc.unownedDuringCleanup) {
		description = "Shared network"
	}
	return fmt.Sprintf(`{"name":"network-a-network","description":%q,"selfLink":%q}`, description, destroyTestNetworkLink), status
}

func (api *destroyTestAPI) childResponse(path string) string {
	network := destroyTestNetworkLink
	if api.tc.foreignChild != "" && strings.Contains(path, "/"+api.tc.foreignChild+"/") && !api.retrying && (!api.tc.foreignChildDuringCleanup || api.networkLookups > 1) {
		network += "-other"
	}
	name := path[strings.LastIndex(path, "/")+1:]
	return fmt.Sprintf(`{"name":%q,"network":%q,"nats":[{"name":"network-a-nat"}]}`, name, network)
}

func destroyTestPlatform() *hyperv1.GCPPlatformSpec {
	return &hyperv1.GCPPlatformSpec{
		Project: "openshift-gcp", Region: "us-central1",
		NetworkConfig: hyperv1.GCPNetworkConfig{
			Network: hyperv1.GCPResourceReference{Name: "network-a-network"}, PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{Name: "network-a-subnet"},
		},
		WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
			ProjectNumber: "123456789012", PoolID: "iam-b-wi-pool", ProviderID: "iam-b-k8s-provider",
			ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
				NodePool: "iam-b-nodepool-mgmt@openshift-gcp.iam.gserviceaccount.com", ControlPlane: "iam-b-ctrlplane-op@openshift-gcp.iam.gserviceaccount.com",
				CloudController: "iam-b-cloud-controller@openshift-gcp.iam.gserviceaccount.com", Storage: "iam-b-gcp-pd-csi@openshift-gcp.iam.gserviceaccount.com",
				ImageRegistry: "iam-b-image-registry@openshift-gcp.iam.gserviceaccount.com", Network: "iam-b-cloud-network@openshift-gcp.iam.gserviceaccount.com",
			},
		},
	}
}

// fakeGoogleAPI redirects SDK and token requests locally. Tests must run serially:
// SDK clients clone the replaced http.DefaultTransport.
func fakeGoogleAPI(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	credentials := filepath.Join(t.TempDir(), "adc.json")
	NewGomegaWithT(t).Expect(os.WriteFile(credentials, []byte(`{"type":"authorized_user","client_id":"unit-test","client_secret":"unit-test","refresh_token":"unit-test"}`), 0600)).To(Succeed())
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credentials)
	t.Setenv("GOOGLE_API_USE_MTLS_ENDPOINT", "never")
	t.Setenv("GOOGLE_API_USE_CLIENT_CERTIFICATE", "false")
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	original := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() {
		http.DefaultTransport = original
		transport.CloseIdleConnections()
	})
}
