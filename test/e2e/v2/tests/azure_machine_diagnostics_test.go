//go:build e2ev2

package tests

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/util/dump"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
)

func init() {
	internal.RegisterEnvVarWithDefault("AZURE_CREDS", "Azure credentials for worker serial diagnostics", false, "/etc/hypershift-ci-jobs-self-managed-azure/credentials.json")
	internal.RegisterEnvVar("AZURE_JOURNAL_SSH_KEY", "Optional worker SSH private key file for journal fallback", false)
	internal.RegisterEnvVar("AZURE_JOURNAL_SSH_JUMP_HOST", "Optional SSH jump host for worker journals", false)
	internal.RegisterEnvVar("AZURE_JOURNAL_SSH_KNOWN_HOSTS", "Known hosts file for worker journal SSH access", false)
}

// RegisterAzureMachineDiagnosticsTests verifies actual production collector output.
func RegisterAzureMachineDiagnosticsTests(getTestCtx internal.TestContextGetter) {
	It("should collect nonempty serial, journal, kubelet and CRI-O artifacts for Azure workers", func(ctx SpecContext) {
		tc := getTestCtx()
		hc, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		Expect(dump.VerifyAzureMachineDiagnostics(ctx, tc.MgmtClient, hc,
			internal.GetEnvVarValue("AZURE_CREDS"), tc.ArtifactDir, "")).To(Succeed())
	}, SpecTimeout(12*time.Minute))
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:AzureMachineDiagnostics] Azure Machine Diagnostics", Label("azure-machine-diagnostics"), func() {
	var tc *internal.TestContext
	BeforeEach(func() {
		tc = internal.GetTestContext()
		Expect(tc).NotTo(BeNil())
		tc.SkipIfNotPlatform(hyperv1.AzurePlatform)
	})
	RegisterAzureMachineDiagnosticsTests(func() *internal.TestContext { return tc })
})
