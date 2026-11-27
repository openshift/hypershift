package util

import (
	"crypto/x509"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/certs"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	"github.com/blang/semver"
)

func TestAllowedCIDRsTargetService(t *testing.T) {
	const ns = "test-hcp"

	publicHC := func(platform hyperv1.PlatformType, svcType hyperv1.PublishingStrategyType) *hyperv1.HostedCluster {
		hc := &hyperv1.HostedCluster{
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{Type: platform},
				Services: []hyperv1.ServicePublishingStrategyMapping{{
					Service:                   hyperv1.APIServer,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: svcType},
				}},
			},
		}
		switch platform {
		case hyperv1.AWSPlatform:
			hc.Spec.Platform.AWS = ptr.To(hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public})
		case hyperv1.AzurePlatform:
			hc.Spec.Platform.Azure = ptr.To(hyperv1.AzurePlatformSpec{Topology: hyperv1.AzureTopologyPublic})
		}
		return hc
	}

	tests := []struct {
		name     string
		hc       *hyperv1.HostedCluster
		aroHCP   bool
		wantName string
		wantNil  bool
	}{
		{
			name:     "When Route strategy on AWS it should return the router service",
			hc:       publicHC(hyperv1.AWSPlatform, hyperv1.Route),
			wantName: "router",
		},
		{
			name:     "When Route strategy on Azure self-managed it should return the router service",
			hc:       publicHC(hyperv1.AzurePlatform, hyperv1.Route),
			wantName: "router",
		},
		{
			name:    "When Route strategy on ARO HCP it should return nil",
			hc:      publicHC(hyperv1.AzurePlatform, hyperv1.Route),
			aroHCP:  true,
			wantNil: true,
		},
		{
			name:     "When LoadBalancer strategy on Azure it should return the Azure LB service",
			hc:       publicHC(hyperv1.AzurePlatform, hyperv1.LoadBalancer),
			wantName: "kube-apiserverlb",
		},
		{
			name: "When LoadBalancer strategy with Azure management annotation it should return the Azure LB service",
			hc: func() *hyperv1.HostedCluster {
				hc := publicHC(hyperv1.NonePlatform, hyperv1.LoadBalancer)
				hc.Annotations = map[string]string{
					hyperv1.ManagementPlatformAnnotation: string(hyperv1.AzurePlatform),
				}
				return hc
			}(),
			wantName: "kube-apiserverlb",
		},
		{
			name:     "When LoadBalancer strategy on AWS it should return the KAS service",
			hc:       publicHC(hyperv1.AWSPlatform, hyperv1.LoadBalancer),
			wantName: "kube-apiserver",
		},
		{
			name: "When private Azure cluster it should return nil",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type:  hyperv1.AzurePlatform,
						Azure: ptr.To(hyperv1.AzurePlatformSpec{Topology: hyperv1.AzureTopologyPrivate}),
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{{
						Service:                   hyperv1.APIServer,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route},
					}},
				},
			},
			wantNil: true,
		},
		{
			name:    "When NodePort strategy it should return nil",
			hc:      publicHC(hyperv1.AWSPlatform, hyperv1.NodePort),
			wantNil: true,
		},
		{
			name: "When no APIServer strategy it should return nil",
			hc: func() *hyperv1.HostedCluster {
				hc := publicHC(hyperv1.AWSPlatform, hyperv1.Route)
				hc.Spec.Services = nil
				return hc
			}(),
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			if tc.aroHCP {
				azureutil.SetAsAroHCPTest(t)
			}
			svc := allowedCIDRsTargetService(tc.hc, ns)
			if tc.wantNil {
				g.Expect(svc).To(BeNil())
			} else {
				g.Expect(svc).ToNot(BeNil())
				g.Expect(svc.Name).To(Equal(tc.wantName))
				g.Expect(svc.Namespace).To(Equal(ns))
			}
		})
	}
}

