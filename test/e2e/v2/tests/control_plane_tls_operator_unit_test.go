//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestComponentSupportsTLSConfig(t *testing.T) {
	t.Parallel()

	components := []struct {
		component      controlPlaneTLSComponent
		deploymentName string
		containerName  string
		flag           string
		unrelatedFlag  string
	}{
		{pkiOperatorTLSComponent, "control-plane-pki-operator", "control-plane-pki-operator", "--config=/etc/kubernetes/config/config.yaml", "--tls-min-version=VersionTLS13"},
		{awsPodIdentityWebhookTLSComponent, "kube-apiserver", "aws-pod-identity-webhook", "--tls-min-version=VersionTLS13", "--config=/etc/kubernetes/config/config.yaml"},
		{konnectivityServerTLSComponent, "kube-apiserver", "konnectivity-server", "--tls-min-version=VersionTLS12", "--config=/etc/kubernetes/config/config.yaml"},
	}
	for _, component := range components {
		t.Run(component.component.name, func(t *testing.T) {
			t.Parallel()
			testCases := []struct {
				name              string
				command           []string
				args              []string
				missingDeployment bool
				missingContainer  bool
				supported         bool
				errorSubstring    string
			}{
				{
					name:      "When the TLS flag is in Command, it should support profile configuration",
					command:   []string{component.containerName, component.flag},
					supported: true,
				},
				{
					name:      "When the TLS flag is in Args, it should support profile configuration without a ConfigMap",
					command:   []string{component.containerName},
					args:      []string{component.flag},
					supported: true,
				},
				{
					name: "When the TLS flag is absent, it should not require a profile-driven restart",
				},
				{
					name:    "When only unrelated flags are present, it should not require a profile-driven restart",
					command: []string{component.unrelatedFlag},
					args:    []string{"--other-flag=value"},
				},
				{
					name:              "When the deployment is missing, it should report the lookup error",
					missingDeployment: true,
					errorSubstring:    "failed to get " + component.deploymentName + " deployment",
				},
				{
					name:             "When the target container is missing, it should report an error instead of hiding a regression",
					missingContainer: true,
					errorSubstring:   "container " + component.containerName + " not found",
				},
			}
			for _, testCase := range testCases {
				t.Run(testCase.name, func(t *testing.T) {
					t.Parallel()
					assertions := NewWithT(t)
					diagnosticContext := fmt.Sprintf("%s: %s (deployment clusters-tls/%s, container %s)",
						component.component.name, testCase.name, component.deploymentName, component.containerName)
					scheme := runtime.NewScheme()
					assertions.Expect(appsv1.AddToScheme(scheme)).To(Succeed(), "%s: failed to register deployment scheme", diagnosticContext)
					deployment := &appsv1.Deployment{
						ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-tls", Name: component.deploymentName},
						Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{Name: "other-sidecar", Command: []string{component.flag}, Args: []string{component.flag}},
							},
						}}},
					}
					if !testCase.missingContainer {
						deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, corev1.Container{
							Name: component.containerName, Command: testCase.command, Args: testCase.args,
						})
					}
					builder := fake.NewClientBuilder().WithScheme(scheme)
					if !testCase.missingDeployment {
						builder.WithObjects(deployment)
					}
					supported, err := componentSupportsTLSConfig(t.Context(), builder.Build(), "clusters-tls", component.component)
					if testCase.errorSubstring != "" {
						assertions.Expect(err).To(MatchError(ContainSubstring(testCase.errorSubstring)), "%s: capability probe should report the expected error", diagnosticContext)
						if testCase.missingDeployment {
							assertions.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "%s: missing deployment should preserve NotFound classification", diagnosticContext)
						}
					} else {
						assertions.Expect(err).NotTo(HaveOccurred(), "%s: capability probe should succeed", diagnosticContext)
					}
					assertions.Expect(supported).To(Equal(testCase.supported), "%s: unexpected TLS configuration capability", diagnosticContext)
				})
			}
		})
	}
}

