//go:build e2ev2

package junitfixture

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var junitReportPath string

func init() {
	flag.StringVar(&junitReportPath, "e2e.junit-report", "", "path to the synthetic JUnit XML report")
}

func TestJUnitFixture(t *testing.T) {
	suiteConfig, reporterConfig := GinkgoConfiguration()
	junitReportPath, reporterConfig = internal.ConfigureJUnitReport(junitReportPath, reporterConfig)
	RegisterFailHandler(internal.InformingAwareFailHandler)
	RunSpecs(t, "synthetic-junit-contract", suiteConfig, reporterConfig)
}

var _ = ReportAfterSuite("Write synthetic JUnit", func(report Report) {
	if err := internal.GenerateJUnitReport(report, junitReportPath); err != nil {
		Fail(fmt.Sprintf("failed to write synthetic JUnit: %v", err))
	}
})

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:JUnitContract] Synthetic JUnit contract", Label("inherited-label"), func() {
	// Register separately so the existing reporting-contract scenarios stay unchanged.
	if os.Getenv("JUNIT_FIXTURE_REQUIRED_WIF") == "true" {
		It("required WIF mutation", Label("required-wif"), func() {
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
				Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{
					Type: hyperv1.PlatformType(os.Getenv("JUNIT_FIXTURE_PLATFORM")),
				}},
				Status: hyperv1.HostedClusterStatus{Version: &hyperv1.ClusterVersionStatus{
					History: []configv1.UpdateHistory{{Version: os.Getenv("JUNIT_FIXTURE_VERSION")}},
				}},
			}
			tc := &internal.TestContext{
				Context: context.Background(), ClusterName: hc.Name, ClusterNamespace: hc.Namespace,
				MgmtClient: fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(hc).Build(),
			}
			tc.SkipIfNotPlatform(hyperv1.GCPPlatform)
			tc.SkipIfVersionBelow(e2eutil.Version51)
			validateWIFMutation("")
		})
		return
	}

	It("ordinary pass", Label("leaf-label"), func() {})

	It("ordinary failure", func() {
		if os.Getenv("JUNIT_FIXTURE_BLOCKING_FAILURE") == "true" {
			validateWIFMutation("blocking boom")
		}
	})

	Context("informing context", Label(internal.InformingLabel), func() {
		It("informing pass", func() {})

		It("informing failure", func() {
			validateWIFMutation("informing boom")
		})

		It("informing mutation failure", func() {
			validateWIFMutation("")
		})

		It("informing genuine skip", func() {
			Skip("platform not supported")
		})
	})
})

// Exercise the actual helper's synchronous and Eventually assertion paths.
func validateWIFMutation(createError string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	hostedClient := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if createError != "" {
				return errors.New(createError)
			}
			pod, isPod := obj.(*corev1.Pod)
			mutation := os.Getenv("JUNIT_FIXTURE_MUTATION")
			if isPod && (mutation == "complete" || mutation == "token-only") {
				pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
					Name: "gcp-iam-token",
					VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
						Sources: []corev1.VolumeProjection{{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
							Audience: "openshift", Path: "token",
						}}},
					}},
				})
				if mutation == "complete" {
					pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, corev1.EnvVar{
						Name: "GOOGLE_APPLICATION_CREDENTIALS", Value: "/var/run/secrets/workload-identity/credentials.json",
					})
				}
			}
			err := c.Create(ctx, obj, opts...)
			if isPod && mutation != "complete" {
				// Stop retries after observing incomplete mutation, without waiting three minutes.
				cancel()
			}
			return err
		},
	}).Build()
	e2eutil.ValidateGCPWorkloadIdentityWebhookMutation(GinkgoTB(), Default, ctx, hostedClient)
}
