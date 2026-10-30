//go:build e2ev2

package tests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	consolelogsazure "github.com/openshift/hypershift/cmd/consolelogs/azure"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
)

const bootstrapSerialMarker = "AZURE-DIAGNOSTICS-WITHOUT-KUBELET"

// The isolated worker boots and writes a console marker, but cannot register.
// Only this new NodePool is changed; existing workers remain available.
const bootstrapDiagnosticsConfig = `apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: azure-diagnostics-bootstrap
spec:
  config:
    ignition:
      version: 3.2.0
    systemd:
      units:
      - name: kubelet.service
        mask: true
      - name: azure-diagnostics-marker.service
        enabled: true
        contents: |
          [Unit]
          Description=Azure diagnostics boot marker
          [Service]
          Type=oneshot
          ExecStart=/usr/bin/echo AZURE-DIAGNOSTICS-WITHOUT-KUBELET
          StandardOutput=journal+console
          [Install]
          WantedBy=multi-user.target
`

// RegisterAzureBootstrapDiagnosticsTests verifies serial access without a Node.
func RegisterAzureBootstrapDiagnosticsTests(getTestCtx internal.TestContextGetter) {
	It("should collect a serial boot log from a worker that never registers a Node", func(ctx SpecContext) {
		tc := getTestCtx()
		hc, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		template := getDefaultNodePool(ctx, tc.MgmtClient, hc)
		Expect(template).NotTo(BeNil())
		Expect(template.Spec.Platform.Azure).NotTo(BeNil(), "default NodePool must have Azure configuration")
		config := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: e2eutil.SimpleNameGenerator.GenerateName("azure-diagnostics-"), Namespace: hc.Namespace},
			Data:       map[string]string{"config": bootstrapDiagnosticsConfig},
		}
		Expect(tc.MgmtClient.Create(ctx, config)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) {
			if err := tc.MgmtClient.Delete(ctx, config); err != nil && !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}
		})
		np := buildTestNodePool(template, "diagnostics", func(pool *hyperv1.NodePool) {
			pool.Spec.Replicas = ptr.To(int32(1))
			pool.Spec.AutoScaling = nil
			pool.Spec.Management.AutoRepair = false
			pool.Spec.Config = append(pool.Spec.Config, corev1.LocalObjectReference{Name: config.Name})
			pool.Spec.Platform.Azure.Diagnostics = &hyperv1.Diagnostics{StorageAccountType: hyperv1.AzureDiagnosticsStorageAccountTypeManaged}
		})
		Expect(tc.MgmtClient.Create(ctx, np)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { cleanupNodePool(ctx, tc.MgmtClient, np) })

		var machine clusterv1.Machine
		Eventually(func(g Gomega) {
			list := &clusterv1.MachineList{}
			g.Expect(tc.MgmtClient.List(ctx, list, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed())
			var matches []clusterv1.Machine
			for _, candidate := range list.Items {
				if candidate.Annotations[hyperv1.NodePoolLabel] == crclient.ObjectKeyFromObject(np).String() {
					matches = append(matches, candidate)
				}
			}
			g.Expect(matches).To(HaveLen(1))
			machine = matches[0]
			azureMachine := &capiazure.AzureMachine{}
			g.Expect(tc.MgmtClient.Get(ctx, crclient.ObjectKey{Namespace: machine.Namespace, Name: machine.Spec.InfrastructureRef.Name}, azureMachine)).To(Succeed())
			g.Expect(azureMachine.Spec.ProviderID).NotTo(BeNil())
			g.Expect(*azureMachine.Spec.ProviderID).NotTo(BeEmpty())
		}).WithContext(ctx).WithTimeout(15 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())
		Expect(machine.Status.NodeRef).To(BeNil(), "diagnostic worker must not register a Node")

		Expect(tc.ArtifactDir).NotTo(BeEmpty())
		Expect(os.MkdirAll(tc.ArtifactDir, 0755)).To(Succeed())
		dir, err := os.MkdirTemp(tc.ArtifactDir, "azure-bootstrap-diagnostics-")
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega, pollCtx context.Context) {
			report, _ := consolelogsazure.CollectMachineDiagnostics(pollCtx, tc.MgmtClient, hc, consolelogsazure.DiagnosticsOptions{
				CredentialsFile: internal.GetEnvVarValue("AZURE_CREDS"), ArtifactDir: dir, SerialOnly: true,
			}, logr.Discard())
			g.Expect(consolelogsazure.VerifyArtifacts(dir, report, []string{machine.Spec.InfrastructureRef.Name}, false)).To(Succeed())
			content, err := os.ReadFile(filepath.Join(dir, "machine-console-logs", machine.Spec.InfrastructureRef.Name+".log"))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(strings.Contains(string(content), bootstrapSerialMarker)).To(BeTrue(), "serial log must contain the worker's boot marker")
		}).WithContext(ctx).WithTimeout(5 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())
		Expect(tc.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&machine), &machine)).To(Succeed())
		Expect(machine.Status.NodeRef).To(BeNil(), "serial collection must work without Node registration")
	}, SpecTimeout(25*time.Minute))
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:AzureMachineDiagnostics] Azure Bootstrap Diagnostics", Label("lifecycle", "azure-machine-diagnostics-bootstrap"), func() {
	var tc *internal.TestContext
	BeforeEach(func() {
		tc = internal.GetTestContext()
		Expect(tc).NotTo(BeNil())
		tc.SkipIfNotPlatform(hyperv1.AzurePlatform)
	})
	RegisterAzureBootstrapDiagnosticsTests(func() *internal.TestContext { return tc })
})