func TestVerifyIntermediateTLSConnectivityAfterDowngrade(t *testing.T) {
	t.Parallel()

	components := []struct {
		component      controlPlaneTLSComponent
		deploymentName string
		containerName  string
		flag           string
	}{
		{pkiOperatorTLSComponent, "control-plane-pki-operator", "control-plane-pki-operator", "--config=/etc/kubernetes/config/config.yaml"},
		{awsPodIdentityWebhookTLSComponent, "kube-apiserver", "aws-pod-identity-webhook", "--tls-min-version=VersionTLS12"},
		{konnectivityServerTLSComponent, "kube-apiserver", "konnectivity-server", "--tls-min-version=VersionTLS12"},
	}
	for supportMask := 0; supportMask < 8; supportMask++ {
		supported := []bool{supportMask&1 != 0, supportMask&2 != 0, supportMask&4 != 0}
		name := fmt.Sprintf("When PKI support is %t, webhook support is %t and konnectivity support is %t, it should wait only for supported components and probe all components", supported[0], supported[1], supported[2])
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertions := NewWithT(t)
			scheme := runtime.NewScheme()
			assertions.Expect(appsv1.AddToScheme(scheme)).To(Succeed(), "%s: failed to register deployment scheme", name)
			pkiDeployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-tls", Name: "control-plane-pki-operator"}}
			kubeAPIServerDeployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-tls", Name: "kube-apiserver"}}
			previousUIDs := make([]string, len(components))
			for componentIndex, component := range components {
				container := corev1.Container{Name: component.containerName}
				if supported[componentIndex] {
					container.Args = []string{component.flag}
				}
				deployment := kubeAPIServerDeployment
				if component.deploymentName == pkiDeployment.Name {
					deployment = pkiDeployment
				}
				deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, container)
				previousUIDs[componentIndex] = component.component.name + "-original-uid"
			}
			mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pkiDeployment, kubeAPIServerDeployment).Build()
			expectedUIDs := append([]string(nil), previousUIDs...)
			for componentIndex, component := range components {
				diagnosticContext := fmt.Sprintf("%s: %s", name, component.component.name)
				originalUID := previousUIDs[componentIndex]
				refreshedUID := component.component.name + "-refreshed-uid"
				expectedEvents := []string{"probe"}
				if supported[componentIndex] {
					expectedUIDs[componentIndex] = refreshedUID
					expectedEvents = []string{"wait", "probe"}
				}
				events := []string{}
				err := verifyIntermediateTLSConnectivityAfterDowngrade(t.Context(), mgmtClient, "clusters-tls", component.component, &previousUIDs[componentIndex],
					func(previousUID string) string {
						events = append(events, "wait")
						assertions.Expect(previousUID).To(Equal(originalUID), "%s: restart wait should receive this component's original UID", diagnosticContext)
						assertions.Expect(previousUIDs[componentIndex]).To(Equal(originalUID), "%s: UID should not change before restart wait returns", diagnosticContext)
						return refreshedUID
					},
					func(expectations []tlsVersionExpectation) {
						events = append(events, "probe")
						assertions.Expect(previousUIDs).To(Equal(expectedUIDs), "%s: probe should observe the refreshed UID only for supported components and preserve independent UID state", diagnosticContext)
						assertions.Expect(expectations).To(Equal([]tlsVersionExpectation{
							{opensslFlag: "tls1_2", expectedProtocol: "tlsv1.2"},
							{opensslFlag: "tls1_3", expectedProtocol: "tlsv1.3"},
						}), "%s: connectivity should verify exactly TLS 1.2 and TLS 1.3 acceptance", diagnosticContext)
					})
				assertions.Expect(err).NotTo(HaveOccurred(), "%s: downgrade connectivity verification should succeed", diagnosticContext)
				assertions.Expect(events).To(Equal(expectedEvents), "%s: only supported components should wait, exactly once before probing", diagnosticContext)
				assertions.Expect(previousUIDs).To(Equal(expectedUIDs), "%s: only this component's UID should be refreshed when supported", diagnosticContext)
			}
		})
	}

	for _, component := range components {
		t.Run(fmt.Sprintf("When %s capability detection fails, it should preserve the UID and avoid waiting or probing", component.component.name), func(t *testing.T) {
			t.Parallel()
			testCases := []struct {
				name              string
				missingDeployment bool
				missingContainer  bool
				canceled          bool
				errorSubstring    string
			}{
				{
					name:              "When the deployment is missing, it should preserve the wrapped NotFound error",
					missingDeployment: true,
					errorSubstring:    "failed to get " + component.deploymentName + " deployment",
				},
				{
					name:             "When the target container is missing, it should report the missing container",
					missingContainer: true,
					errorSubstring:   "container " + component.containerName + " not found in " + component.deploymentName + " deployment",
				},
				{
					name:           "When the context is canceled, it should preserve the wrapped cancellation error",
					canceled:       true,
					errorSubstring: "failed to get " + component.deploymentName + " deployment",
				},
			}
			for _, testCase := range testCases {
				t.Run(testCase.name, func(t *testing.T) {
					t.Parallel()
					assertions := NewWithT(t)
					diagnosticContext := fmt.Sprintf("%s: %s", component.component.name, testCase.name)
					scheme := runtime.NewScheme()
					assertions.Expect(appsv1.AddToScheme(scheme)).To(Succeed(), "%s: failed to register deployment scheme", diagnosticContext)
					deployment := &appsv1.Deployment{
						ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-tls", Name: component.deploymentName},
						Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: "other-sidecar", Args: []string{component.flag}}},
						}}},
					}
					if !testCase.missingContainer {
						deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, corev1.Container{Name: component.containerName, Args: []string{component.flag}})
					}
					builder := fake.NewClientBuilder().WithScheme(scheme)
					if !testCase.missingDeployment {
						builder.WithObjects(deployment)
					}
					ctx := t.Context()
					if testCase.canceled {
						canceledContext, cancel := context.WithCancel(ctx)
						cancel()
						ctx = canceledContext
						builder.WithInterceptorFuncs(interceptor.Funcs{
							Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, options ...crclient.GetOption) error {
								if err := ctx.Err(); err != nil {
									return err
								}
								return client.Get(ctx, key, object, options...)
							},
						})
					}
					originalUID := component.component.name + "-original-uid"
					previousUID := originalUID
					events := []string{}
					err := verifyIntermediateTLSConnectivityAfterDowngrade(ctx, builder.Build(), "clusters-tls", component.component, &previousUID,
						func(string) string {
							events = append(events, "wait")
							return "unexpected-refreshed-uid"
						},
						func([]tlsVersionExpectation) { events = append(events, "probe") })
					assertions.Expect(err).To(MatchError(ContainSubstring(testCase.errorSubstring)), "%s: should return the capability detection error", diagnosticContext)
					if testCase.missingDeployment {
						assertions.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "%s: error should retain NotFound classification", diagnosticContext)
					}
					if testCase.canceled {
						assertions.Expect(errors.Is(err, context.Canceled)).To(BeTrue(), "%s: error should retain cancellation identity", diagnosticContext)
					}
					assertions.Expect(events).To(BeEmpty(), "%s: capability errors must prevent both restart waits and connectivity probes", diagnosticContext)
					assertions.Expect(previousUID).To(Equal(originalUID), "%s: capability errors must leave the UID unchanged", diagnosticContext)
				})
			}
		})
	}
}