func TestExpectedNodeRuntimeHandlers(t *testing.T) {
	originalVersion := releaseVersion
	t.Cleanup(func() { releaseVersion = originalVersion })

	tests := []struct {
		name           string
		releaseVersion semver.Version
		nodePool       *hyperv1.NodePool
		wantRunc       bool
		wantErr        bool
	}{
		{
			name:           "When status reports RHEL 10 and suite is pre-5.0, it should not require runc",
			releaseVersion: Version423,
			nodePool: &hyperv1.NodePool{Status: hyperv1.NodePoolStatus{
				OSImageStream: hyperv1.OSImageStreamReference{Name: hyperv1.OSImageStreamRHEL10},
			}},
			wantRunc: false,
		},
		{
			name:           "When status reports RHEL 9 and suite is 5.0+, it should require runc",
			releaseVersion: Version50,
			nodePool: &hyperv1.NodePool{Status: hyperv1.NodePoolStatus{
				OSImageStream: hyperv1.OSImageStreamReference{Name: hyperv1.OSImageStreamRHEL9},
			}},
			wantRunc: true,
		},
		{
			name:           "When status reports RHEL 10 and spec requests RHEL 9, it should not require runc",
			releaseVersion: Version423,
			nodePool: &hyperv1.NodePool{
				Spec:   hyperv1.NodePoolSpec{OSImageStream: hyperv1.OSImageStreamReference{Name: hyperv1.OSImageStreamRHEL9}},
				Status: hyperv1.NodePoolStatus{OSImageStream: hyperv1.OSImageStreamReference{Name: hyperv1.OSImageStreamRHEL10}},
			},
			wantRunc: false,
		},
		{
			name:           "When status reports RHEL 9 and spec requests RHEL 10, it should require runc",
			releaseVersion: Version50,
			nodePool: &hyperv1.NodePool{
				Spec:   hyperv1.NodePoolSpec{OSImageStream: hyperv1.OSImageStreamReference{Name: hyperv1.OSImageStreamRHEL10}},
				Status: hyperv1.NodePoolStatus{OSImageStream: hyperv1.OSImageStreamReference{Name: hyperv1.OSImageStreamRHEL9}},
			},
			wantRunc: true,
		},
		{
			name:           "When status version is pre-5.0 with no stream info, it should require runc",
			releaseVersion: Version50,
			nodePool:       &hyperv1.NodePool{Status: hyperv1.NodePoolStatus{Version: "4.23.0"}},
			wantRunc:       true,
		},
		{
			name:           "When status version is 5.0+ with no stream info, it should not require runc",
			releaseVersion: Version423,
			nodePool:       &hyperv1.NodePool{Status: hyperv1.NodePoolStatus{Version: "5.0.0"}},
			wantRunc:       false,
		},
		{
			name:           "When status version is invalid semver, it should return an error",
			releaseVersion: Version50,
			nodePool:       &hyperv1.NodePool{Status: hyperv1.NodePoolStatus{Version: "not-a-semver"}},
			wantErr:        true,
		},
		{
			name:           "When spec requests RHEL 9 and status version is 5.0+, it should require runc",
			releaseVersion: Version50,
			nodePool: &hyperv1.NodePool{
				Spec:   hyperv1.NodePoolSpec{OSImageStream: hyperv1.OSImageStreamReference{Name: hyperv1.OSImageStreamRHEL9}},
				Status: hyperv1.NodePoolStatus{Version: "5.0.0"},
			},
			wantRunc: true,
		},
		{
			name:           "When NodePool has no stream or version info, it should fall back to suite release version",
			releaseVersion: Version423,
			nodePool:       &hyperv1.NodePool{},
			wantRunc:       true,
		},
		{
			name:           "When NodePool is nil, it should fall back to suite release version",
			releaseVersion: Version423,
			wantRunc:       true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			releaseVersion = tc.releaseVersion
			got, err := expectedNodeRuntimeHandlers(tc.nodePool)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected invalid status version to fail validation")
				}
				return
			}
			if err != nil {
				t.Fatalf("expected valid NodePool state, got error: %v", err)
			}
			if _, ok := got["crun"]; !ok {
				t.Fatal("expected crun to be required")
			}
			if _, ok := got["runc"]; ok != tc.wantRunc {
				t.Errorf("runc required = %t, want %t; handlers = %#v", ok, tc.wantRunc, got)
			}
		})
	}
}

