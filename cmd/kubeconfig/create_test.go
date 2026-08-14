package kubeconfig

import (
	"context"
	"fmt"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientcmdapiv1 "k8s.io/client-go/tools/clientcmd/api/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"
)

func validKubeconfig(t *testing.T, server string) []byte {
	t.Helper()
	kc := clientcmdapiv1.Config{
		APIVersion: "v1",
		Kind:       "Config",
		Clusters: []clientcmdapiv1.NamedCluster{
			{
				Name: "test-cluster",
				Cluster: clientcmdapiv1.Cluster{
					Server:                   server,
					CertificateAuthorityData: []byte("fake-ca"),
				},
			},
		},
		AuthInfos: []clientcmdapiv1.NamedAuthInfo{
			{
				Name: "admin",
				AuthInfo: clientcmdapiv1.AuthInfo{
					ClientCertificateData: []byte("fake-cert"),
					ClientKeyData:         []byte("fake-key"),
				},
			},
		},
		Contexts: []clientcmdapiv1.NamedContext{
			{
				Name: "test-context",
				Context: clientcmdapiv1.Context{
					Cluster:  "test-cluster",
					AuthInfo: "admin",
				},
			},
		},
		CurrentContext: "test-context",
	}
	data, err := yaml.Marshal(&kc)
	if err != nil {
		t.Fatalf("failed to marshal valid kubeconfig: %v", err)
	}
	return data
}

func TestRewriteServerForPrivateCluster(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add corev1 to scheme: %v", err)
	}

	cluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "clusters",
		},
	}
	controlPlaneNamespace := fmt.Sprintf("%s-%s", cluster.Namespace, cluster.Name)

	tests := []struct {
		name        string
		objects     []runtime.Object
		data        []byte
		expectError bool
		expectPort  int32
	}{
		{
			name:        "When the KAS service is not found, it should return an error",
			objects:     nil,
			data:        validKubeconfig(t, "https://api.example.com:6443"),
			expectError: true,
		},
		{
			name: "When the KAS service has no ports, it should default to port 6443",
			objects: []runtime.Object{
				&corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: controlPlaneNamespace,
					},
					Spec: corev1.ServiceSpec{
						ClusterIP: "10.0.0.100",
						Ports:     []corev1.ServicePort{},
					},
				},
			},
			data:        validKubeconfig(t, "https://api.example.com:6443"),
			expectError: false,
			expectPort:  6443,
		},
		{
			name: "When the kubeconfig has a valid server URL, it should rewrite to the KAS service ClusterIP and port",
			objects: []runtime.Object{
				&corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: controlPlaneNamespace,
					},
					Spec: corev1.ServiceSpec{
						ClusterIP: "10.0.0.100",
						Ports: []corev1.ServicePort{
							{
								Port: 8443,
							},
						},
					},
				},
			},
			data:        validKubeconfig(t, "https://api.example.com:6443"),
			expectError: false,
			expectPort:  8443,
		},
		{
			name: "When the kubeconfig YAML is malformed, it should return an error",
			objects: []runtime.Object{
				&corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: controlPlaneNamespace,
					},
					Spec: corev1.ServiceSpec{
						ClusterIP: "10.0.0.100",
						Ports: []corev1.ServicePort{
							{
								Port: 6443,
							},
						},
					},
				},
			},
			data:        []byte("{{not: valid: yaml: [[["),
			expectError: true,
		},
		{
			name: "When the function succeeds, it should update the kubeconfig bytes with the new server URL",
			objects: []runtime.Object{
				&corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: controlPlaneNamespace,
					},
					Spec: corev1.ServiceSpec{
						ClusterIP: "10.0.0.100",
						Ports: []corev1.ServicePort{
							{
								Port: 6443,
							},
						},
					},
				},
			},
			data:        validKubeconfig(t, "https://api.original.example.com:6443"),
			expectError: false,
			expectPort:  6443,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			builder := fake.NewClientBuilder().WithScheme(scheme)
			if len(tc.objects) > 0 {
				builder = builder.WithRuntimeObjects(tc.objects...)
			}
			c := builder.Build()

			result, err := rewriteServerForPrivateCluster(context.Background(), c, cluster, tc.data)
			if tc.expectError {
				if err == nil {
					t.Fatalf("expected an error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}

			// Verify the rewritten kubeconfig contains the expected server URL.
			var resultConfig clientcmdapiv1.Config
			if err := yaml.Unmarshal(result, &resultConfig); err != nil {
				t.Fatalf("failed to unmarshal result kubeconfig: %v", err)
			}

			expectedServer := fmt.Sprintf("https://localhost:%d", tc.expectPort)
			for i, namedCluster := range resultConfig.Clusters {
				if namedCluster.Cluster.Server != expectedServer {
					t.Errorf("cluster[%d].Server = %q, want %q", i, namedCluster.Cluster.Server, expectedServer)
				}
			}
		})
	}
}
