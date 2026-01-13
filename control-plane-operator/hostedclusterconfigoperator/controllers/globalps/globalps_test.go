package globalps

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	validAuth = base64.StdEncoding.EncodeToString([]byte("user:pass"))
	oldAuth   = base64.StdEncoding.EncodeToString([]byte("olduser:oldpass"))
)

func TestReconcileGlobalPullSecret(t *testing.T) {
	tests := []struct {
		name                       string
		existingObjects            []client.Object
		nodeObjects                []client.Object
		hcpNamespace               string
		hccoImage                  string
		expectGlobalSecretExists   bool
		expectOriginalSecretExists bool
		expectDaemonSetExists      bool
		expectServiceAccountExists bool
		expectError                bool
		validateDaemonSet          func(*testing.T, *appsv1.DaemonSet)
		validateGlobalSecret       func(*testing.T, *corev1.Secret)
		validateOriginalSecret     func(*testing.T, *corev1.Secret)
		validateServiceAccount     func(*testing.T, *corev1.ServiceAccount)
	}{
		{
			name:         "When original pull secret exists without additional pull secret, it should create original secret and daemonset without global secret",
			hcpNamespace: "test-hcp",
			hccoImage:    "test-image:latest",
			existingObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pull-secret",
						Namespace: "test-hcp",
					},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: composePullSecretBytes(map[string]string{"registry1.io": validAuth}),
					},
				},
			},
			nodeObjects: []client.Object{
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node-1",
					},
				},
				&capiv1.MachineSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-machineset",
						Namespace: "test-hcp",
					},
					Spec: capiv1.MachineSetSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{"machineset": "test"},
						},
					},
				},
				&capiv1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-machine",
						Namespace: "test-hcp",
						Labels:    map[string]string{"machineset": "test"},
					},
					Status: capiv1.MachineStatus{
						NodeRef: &corev1.ObjectReference{Name: "test-node-1"},
					},
				},
			},
			expectGlobalSecretExists:   false,
			expectOriginalSecretExists: true,
			expectDaemonSetExists:      true,
			expectServiceAccountExists: true,
			expectError:                false,
			validateDaemonSet: func(t *testing.T, ds *appsv1.DaemonSet) {
				g := NewWithT(t)
				g.Expect(ds.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue(globalPSLabelKey, "true"))
				g.Expect(ds.Spec.Template.Spec.Volumes).To(HaveLen(3)) // kubelet-config, dbus, original-pull-secret
				hasGlobalSecret := false
				for _, vol := range ds.Spec.Template.Spec.Volumes {
					if vol.Name == "global-pull-secret" {
						hasGlobalSecret = true
					}
				}
				g.Expect(hasGlobalSecret).To(BeFalse(), "DaemonSet should not have global-pull-secret volume when additional secret doesn't exist")
			},
			validateOriginalSecret: func(t *testing.T, secret *corev1.Secret) {
				g := NewWithT(t)
				g.Expect(secret.Data).To(HaveKey(corev1.DockerConfigJsonKey))
			},
		},
		{
			name:         "When original pull secret and valid additional pull secret exist, it should merge secrets and create daemonset with global secret",
			hcpNamespace: "test-hcp",
			hccoImage:    "test-image:latest",
			existingObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pull-secret",
						Namespace: "test-hcp",
					},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: composePullSecretBytes(map[string]string{"registry1.io": validAuth}),
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "additional-pull-secret",
						Namespace: "kube-system",
					},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: composePullSecretBytes(map[string]string{"registry2.io": validAuth}),
					},
				},
			},
			nodeObjects: []client.Object{
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node-1",
					},
				},
				&capiv1.MachineSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-machineset",
						Namespace: "test-hcp",
					},
					Spec: capiv1.MachineSetSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{"machineset": "test"},
						},
					},
				},
				&capiv1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-machine",
						Namespace: "test-hcp",
						Labels:    map[string]string{"machineset": "test"},
					},
					Status: capiv1.MachineStatus{
						NodeRef: &corev1.ObjectReference{Name: "test-node-1"},
					},
				},
			},
			expectGlobalSecretExists:   true,
			expectOriginalSecretExists: true,
			expectDaemonSetExists:      true,
			expectServiceAccountExists: true,
			expectError:                false,
			validateDaemonSet: func(t *testing.T, ds *appsv1.DaemonSet) {
				g := NewWithT(t)
				g.Expect(ds.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue(globalPSLabelKey, "true"))
				g.Expect(ds.Spec.Template.Spec.Volumes).To(HaveLen(4)) // kubelet-config, dbus, original-pull-secret, global-pull-secret
				hasGlobalSecret := false
				for _, vol := range ds.Spec.Template.Spec.Volumes {
					if vol.Name == "global-pull-secret" {
						hasGlobalSecret = true
					}
				}
				g.Expect(hasGlobalSecret).To(BeTrue(), "DaemonSet should have global-pull-secret volume when additional secret exists")
			},
			validateGlobalSecret: func(t *testing.T, secret *corev1.Secret) {
				g := NewWithT(t)
				g.Expect(secret.Data).To(HaveKey(corev1.DockerConfigJsonKey))
				// Verify merged content contains both registries
				var dockerConfigJSON map[string]any
				err := json.Unmarshal(secret.Data[corev1.DockerConfigJsonKey], &dockerConfigJSON)
				g.Expect(err).NotTo(HaveOccurred())
				auths := dockerConfigJSON["auths"].(map[string]any)
				g.Expect(auths).To(HaveKey("registry1.io"))
				g.Expect(auths).To(HaveKey("registry2.io"))
			},
		},
		{
			name:            "When original pull secret does not exist, it should return error",
			hcpNamespace:    "test-hcp",
			hccoImage:       "test-image:latest",
			existingObjects: []client.Object{
				// No pull secret in control plane namespace
			},
			nodeObjects:                []client.Object{},
			expectGlobalSecretExists:   false,
			expectOriginalSecretExists: false,
			expectDaemonSetExists:      false,
			expectServiceAccountExists: true, // ServiceAccount is created before error occurs
			expectError:                true,
		},
		{
			name:         "When original pull secret exists but missing dockerConfigJson key, it should return error",
			hcpNamespace: "test-hcp",
			hccoImage:    "test-image:latest",
			existingObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pull-secret",
						Namespace: "test-hcp",
					},
					Data: map[string][]byte{
						"wrong-key": []byte("data"),
					},
				},
			},
			nodeObjects:                []client.Object{},
			expectGlobalSecretExists:   false,
			expectOriginalSecretExists: false,
			expectDaemonSetExists:      false,
			expectServiceAccountExists: true,
			expectError:                true,
		},
		{
			name:         "When additional pull secret has invalid json, it should return error",
			hcpNamespace: "test-hcp",
			hccoImage:    "test-image:latest",
			existingObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pull-secret",
						Namespace: "test-hcp",
					},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: composePullSecretBytes(map[string]string{"registry1.io": validAuth}),
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "additional-pull-secret",
						Namespace: "kube-system",
					},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: []byte("invalid json"),
					},
				},
			},
			nodeObjects: []client.Object{
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node-1",
					},
				},
				&capiv1.MachineSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-machineset",
						Namespace: "test-hcp",
					},
					Spec: capiv1.MachineSetSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{"machineset": "test"},
						},
					},
				},
				&capiv1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-machine",
						Namespace: "test-hcp",
						Labels:    map[string]string{"machineset": "test"},
					},
					Status: capiv1.MachineStatus{
						NodeRef: &corev1.ObjectReference{Name: "test-node-1"},
					},
				},
			},
			expectGlobalSecretExists:   false,
			expectOriginalSecretExists: false,
			expectDaemonSetExists:      false,
			expectServiceAccountExists: true,
			expectError:                true,
		},
		{
			name:         "When nodes belong to InPlace NodePools, it should not label nodes",
			hcpNamespace: "test-hcp",
			hccoImage:    "test-image:latest",
			existingObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pull-secret",
						Namespace: "test-hcp",
					},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: composePullSecretBytes(map[string]string{"registry1.io": validAuth}),
					},
				},
			},
			nodeObjects: []client.Object{
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "inplace-node-1",
					},
				},
				&capiv1.MachineSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "inplace-machineset",
						Namespace: "test-hcp",
						Annotations: map[string]string{
							"hypershift.openshift.io/nodePoolTargetConfigVersion": "config-hash-123",
						},
					},
					Spec: capiv1.MachineSetSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{"machineset": "inplace"},
						},
					},
				},
				&capiv1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "inplace-machine",
						Namespace: "test-hcp",
						Labels:    map[string]string{"machineset": "inplace"},
					},
					Status: capiv1.MachineStatus{
						NodeRef: &corev1.ObjectReference{Name: "inplace-node-1"},
					},
				},
			},
			expectGlobalSecretExists:   false,
			expectOriginalSecretExists: true,
			expectDaemonSetExists:      true,
			expectServiceAccountExists: true,
			expectError:                false,
			validateDaemonSet: func(t *testing.T, ds *appsv1.DaemonSet) {
				g := NewWithT(t)
				g.Expect(ds.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue(globalPSLabelKey, "true"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Create runtime scheme
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			_ = appsv1.AddToScheme(scheme)
			_ = capiv1.AddToScheme(scheme)

			// Create separate clients for different namespaces/purposes
			cpClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.existingObjects...).Build()
			kubeSystemSecretClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.existingObjects...).Build()
			nodeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.nodeObjects...).Build()
			hcUncachedClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.existingObjects...).Build()

			// Create reconciler
			reconciler := &Reconciler{
				cpClient:               cpClient,
				kubeSystemSecretClient: kubeSystemSecretClient,
				nodeClient:             nodeClient,
				hcUncachedClient:       hcUncachedClient,
				hcpNamespace:           tt.hcpNamespace,
				hccoImage:              tt.hccoImage,
				CreateOrUpdateProvider: upsert.New(false),
			}

			// Execute reconciliation
			err := reconciler.reconcileGlobalPullSecret(context.Background())

			// Verify error expectation
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).NotTo(HaveOccurred())

			// Verify ServiceAccount
			if tt.expectServiceAccountExists {
				sa := &corev1.ServiceAccount{}
				err = hcUncachedClient.Get(context.Background(), client.ObjectKey{
					Name:      "global-pull-secret-syncer",
					Namespace: "kube-system",
				}, sa)
				g.Expect(err).NotTo(HaveOccurred())
				if tt.validateServiceAccount != nil {
					tt.validateServiceAccount(t, sa)
				}
			}

			// Verify global pull secret
			globalSecret := &corev1.Secret{}
			err = kubeSystemSecretClient.Get(context.Background(), client.ObjectKey{
				Name:      "global-pull-secret",
				Namespace: "kube-system",
			}, globalSecret)
			if tt.expectGlobalSecretExists {
				g.Expect(err).NotTo(HaveOccurred())
				if tt.validateGlobalSecret != nil {
					tt.validateGlobalSecret(t, globalSecret)
				}
			} else {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}

			// Verify original pull secret
			originalSecret := &corev1.Secret{}
			err = kubeSystemSecretClient.Get(context.Background(), client.ObjectKey{
				Name:      "original-pull-secret",
				Namespace: "kube-system",
			}, originalSecret)
			if tt.expectOriginalSecretExists {
				g.Expect(err).NotTo(HaveOccurred())
				if tt.validateOriginalSecret != nil {
					tt.validateOriginalSecret(t, originalSecret)
				}
			} else {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}

			// Verify DaemonSet
			ds := &appsv1.DaemonSet{}
			err = hcUncachedClient.Get(context.Background(), client.ObjectKey{
				Name:      "global-pull-secret-syncer",
				Namespace: "kube-system",
			}, ds)
			if tt.expectDaemonSetExists {
				g.Expect(err).NotTo(HaveOccurred())
				if tt.validateDaemonSet != nil {
					tt.validateDaemonSet(t, ds)
				}
			} else {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}
		})
	}
}