// TestGenerateCustomCertificate verifies that our certificate generation works correctly
func TestGenerateCustomCertificate(t *testing.T) {
	testsCases := []struct {
		name       string
		dnsNames   []string
		duration   time.Duration
		wantErr    bool
		expectedCN string
	}{
		{
			name:       "When generating a certificate with DNS names it should succeed",
			dnsNames:   []string{"example.com", "test.example.com"},
			duration:   24 * time.Hour,
			wantErr:    false,
			expectedCN: "example.com",
		},
		{
			name:     "When generating a certificate with no DNS names it should fail",
			dnsNames: []string{},
			duration: 24 * time.Hour,
			wantErr:  true,
		},
		{
			name:       "When generating a certificate with zero duration it should succeed",
			dnsNames:   []string{"example.com"},
			duration:   0,
			wantErr:    false,
			expectedCN: "example.com",
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			certPEM, keyPEM, err := v2util.GenerateCustomCertificate(tc.dnsNames, tc.duration)

			if tc.wantErr {
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(certPEM).NotTo(BeNil())
			g.Expect(keyPEM).NotTo(BeNil())

			// Parse the certificate to verify its contents
			cert, err := certs.PemToCertificate(certPEM)
			g.Expect(err).NotTo(HaveOccurred())

			// Verify CommonName
			g.Expect(cert.Subject.CommonName).To(Equal(tc.expectedCN))

			// Verify DNS names
			if len(tc.dnsNames) == 0 {
				g.Expect(cert.DNSNames).To(BeEmpty())
			} else {
				g.Expect(cert.DNSNames).To(Equal(tc.dnsNames))
			}

			// Verify validity period
			if tc.duration > 0 {
				g.Expect(cert.NotAfter.Sub(cert.NotBefore)).To(Equal(tc.duration))
			}

			// Verify key usage
			g.Expect(cert.KeyUsage & x509.KeyUsageKeyEncipherment).NotTo(BeZero())
			g.Expect(cert.KeyUsage & x509.KeyUsageDigitalSignature).NotTo(BeZero())

			// Verify extended key usage
			g.Expect(cert.ExtKeyUsage).To(ContainElement(x509.ExtKeyUsageServerAuth))

			// Verify the private key can be parsed
			_, err = certs.PemToPrivateKey(keyPEM)
			g.Expect(err).NotTo(HaveOccurred())
		})
	}
}

func TestMatchesLeaderElectionFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		line  string
		match bool
	}{
		{
			name:  "When log contains controller-runtime leader election lost error, it should match",
			line:  `E0709 10:32:45.123456       1 main.go:42] "leader election lost"`,
			match: true,
		},
		{
			name:  "When log contains structured leader election lost error, it should match",
			line:  `{"ts":"2025-07-09T10:32:45Z","level":"error","msg":"leader election lost"}`,
			match: true,
		},
		{
			name:  "When log contains failed to renew lease message, it should match",
			line:  `I0709 10:32:45.123456       1 leaderelection.go:299] "Failed to renew lease" lock="ns/lease" err="context deadline exceeded"`,
			match: true,
		},
		{
			name:  "When log contains stopped leading event, it should match",
			line:  `I0709 10:32:45.123456       1 leaderelection.go:280] stopped leading`,
			match: true,
		},
		{
			name:  "When log contains unrelated message, it should not match",
			line:  `I0709 10:32:45.123456       1 controller.go:100] "Reconciling resource" name="my-resource"`,
			match: false,
		},
		{
			name:  "When log contains election in non-failure context, it should not match",
			line:  `I0709 10:32:45.123456       1 leaderelection.go:272] "Successfully acquired lease"`,
			match: false,
		},
		{
			name:  "When log is empty, it should not match",
			line:  "",
			match: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(MatchesLeaderElectionFailure(tc.line)).To(Equal(tc.match))
		})
	}
}

func TestMatchesFatal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		line  string
		match bool
	}{
		{
			name:  "When the line is a klog fatal, it should match",
			line:  `F1111 15:23:29.927335       1 main.go:106] failed to apply cluster TLS profile`,
			match: true,
		},
		{
			name:  "When the line is a klog fatal on a control plane endpoint, it should match",
			line:  `F1111 15:23:29.927335       1 main.go:106] failed to create client: dial tcp 172.30.77.66:6443: connect: connection refused`,
			match: true,
		},
		{
			name:  "When the line is shorter than a klog header, it should not match",
			line:  `F1127`,
			match: false,
		},
		{
			name:  "When the day field is longer than two digits, it should not match",
			line:  `F11279 main.go:106] failed to apply cluster TLS profile`,
			match: false,
		},
		{
			name:  "When the header has a tab instead of a space, it should not match",
			line:  "F1127\t01:02:54.327335       1 main.go:106] failed to apply cluster TLS profile",
			match: false,
		},
		{
			name:  "When the line is a klog error, it should not match",
			line:  `E1111 15:23:29.927335       1 main.go:106] failed to apply cluster TLS profile`,
			match: false,
		},
		{
			name:  "When the line starts with F but is not a klog severity prefix, it should not match",
			line:  `Failed to apply cluster TLS profile`,
			match: false,
		},
		{
			name:  "When the line is a zap fatal, it should not match",
			line:  `{"level":"fatal","msg":"failed to apply cluster TLS profile"}`,
			match: false,
		},
		{
			name:  "When log is empty, it should not match",
			line:  "",
			match: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(MatchesFatal(tc.line)).To(Equal(tc.match))
		})
	}
}

