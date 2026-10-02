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

package internal

import (
	"context"
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestWaitForHostedClusterKubeConfig(t *testing.T) {
	const kubeconfig = "apiVersion: v1\nkind: Config\n"

	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "example"},
		Status: hyperv1.HostedClusterStatus{
			KubeConfig: &corev1.LocalObjectReference{Name: "example-kubeconfig"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: hc.Status.KubeConfig.Name},
		Data:       map[string][]byte{"kubeconfig": []byte(kubeconfig)},
	}
	client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(hc, secret).Build()
	testCtx := &TestContext{Context: t.Context(), MgmtClient: client}

	got, err := testCtx.WaitForHostedClusterKubeConfig(hc)
	if err != nil {
		t.Fatalf("WaitForHostedClusterKubeConfig returned an unexpected error: %v", err)
	}
	if string(got) != kubeconfig {
		t.Fatalf("WaitForHostedClusterKubeConfig returned %q, want %q", got, kubeconfig)
	}
}

func TestWaitForHostedClusterKubeConfigRefreshesHostedCluster(t *testing.T) {
	const kubeconfig = "apiVersion: v1\nkind: Config\n"

	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "example"},
	}
	storedHC := hc.DeepCopy()
	storedHC.Status.KubeConfig = &corev1.LocalObjectReference{Name: "example-kubeconfig"}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: storedHC.Namespace, Name: storedHC.Status.KubeConfig.Name},
		Data:       map[string][]byte{"kubeconfig": []byte(kubeconfig)},
	}
	client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(storedHC, secret).Build()
	testCtx := &TestContext{Context: t.Context(), MgmtClient: client}

	_, err := testCtx.WaitForHostedClusterKubeConfig(hc)
	if err != nil {
		t.Fatalf("WaitForHostedClusterKubeConfig returned an unexpected error: %v", err)
	}
	if hc.Status.KubeConfig == nil || hc.Status.KubeConfig.Name != storedHC.Status.KubeConfig.Name {
		t.Fatalf("WaitForHostedClusterKubeConfig did not refresh HostedCluster status: %#v", hc.Status.KubeConfig)
	}
}

func TestWaitForHostedClusterRESTConfigUsesV2ClientSettings(t *testing.T) {
	const kubeconfig = `apiVersion: v1
clusters:
- cluster:
    server: https://example.com
  name: example
contexts:
- context:
    cluster: example
    user: example
  name: example
current-context: example
kind: Config
users:
- name: example
  user: {}
`

	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "example"},
		Status: hyperv1.HostedClusterStatus{
			KubeConfig: &corev1.LocalObjectReference{Name: "example-kubeconfig"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: hc.Status.KubeConfig.Name},
		Data:       map[string][]byte{"kubeconfig": []byte(kubeconfig)},
	}
	client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(hc, secret).Build()
	testCtx := &TestContext{Context: t.Context(), MgmtClient: client}

	config, err := testCtx.WaitForHostedClusterRESTConfig(hc)
	if err != nil {
		t.Fatalf("WaitForHostedClusterRESTConfig returned an unexpected error: %v", err)
	}
	if config.QPS != -1 || config.Burst != -1 {
		t.Fatalf("WaitForHostedClusterRESTConfig settings = QPS %v, Burst %v; want QPS -1, Burst -1", config.QPS, config.Burst)
	}
}

func TestWaitForHostedClusterKubeConfigReturnsLastError(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()

	hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "example"}}
	client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(hc).Build()
	testCtx := &TestContext{Context: ctx, MgmtClient: client}

	_, err := testCtx.WaitForHostedClusterKubeConfig(hc)
	if err == nil {
		t.Fatal("WaitForHostedClusterKubeConfig returned nil error")
	}
	if !strings.Contains(err.Error(), "has not published a kubeconfig reference") {
		t.Fatalf("WaitForHostedClusterKubeConfig error = %q, want last observed kubeconfig error", err)
	}
}