func TestValidateAdditionalPullSecret(t *testing.T) {
	tests := []struct {
		name    string
		secret  *corev1.Secret
		wantErr bool
	}{
		{
			name: "valid pull secret",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: composePullSecretBytes(map[string]string{"quay.io": validAuth}),
				},
			},
			wantErr: false,
		},
		{
			name: "missing docker config key",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					"wrong-key": composePullSecretBytes(map[string]string{"quay.io": validAuth}),
				},
			},
			wantErr: true,
		},
		{
			name: "invalid json",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte(`invalid json`),
				},
			},
			wantErr: true,
		},
		{
			name: "empty auths",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`),
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := validateAdditionalPullSecret(tt.secret)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestMergePullSecrets(t *testing.T) {
	tests := []struct {
		name             string
		originalSecret   []byte
		additionalSecret []byte
		expectedResult   []byte
		wantErr          bool
	}{
		{
			name:             "successful merge with 1 entries",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": validAuth}),
			additionalSecret: composePullSecretBytes(map[string]string{"registry2": validAuth}),
			expectedResult:   composePullSecretBytes(map[string]string{"registry1": validAuth, "registry2": validAuth}),
			wantErr:          false,
		},
		{
			name:             "successful merge with 2 entries in additional secret",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": validAuth}),
			additionalSecret: composePullSecretBytes(map[string]string{"registry2": validAuth, "registry3": validAuth}),
			expectedResult:   composePullSecretBytes(map[string]string{"registry1": validAuth, "registry2": validAuth, "registry3": validAuth}),
			wantErr:          false,
		},
		{
			name:             "successful merge with 2 entries in original secret",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": validAuth, "registry2": validAuth}),
			additionalSecret: composePullSecretBytes(map[string]string{"registry3": validAuth}),
			expectedResult:   composePullSecretBytes(map[string]string{"registry1": validAuth, "registry2": validAuth, "registry3": validAuth}),
			wantErr:          false,
		},
		{
			name:             "conflict resolution - original always wins",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": oldAuth}),
			additionalSecret: composePullSecretBytes(map[string]string{"registry1": validAuth}),
			expectedResult:   composePullSecretBytes(map[string]string{"registry1": oldAuth}),
			wantErr:          false,
		},
		{
			name:             "precedence test - original always has precedence",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": oldAuth, "registry2": oldAuth}),
			additionalSecret: composePullSecretBytes(map[string]string{"registry1": validAuth, "registry3": validAuth}),
			expectedResult:   composePullSecretBytes(map[string]string{"registry1": oldAuth, "registry2": oldAuth, "registry3": validAuth}),
			wantErr:          false,
		},
		{
			name:             "multiple conflicts - original always wins",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": oldAuth, "registry2": oldAuth}),
			additionalSecret: composePullSecretBytes(map[string]string{"registry1": validAuth, "registry2": validAuth, "registry3": validAuth}),
			expectedResult:   composePullSecretBytes(map[string]string{"registry1": oldAuth, "registry2": oldAuth, "registry3": validAuth}),
			wantErr:          false,
		},
		{
			name:             "invalid original secret",
			originalSecret:   []byte(`invalid json`),
			additionalSecret: composePullSecretBytes(map[string]string{"registry1": validAuth}),
			wantErr:          true,
		},
		{
			name:             "invalid additional secret",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": validAuth}),
			additionalSecret: []byte(`invalid json`),
			wantErr:          true,
		},
		{
			name:             "empty additional secret, invalid JSON",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": validAuth}),
			additionalSecret: []byte{},
			expectedResult:   composePullSecretBytes(map[string]string{"registry1": validAuth}),
			wantErr:          true,
		},
		{
			name:             "empty additional secret with valid JSON",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": validAuth, "registry2": validAuth}),
			additionalSecret: []byte(`{"auths":{}}`),
			expectedResult:   composePullSecretBytes(map[string]string{"registry1": validAuth, "registry2": validAuth}),
			wantErr:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result, err := mergePullSecrets(context.Background(), tt.originalSecret, tt.additionalSecret)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(result).To(Equal(tt.expectedResult))
			}
		})
	}
}

func composePullSecretBytes(auths map[string]string) []byte {
	authsJSON := make(map[string]any)
	authsEntries := make(map[string]any)
	for registry, authEntry := range auths {
		authsEntries[registry] = map[string]any{
			"auth": authEntry,
		}
	}
	authsJSON["auths"] = authsEntries
	authsBytes, err := json.Marshal(authsJSON)
	if err != nil {
		panic(err)
	}
	return authsBytes
}

func TestAdditionalPullSecretExists(t *testing.T) {
	pullSecret := composePullSecretBytes(map[string]string{"quay.io": validAuth})
	tests := []struct {
		name           string
		secretExists   bool
		expectedExists bool
		expectedSecret *corev1.Secret
		objects        []client.Object
	}{
		{
			name:           "secret exists",
			secretExists:   true,
			expectedExists: true,
			expectedSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "additional-pull-secret",
					Namespace: "kube-system",
				},
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: pullSecret,
				},
			},
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "additional-pull-secret",
						Namespace: "kube-system",
					},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: pullSecret,
					},
				},
			},
		},
		{
			name:           "secret exists but has no content",
			secretExists:   true,
			expectedExists: true,
			expectedSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "additional-pull-secret",
					Namespace: "kube-system",
				},
				Data: nil,
			},
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "additional-pull-secret",
						Namespace: "kube-system",
					},
					Data: nil,
				},
			},
		},
		{
			name:           "secret exists but has incorrect content",
			secretExists:   true,
			expectedExists: true,
			expectedSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "additional-pull-secret",
					Namespace: "kube-system",
				},
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte(`invalid json content`),
				},
			},
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "additional-pull-secret",
						Namespace: "kube-system",
					},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: []byte(`invalid json content`),
					},
				},
			},
		},
		{
			name:           "secret does not exist",
			secretExists:   false,
			expectedExists: false,
			expectedSecret: nil,
			objects:        []client.Object{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			fakeClient := fake.NewClientBuilder().WithObjects(tt.objects...).Build()
			exists, secret, err := additionalPullSecretExists(context.Background(), fakeClient)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(exists).To(Equal(tt.expectedExists))

			if tt.expectedSecret != nil {
				g.Expect(secret).NotTo(BeNil())
				g.Expect(secret.Name).To(Equal(tt.expectedSecret.Name))
				g.Expect(secret.Namespace).To(Equal(tt.expectedSecret.Namespace))
				g.Expect(secret.Data).To(Equal(tt.expectedSecret.Data))
			} else {
				g.Expect(secret).To(BeNil())
			}
		})
	}
}

func TestLabelNodesForGlobalPullSecret(t *testing.T) {
	tests := []struct {
		name            string
		nodes           []corev1.Node
		machineSets     []capiv1.MachineSet
		machines        []capiv1.Machine
		expectedLabeled []string // names of nodes that should have the label
	}{
		{
			name: "Replace-InPlace-Replace scenario: only Replace nodes should be labeled",
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "replace-node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "inplace-node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "replace-node-2",
					},
				},
			},
			machineSets: []capiv1.MachineSet{
				// First NodePool: Replace strategy (no InPlace annotations)
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "replace-machineset-1",
						Namespace: "test-namespace",
					},
					Spec: capiv1.MachineSetSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"machineset": "replace-1",
							},
						},
					},
				},
				// Second NodePool: InPlace strategy (has InPlace annotations)
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "inplace-machineset-1",
						Namespace: "test-namespace",
						Annotations: map[string]string{
							"hypershift.openshift.io/nodePoolTargetConfigVersion":  "config-hash-123",
							"hypershift.openshift.io/nodePoolCurrentConfigVersion": "config-hash-456",
						},
					},
					Spec: capiv1.MachineSetSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"machineset": "inplace-1",
							},
						},
					},
				},
				// Third NodePool: Replace strategy (no InPlace annotations) - this should work after InPlace
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "replace-machineset-2",
						Namespace: "test-namespace",
					},
					Spec: capiv1.MachineSetSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"machineset": "replace-2",
							},
						},
					},
				},
			},
			machines: []capiv1.Machine{
				// Machine for first Replace NodePool
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "replace-machine-1",
						Namespace: "test-namespace",
						Labels: map[string]string{
							"machineset": "replace-1",
						},
					},
					Status: capiv1.MachineStatus{
						NodeRef: &corev1.ObjectReference{
							Name: "replace-node-1",
						},
					},
				},
				// Machine for InPlace NodePool
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "inplace-machine-1",
						Namespace: "test-namespace",
						Labels: map[string]string{
							"machineset": "inplace-1",
						},
					},
					Status: capiv1.MachineStatus{
						NodeRef: &corev1.ObjectReference{
							Name: "inplace-node-1",
						},
					},
				},
				// Machine for second Replace NodePool (created after InPlace)
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "replace-machine-2",
						Namespace: "test-namespace",
						Labels: map[string]string{
							"machineset": "replace-2",
						},
					},
					Status: capiv1.MachineStatus{
						NodeRef: &corev1.ObjectReference{
							Name: "replace-node-2",
						},
					},
				},
			},
			expectedLabeled: []string{"replace-node-1", "replace-node-2"}, // Both Replace nodes should be labeled
		},
		{
			name: "Only InPlace NodePools: no nodes should be labeled",
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "inplace-node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "inplace-node-2",
					},
				},
			},
			machineSets: []capiv1.MachineSet{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "inplace-machineset-1",
						Namespace: "test-namespace",
						Annotations: map[string]string{
							"hypershift.openshift.io/nodePoolTargetConfigVersion": "config-hash-123",
						},
					},
					Spec: capiv1.MachineSetSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"machineset": "inplace-1",
							},
						},
					},
				},
			},
			machines: []capiv1.Machine{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "inplace-machine-1",
						Namespace: "test-namespace",
						Labels: map[string]string{
							"machineset": "inplace-1",
						},
					},
					Status: capiv1.MachineStatus{
						NodeRef: &corev1.ObjectReference{
							Name: "inplace-node-1",
						},
					},
				},
			},
			expectedLabeled: []string{}, // No nodes should be labeled
		},
		{
			name: "Only Replace NodePools: all nodes should be labeled",
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "replace-node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "replace-node-2",
					},
				},
			},
			machineSets: []capiv1.MachineSet{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "replace-machineset-1",
						Namespace: "test-namespace",
						// No InPlace annotations
					},
					Spec: capiv1.MachineSetSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"machineset": "replace-1",
							},
						},
					},
				},
			},
			machines: []capiv1.Machine{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "replace-machine-1",
						Namespace: "test-namespace",
						Labels: map[string]string{
							"machineset": "replace-1",
						},
					},
					Status: capiv1.MachineStatus{
						NodeRef: &corev1.ObjectReference{
							Name: "replace-node-1",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "replace-machine-2",
						Namespace: "test-namespace",
						Labels: map[string]string{
							"machineset": "replace-1",
						},
					},
					Status: capiv1.MachineStatus{
						NodeRef: &corev1.ObjectReference{
							Name: "replace-node-2",
						},
					},
				},
			},
			expectedLabeled: []string{"replace-node-1", "replace-node-2"}, // All Replace nodes should be labeled
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Create runtime scheme and add required types
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			_ = capiv1.AddToScheme(scheme)

			// Convert to client.Object slices
			var objects []client.Object
			for i := range tt.nodes {
				objects = append(objects, &tt.nodes[i])
			}
			for i := range tt.machineSets {
				objects = append(objects, &tt.machineSets[i])
			}
			for i := range tt.machines {
				objects = append(objects, &tt.machines[i])
			}

			// Create fake clients
			cpClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			kubeSystemSecretClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			nodeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			hcUncachedClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()

			// Create reconciler
			reconciler := &Reconciler{
				cpClient:               cpClient,
				kubeSystemSecretClient: kubeSystemSecretClient,
				nodeClient:             nodeClient,
				hcUncachedClient:       hcUncachedClient,
				hcpNamespace:           "test-namespace",
			}

			// Execute the function under test
			err := reconciler.labelNodesForGlobalPullSecret(context.Background())
			g.Expect(err).NotTo(HaveOccurred())

			// Check that only expected nodes have the label
			nodeList := &corev1.NodeList{}
			err = nodeClient.List(context.Background(), nodeList)
			g.Expect(err).NotTo(HaveOccurred())

			labeledNodes := make(map[string]bool)
			for _, node := range nodeList.Items {
				if node.Labels != nil && node.Labels[globalPSLabelKey] == "true" {
					labeledNodes[node.Name] = true
				}
			}

			// Verify expected nodes are labeled
			for _, expectedNode := range tt.expectedLabeled {
				g.Expect(labeledNodes[expectedNode]).To(BeTrue(), "Node %s should be labeled but wasn't", expectedNode)
			}

			// Verify no unexpected nodes are labeled
			g.Expect(len(labeledNodes)).To(Equal(len(tt.expectedLabeled)), "Number of labeled nodes doesn't match expected")
		})
	}
}