func TestMatchesTokenMinterUnavailable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		line  string
		match bool
	}{
		{
			name:  "When the cloud token file is missing, it should match",
			line:  `E1127 01:02:54.057079       1 csi-provisioner.go:236] CSI driver probe failed: rpc error: code = FailedPrecondition desc = Failed health check (verify network connection and IAM credentials): dry-run EC2 API call failed: operation error EC2: DescribeAvailabilityZones, get identity: get credentials: failed to refresh cached credentials, failed to retrieve jwt from provide source, unable to read file at /var/run/secrets/openshift/serviceaccount/token: open /var/run/secrets/openshift/serviceaccount/token: no such file or directory`,
			match: true,
		},
		{
			name:  "When the kubelet-projected service account token is missing, it should not match",
			line:  `E1111 15:23:29.927335       1 main.go:106] failed to read token: open /var/run/secrets/kubernetes.io/serviceaccount/token: no such file or directory`,
			match: false,
		},
		{
			name:  "When a sibling file of the token is missing, it should not match",
			line:  `E1111 15:23:29.927335       1 main.go:106] failed to read token: open /var/run/secrets/openshift/serviceaccount/token.bak: no such file or directory`,
			match: false,
		},
		{
			name:  "When the token path and the missing-file error come from different files, it should not match",
			line:  `E1111 15:23:29.927335       1 main.go:106] open /var/run/secrets/openshift/serviceaccount/token: permission denied; open /etc/driver.conf: no such file or directory`,
			match: false,
		},
		{
			name:  "When the token file is unreadable for another reason, it should not match",
			line:  `E1111 15:23:29.927335       1 main.go:106] failed to read token: open /var/run/secrets/openshift/serviceaccount/token: permission denied`,
			match: false,
		},
		{
			name:  "When an unrelated file is missing, it should not match",
			line:  `E1111 15:23:29.927335       1 main.go:106] failed to read /etc/hosted-kubernetes/kubeconfig: no such file or directory`,
			match: false,
		},
		{
			name:  "When the log line is unrelated, it should not match",
			line:  `I1111 15:23:29.927335       1 main.go:106] serving metrics on :8080`,
			match: false,
		},
		{
			name:  "When log is empty, it should not match",
			line:  "",
			match: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(MatchesTokenMinterUnavailable(tc.line)).To(Equal(tc.match))
		})
	}
}

func TestMatchesControlPlaneUnavailable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		line  string
		match bool
	}{
		{
			name:  "When a component cannot dial the kube-apiserver service, it should match",
			line:  `F1111 15:23:29.927335       1 main.go:106] failed to apply cluster TLS profile: failed to create CNO client: Get "https://kube-apiserver:6443/api?timeout=32s": dial tcp 172.30.77.66:6443: connect: connection refused`,
			match: true,
		},
		{
			name:  "When a sidecar cannot dial the kube-apiserver over localhost, it should match",
			line:  `{"level":"error","msg":"failed to apply file: failed to get server groups: Get \"https://localhost:6443/api\": dial tcp [::1]:6443: connect: connection refused"}`,
			match: true,
		},
		{
			name:  "When the kube-apiserver cannot reach etcd, it should match",
			line:  `E1111 15:23:13.993104       1 run.go:72] "command failed" err="etcd grpc connection not ready: context deadline exceeded"`,
			match: true,
		},
		{
			name:  "When a client times out dialing etcd, it should match",
			line:  `W1111 15:22:53.990365       1 logging.go:55] grpc: addrConn.createTransport failed to connect to {Addr: "etcd-client.ns.svc:2379"}. Err: connection error: desc = "transport: Error while dialing: dial tcp 172.30.68.82:2379: connect: connection refused"`,
			match: true,
		},
		{
			name:  "When a transport failure names no control plane endpoint, it should not match",
			line:  `E1111 15:23:29.927335       1 main.go:106] failed to reach https://registry.example.com/v2/: dial tcp 10.0.0.1:443: connect: connection refused`,
			match: false,
		},
		{
			name:  "When a control plane endpoint is reached successfully, it should not match",
			line:  `I1111 15:23:29.927335       1 main.go:106] connected to https://kube-apiserver:6443`,
			match: false,
		},
		{
			name:  "When the kube-apiserver rejects a request, it should not match",
			line:  `E1111 15:23:29.927335       1 main.go:106] Get "https://kube-apiserver:6443/api": forbidden`,
			match: false,
		},
		{
			name:  "When log is empty, it should not match",
			line:  "",
			match: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(MatchesControlPlaneUnavailable(tc.line)).To(Equal(tc.match))
		})
	}
}

func TestCauseMaxRestarts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		description string
		maxRestarts int32
		capped      bool
	}{
		{
			name:        "When the cause is capped, it should report the cap",
			description: "Token minter not ready",
			maxRestarts: 3,
			capped:      true,
		},
		{
			name:        "When the cause is unbounded, it should report no cap",
			description: "Leader election failure",
			capped:      false,
		},
		{
			name:        "When the cause is unknown, it should report no cap",
			description: "No such cause",
			capped:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			maxRestarts, capped := causeMaxRestarts(tc.description)
			g.Expect(capped).To(Equal(tc.capped))
			g.Expect(maxRestarts).To(Equal(tc.maxRestarts))
		})
	}
}

func TestContainerRestartCountMatches(t *testing.T) {
	t.Parallel()

	podWithRestartCounts := func(counts map[string]int32) *corev1.Pod {
		pod := &corev1.Pod{}
		for name, count := range counts {
			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
				Name:         name,
				RestartCount: count,
			})
		}
		return pod
	}

	tests := []struct {
		name      string
		pod       *corev1.Pod
		container string
		observed  int32
		match     bool
	}{
		{
			name:      "When the restart count is unchanged, it should match",
			pod:       podWithRestartCounts(map[string]int32{"csi-provisioner": 3}),
			container: "csi-provisioner",
			observed:  3,
			match:     true,
		},
		{
			name:      "When the container restarted after the pod was listed, it should not match",
			pod:       podWithRestartCounts(map[string]int32{"csi-provisioner": 4}),
			container: "csi-provisioner",
			observed:  3,
			match:     false,
		},
		{
			name:      "When the container is gone, it should not match",
			pod:       podWithRestartCounts(map[string]int32{"csi-driver": 1}),
			container: "csi-provisioner",
			observed:  1,
			match:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(containerRestartCountMatches(tc.pod, tc.container, tc.observed)).To(Equal(tc.match))
		})
	}
}

func TestToleratedRestartCauseInLog(t *testing.T) {
	t.Parallel()

	const (
		etcdWarning = `W1111 15:22:53.990365       1 logging.go:55] grpc: addrConn.createTransport failed to connect to {Addr: "etcd-client.ns.svc:2379"}. Err: connection error: desc = "transport: Error while dialing: dial tcp 172.30.68.82:2379: connect: connection refused"`
		kasRefused  = `F1111 15:23:29.927335       1 main.go:106] failed to create client: Get "https://kube-apiserver:6443/api?timeout=32s": dial tcp 172.30.77.66:6443: connect: connection refused`
		leaderLost  = `E1111 15:23:29.927335       1 leaderelection.go:369] failed to renew lease ns/some-component: timed out waiting for the condition`
		crash       = "panic: runtime error: invalid memory address or nil pointer dereference"
		stackFrame  = `goroutine 1 [running]:` + "\n" + `main.run(0xc000123456)` + "\n" + "\t" + `/go/src/main.go:42 +0x1a5`
		healthy     = `I1111 15:23:29.927335       1 main.go:106] serving metrics on :8080`

		missingCloudToken = `E1127 01:02:54.057079       1 csi-provisioner.go:236] CSI driver probe failed: failed to retrieve jwt from provide source, unable to read file at /var/run/secrets/openshift/serviceaccount/token: open /var/run/secrets/openshift/serviceaccount/token: no such file or directory`
	)

	tests := []struct {
		name         string
		log          string
		restartCount int32
		cause        string
		tolerated    bool
	}{
		{
			name:      "When the log ends on a control plane endpoint being unreachable, it should be tolerated",
			log:       strings.Join([]string{healthy, kasRefused}, "\n"),
			cause:     "Control plane unavailability",
			tolerated: true,
		},
		{
			name:      "When the log ends on a lost lease, it should be tolerated",
			log:       strings.Join([]string{healthy, leaderLost}, "\n"),
			cause:     "Leader election failure",
			tolerated: true,
		},
		{
			name:      "When a retryable etcd connection warning is followed by an unrelated panic, it should not be tolerated",
			log:       strings.Join([]string{etcdWarning, crash, stackFrame}, "\n"),
			tolerated: false,
		},
		{
			name:      "When a retryable etcd connection warning is followed by an unrelated fatal error, it should not be tolerated",
			log:       strings.Join([]string{etcdWarning, "fatal error: concurrent map writes"}, "\n"),
			tolerated: false,
		},
		{
			name:      "When a panic is followed by the control plane going away, it should be tolerated",
			log:       strings.Join([]string{crash, stackFrame, kasRefused}, "\n"),
			cause:     "Control plane unavailability",
			tolerated: true,
		},
		{
			name:      "When the component panics on a control plane endpoint being unreachable, it should be tolerated",
			log:       strings.Join([]string{healthy, `panic: Get "https://kube-apiserver:6443/api": dial tcp 172.30.77.66:6443: connect: connection refused`}, "\n"),
			cause:     "Control plane unavailability",
			tolerated: true,
		},
		{
			name:      "When the log ends on the token-minter not having written the token, it should be tolerated",
			log:       strings.Join([]string{healthy, missingCloudToken}, "\n"),
			cause:     "Token minter not ready",
			tolerated: true,
		},
		{
			name:      "When the token-minter misses the token and the component then panics, it should not be tolerated",
			log:       strings.Join([]string{missingCloudToken, crash, stackFrame}, "\n"),
			tolerated: false,
		},
		{
			name:         "When the token-minter cause matches at the restart ceiling, it should be tolerated",
			log:          strings.Join([]string{healthy, missingCloudToken}, "\n"),
			restartCount: 3,
			cause:        "Token minter not ready",
			tolerated:    true,
		},
		{
			name:         "When the token-minter cause matches above the restart ceiling, it should not be tolerated",
			log:          strings.Join([]string{healthy, missingCloudToken}, "\n"),
			restartCount: 4,
			tolerated:    false,
		},
		{
			name:         "When an unbounded cause matches at a high restart count, it should be tolerated",
			log:          strings.Join([]string{healthy, leaderLost}, "\n"),
			restartCount: 10,
			cause:        "Leader election failure",
			tolerated:    true,
		},
		{
			name:      "When the token-minter misses the token and the component then exits fatally, it should not be tolerated",
			log:       strings.Join([]string{missingCloudToken, `F1127 01:02:55.000000       1 main.go:106] failed to apply storage class: invalid object`}, "\n"),
			tolerated: false,
		},
		{
			name:      "When the log records no tolerated cause, it should not be tolerated",
			log:       strings.Join([]string{healthy, "E1111 15:23:29.927335       1 main.go:106] exiting"}, "\n"),
			tolerated: false,
		},
		{
			name:      "When the log is empty, it should not be tolerated",
			log:       "",
			tolerated: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cause, tolerated, err := toleratedRestartCauseInLog(strings.NewReader(tc.log), tc.restartCount)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(tolerated).To(Equal(tc.tolerated))
			g.Expect(cause).To(Equal(tc.cause))
		})
	}
}

// errAfterReader yields prefix and then fails, standing in for a log stream that breaks
// part way through.
type errAfterReader struct {
	prefix *strings.Reader
	err    error
}

func (r *errAfterReader) Read(p []byte) (int, error) {
	if r.prefix.Len() > 0 {
		return r.prefix.Read(p)
	}
	return 0, r.err
}

func TestToleratedRestartCauseInLogReadFailure(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	// The part of the log that was read ends on a tolerated cause, so only the error tells
	// the caller that the end of the log was never seen.
	log := &errAfterReader{
		prefix: strings.NewReader(`E1111 15:23:29.927335       1 leaderelection.go:369] failed to renew lease ns/some-component: timed out waiting for the condition` + "\n"),
		err:    errors.New("unexpected EOF"),
	}

	_, _, err := toleratedRestartCauseInLog(log, 0)
	g.Expect(err).To(MatchError(ContainSubstring("unexpected EOF")))
}
