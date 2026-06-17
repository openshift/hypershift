package reencryption

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	kasaescbc "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas/kms"
	hccoapi "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/secretencryption"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	testNamespace = "test-hcp-namespace"
	testHCPName   = "test-hcp"
)

var (
	testScheme = func() *runtime.Scheme {
		s := runtime.NewScheme()
		_ = clientgoscheme.AddToScheme(s)
		_ = hyperv1.AddToScheme(s)
		return s
	}()

	fixedTime = time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
)

// fakeMigrator implements the Migrator interface for testing.
type fakeMigrator struct {
	migrations      map[string]*fakeMigrationState
	discoveryErrors map[schema.GroupResource]error
}

type fakeMigrationState struct {
	finished bool
	result   error
	ts       time.Time
}

func newFakeMigrator() *fakeMigrator {
	return &fakeMigrator{
		migrations:      make(map[string]*fakeMigrationState),
		discoveryErrors: make(map[schema.GroupResource]error),
	}
}

func (f *fakeMigrator) EnsureMigration(gr schema.GroupResource, writeKey string) (finished bool, result error, ts time.Time, err error) {
	if discoveryErr, ok := f.discoveryErrors[gr]; ok {
		return false, nil, time.Time{}, discoveryErr
	}
	key := fmt.Sprintf("%s/%s", gr.String(), writeKey)
	state, exists := f.migrations[key]
	if !exists {
		f.migrations[key] = &fakeMigrationState{finished: false}
		return false, nil, time.Time{}, nil
	}
	return state.finished, state.result, state.ts, nil
}

func (f *fakeMigrator) PruneMigration(gr schema.GroupResource) error {
	for k := range f.migrations {
		if strings.HasPrefix(k, gr.String()) {
			delete(f.migrations, k)
		}
	}
	return nil
}

func (f *fakeMigrator) AddEventHandler(handler cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	return nil, nil
}

func (f *fakeMigrator) HasSynced() bool {
	return true
}

func (f *fakeMigrator) completeMigration(gr schema.GroupResource, writeKey string) {
	key := fmt.Sprintf("%s/%s", gr.String(), writeKey)
	f.migrations[key] = &fakeMigrationState{
		finished: true,
		result:   nil,
		ts:       fixedTime,
	}
}

func (f *fakeMigrator) failMigration(gr schema.GroupResource, writeKey string, err error) {
	key := fmt.Sprintf("%s/%s", gr.String(), writeKey)
	f.migrations[key] = &fakeMigrationState{
		finished: true,
		result:   err,
		ts:       fixedTime,
	}
}

func (f *fakeMigrator) completeAll(resources []schema.GroupResource, writeKey string) {
	for _, gr := range resources {
		f.completeMigration(gr, writeKey)
	}
}

// Test helpers.

func newHCP(opts ...func(*hyperv1.HostedControlPlane)) *hyperv1.HostedControlPlane {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:       testHCPName,
			Namespace:  testNamespace,
			Generation: 1,
		},
	}
	for _, opt := range opts {
		opt(hcp)
	}
	return hcp
}

func withAESCBCEncryption(secretName string) func(*hyperv1.HostedControlPlane) {
	return func(hcp *hyperv1.HostedControlPlane) {
		hcp.Spec.SecretEncryption = &hyperv1.SecretEncryptionSpec{
			Type: hyperv1.AESCBC,
			AESCBC: &hyperv1.AESCBCSpec{
				ActiveKey: corev1.LocalObjectReference{Name: secretName},
			},
		}
	}
}

func withKMSEncryption() func(*hyperv1.HostedControlPlane) {
	return func(hcp *hyperv1.HostedControlPlane) {
		hcp.Spec.SecretEncryption = &hyperv1.SecretEncryptionSpec{
			Type: hyperv1.KMS,
			KMS: &hyperv1.KMSSpec{
				Provider: hyperv1.AWS,
				AWS: &hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789012:key/test-key-1"},
					Region:    "us-east-1",
				},
			},
		}
	}
}

func withExternalOIDC() func(*hyperv1.HostedControlPlane) {
	return func(hcp *hyperv1.HostedControlPlane) {
		if hcp.Spec.Configuration == nil {
			hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{}
		}
		hcp.Spec.Configuration.Authentication = &configv1.AuthenticationSpec{
			Type: configv1.AuthenticationTypeOIDC,
		}
	}
}

func withActiveKey(ks *hyperv1.SecretEncryptionKeyStatus) func(*hyperv1.HostedControlPlane) {
	return func(hcp *hyperv1.HostedControlPlane) {
		hcp.Status.SecretEncryption.ActiveKey = *ks.DeepCopy()
	}
}

func withTargetKey(ks *hyperv1.SecretEncryptionKeyStatus) func(*hyperv1.HostedControlPlane) {
	return func(hcp *hyperv1.HostedControlPlane) {
		hcp.Status.SecretEncryption.TargetKey = *ks.DeepCopy()
	}
}

func withHistory(entries ...hyperv1.EncryptionMigrationHistory) func(*hyperv1.HostedControlPlane) {
	return func(hcp *hyperv1.HostedControlPlane) {
		hcp.Status.SecretEncryption.History = entries
	}
}

func aescbcKeyStatus(secretName, dataHash string) *hyperv1.SecretEncryptionKeyStatus {
	return secretencryption.KeyStatusFromAESCBCSpec(secretName, dataHash)
}

func awsKeyStatus(arn string) *hyperv1.SecretEncryptionKeyStatus {
	return secretencryption.KeyStatusFromAWSSpec(hyperv1.AWSKMSKeyEntry{ARN: arn})
}

func aescbcKeySecret(name, namespace, keyData string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			hyperv1.AESCBCKeySecretKey: []byte(keyData),
		},
	}
}

// aescbcProviderKeyName computes the provider key name that the CPO generates
// for an AESCBC key. This must match the naming in kas/aescbc.go.
func aescbcProviderKeyName(keyData string) string {
	name, _ := kasaescbc.AESCBCKeyName([]byte(keyData))
	return name
}

func awsProviderKeyName(arn string) string {
	name, _ := kms.AWSKMSProviderName(arn)
	return name
}

// encryptionConfigSecret builds the kas-secret-encryption-config Secret with
// a config.yaml field containing a YAML-encoded EncryptionConfiguration.
// writeKeyName is the first (write) provider key; readKeyName (if non-empty) is
// the second (read-only) provider key.
func encryptionConfigSecret(namespace string, encType hyperv1.SecretEncryptionType, writeKeyName, readKeyName string) *corev1.Secret {
	var cfg apiserverv1.EncryptionConfiguration
	cfg.TypeMeta = metav1.TypeMeta{
		APIVersion: apiserverv1.SchemeGroupVersion.String(),
		Kind:       "EncryptionConfiguration",
	}

	var providers []apiserverv1.ProviderConfiguration

	switch encType {
	case hyperv1.AESCBC:
		keys := []apiserverv1.Key{
			{Name: writeKeyName, Secret: base64.StdEncoding.EncodeToString([]byte("dummy"))},
		}
		if readKeyName != "" {
			keys = append(keys, apiserverv1.Key{Name: readKeyName, Secret: base64.StdEncoding.EncodeToString([]byte("dummy"))})
		}
		providers = append(providers,
			apiserverv1.ProviderConfiguration{AESCBC: &apiserverv1.AESConfiguration{Keys: keys}},
			apiserverv1.ProviderConfiguration{Identity: &apiserverv1.IdentityConfiguration{}},
		)
		cfg.Resources = []apiserverv1.ResourceConfiguration{
			{Resources: []string{"secrets"}, Providers: providers},
		}

	case hyperv1.KMS:
		providers = append(providers, apiserverv1.ProviderConfiguration{
			KMS: &apiserverv1.KMSConfiguration{
				Name:       writeKeyName,
				APIVersion: "v2",
				Endpoint:   "unix:///var/run/awskmsactive.sock",
				Timeout:    &metav1.Duration{Duration: 35 * time.Second},
			},
		})
		if readKeyName != "" {
			providers = append(providers, apiserverv1.ProviderConfiguration{
				KMS: &apiserverv1.KMSConfiguration{
					Name:       readKeyName,
					APIVersion: "v2",
					Endpoint:   "unix:///var/run/awskmsbackup.sock",
					Timeout:    &metav1.Duration{Duration: 35 * time.Second},
				},
			})
		}
		providers = append(providers, apiserverv1.ProviderConfiguration{Identity: &apiserverv1.IdentityConfiguration{}})
		cfg.Resources = []apiserverv1.ResourceConfiguration{
			{Resources: config.KMSEncryptedObjects(), Providers: providers},
		}
	}

	buf := bytes.NewBuffer(nil)
	if err := hccoapi.YamlSerializer.Encode(&cfg, buf); err != nil {
		panic(fmt.Sprintf("failed to encode EncryptionConfiguration: %v", err))
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kas-secret-encryption-config",
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"config.yaml": buf.Bytes(),
		},
	}
}

func convergedKASDeployment(namespace string) *appsv1.Deployment {
	replicas := int32(3)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "kube-apiserver",
			Namespace:  namespace,
			Generation: 1,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration:  1,
			Replicas:            3,
			UpdatedReplicas:     3,
			ReadyReplicas:       3,
			AvailableReplicas:   3,
			UnavailableReplicas: 0,
		},
	}
}

func rollingKASDeployment(namespace string) *appsv1.Deployment {
	replicas := int32(3)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "kube-apiserver",
			Namespace:  namespace,
			Generation: 2,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration:  1,
			Replicas:            3,
			UpdatedReplicas:     1,
			ReadyReplicas:       2,
			AvailableReplicas:   2,
			UnavailableReplicas: 1,
		},
	}
}

func newReconciler(cpClient, guestClient client.Client, migrator *fakeMigrator) *Reconciler {
	return &Reconciler{
		cpClient:     cpClient,
		guestClient:  guestClient,
		hcpName:      testHCPName,
		hcpNamespace: testNamespace,
		migrator:     migrator,
		now:          func() time.Time { return fixedTime },
	}
}

func buildCPClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(objs...).
		WithStatusSubresource(&hyperv1.HostedControlPlane{}).
		Build()
}

func getHCP(ctx context.Context, g Gomega, cl client.Client) *hyperv1.HostedControlPlane {
	hcp := &hyperv1.HostedControlPlane{}
	g.Expect(cl.Get(ctx, types.NamespacedName{Name: testHCPName, Namespace: testNamespace}, hcp)).To(Succeed())
	return hcp
}

func TestReconcile(t *testing.T) {
	tests := []struct {
		name         string
		cpObjects    []client.Object
		migrator     func() *fakeMigrator
		expectResult reconcile.Result
		expectError  bool
		validate     func(*testing.T, Gomega, client.Client, *fakeMigrator)
	}{
		{
			name: "When encryption is not configured it should remove the condition and clear targetKey",
			cpObjects: []client.Object{
				newHCP(), // no encryption spec
				convergedKASDeployment(testNamespace),
			},
			migrator: newFakeMigrator,
			validate: func(t *testing.T, g Gomega, cl client.Client, _ *fakeMigrator) {
				hcp := getHCP(context.Background(), g, cl)
				cond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))
				g.Expect(cond).To(BeNil(), "condition should be removed when encryption is not configured")
			},
		},
		{
			name: "When encryption is configured with AESCBC and no active key in status it should initialize active key",
			cpObjects: []client.Object{
				newHCP(withAESCBCEncryption("aescbc-key-1")),
				aescbcKeySecret("aescbc-key-1", testNamespace, "test-key-data-1"),
				convergedKASDeployment(testNamespace),
			},
			migrator: newFakeMigrator,
			validate: func(t *testing.T, g Gomega, cl client.Client, _ *fakeMigrator) {
				hcp := getHCP(context.Background(), g, cl)
				g.Expect(hcp.Status.SecretEncryption.ActiveKey.Provider).ToNot(BeEmpty())
				g.Expect(hcp.Status.SecretEncryption.ActiveKey.Provider).To(Equal(hyperv1.SecretEncryptionProviderAESCBC))
				g.Expect(hcp.Status.SecretEncryption.ActiveKey.AESCBC.DataHash).ToNot(BeEmpty())
				g.Expect(hcp.Status.SecretEncryption.ActiveKey.AESCBC.Secret.Name).To(Equal("aescbc-key-1"))
				dh := secretencryption.DataHash([]byte("test-key-data-1"))
				g.Expect(hcp.Status.SecretEncryption.ActiveKey.AESCBC.DataHash).To(Equal(dh))
				g.Expect(hcp.Status.SecretEncryption.TargetKey.Provider).To(BeEmpty())

				cond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Reason).To(Equal(hyperv1.ReEncryptionCompletedReason))
			},
		},
		{
			name: "When encryption key is already up to date it should remain in steady state",
			cpObjects: func() []client.Object {
				dataHash := secretencryption.DataHash([]byte("test-key-data-1"))
				return []client.Object{
					newHCP(
						withAESCBCEncryption("aescbc-key-1"),
						withActiveKey(aescbcKeyStatus("aescbc-key-1", dataHash)),
					),
					aescbcKeySecret("aescbc-key-1", testNamespace, "test-key-data-1"),
					convergedKASDeployment(testNamespace),
				}
			}(),
			migrator: newFakeMigrator,
			validate: func(t *testing.T, g Gomega, cl client.Client, _ *fakeMigrator) {
				hcp := getHCP(context.Background(), g, cl)
				g.Expect(hcp.Status.SecretEncryption.TargetKey.Provider).To(BeEmpty())
			},
		},
		{
			name: "When AESCBC key data changes it should start a new rotation",
			cpObjects: func() []client.Object {
				oldHash := secretencryption.DataHash([]byte("old-key-data"))
				return []client.Object{
					newHCP(
						withAESCBCEncryption("aescbc-key-1"),
						withActiveKey(aescbcKeyStatus("aescbc-key-1", oldHash)),
					),
					aescbcKeySecret("aescbc-key-1", testNamespace, "new-key-data"),
					convergedKASDeployment(testNamespace),
				}
			}(),
			migrator: newFakeMigrator,
			validate: func(t *testing.T, g Gomega, cl client.Client, _ *fakeMigrator) {
				hcp := getHCP(context.Background(), g, cl)
				g.Expect(hcp.Status.SecretEncryption.TargetKey.Provider).ToNot(BeEmpty())
				g.Expect(hcp.Status.SecretEncryption.TargetKey.Provider).To(Equal(hyperv1.SecretEncryptionProviderAESCBC))
				newHash := secretencryption.DataHash([]byte("new-key-data"))
				g.Expect(hcp.Status.SecretEncryption.TargetKey.AESCBC.DataHash).To(Equal(newHash))

				g.Expect(hcp.Status.SecretEncryption.History).To(HaveLen(1))
				g.Expect(hcp.Status.SecretEncryption.History[0].State).To(Equal(hyperv1.EncryptionMigrationStateReadOnlyDeploy))
				g.Expect(hcp.Status.SecretEncryption.History[0].CompletionTime.IsZero()).To(BeTrue())

				cond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(hyperv1.ReadOnlyRolloutInProgressReason))
			},
		},
		{
			name: "When in ReadOnlyDeploy phase and KAS is not converged it should wait",
			cpObjects: func() []client.Object {
				oldHash := secretencryption.DataHash([]byte("old-key-data"))
				newHash := secretencryption.DataHash([]byte("new-key-data"))
				oldKS := aescbcKeyStatus("aescbc-key-1", oldHash)
				newKS := aescbcKeyStatus("aescbc-key-1", newHash)
				return []client.Object{
					newHCP(
						withAESCBCEncryption("aescbc-key-1"),
						withActiveKey(oldKS),
						withTargetKey(newKS),
						withHistory(hyperv1.EncryptionMigrationHistory{
							From:        secretencryption.KeyReferenceFromStatus(oldKS),
							To:          secretencryption.KeyReferenceFromStatus(newKS),
							State:       hyperv1.EncryptionMigrationStateReadOnlyDeploy,
							StartedTime: metav1.Time{Time: fixedTime},
						}),
					),
					aescbcKeySecret("aescbc-key-1", testNamespace, "new-key-data"),
					rollingKASDeployment(testNamespace),
					// Target key is read-only (second), old key is write (first).
					encryptionConfigSecret(testNamespace, hyperv1.AESCBC,
						aescbcProviderKeyName("old-key-data"),
						aescbcProviderKeyName("new-key-data")),
				}
			}(),
			migrator:     newFakeMigrator,
			expectResult: reconcile.Result{RequeueAfter: 30 * time.Second},
			validate: func(t *testing.T, g Gomega, cl client.Client, _ *fakeMigrator) {
				hcp := getHCP(context.Background(), g, cl)
				g.Expect(hcp.Status.SecretEncryption.History[0].State).To(Equal(hyperv1.EncryptionMigrationStateReadOnlyDeploy))

				cond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(hyperv1.ReEncryptionWaitingForKASReason))
			},
		},
		{
			name: "When in ReadOnlyDeploy phase and KAS is converged it should advance to WritePromote",
			cpObjects: func() []client.Object {
				oldHash := secretencryption.DataHash([]byte("old-key-data"))
				newHash := secretencryption.DataHash([]byte("new-key-data"))
				oldKS := aescbcKeyStatus("aescbc-key-1", oldHash)
				newKS := aescbcKeyStatus("aescbc-key-1", newHash)
				return []client.Object{
					newHCP(
						withAESCBCEncryption("aescbc-key-1"),
						withActiveKey(oldKS),
						withTargetKey(newKS),
						withHistory(hyperv1.EncryptionMigrationHistory{
							From:        secretencryption.KeyReferenceFromStatus(oldKS),
							To:          secretencryption.KeyReferenceFromStatus(newKS),
							State:       hyperv1.EncryptionMigrationStateReadOnlyDeploy,
							StartedTime: metav1.Time{Time: fixedTime},
						}),
					),
					aescbcKeySecret("aescbc-key-1", testNamespace, "new-key-data"),
					convergedKASDeployment(testNamespace),
					// Target key is read-only (second), old key is write (first).
					encryptionConfigSecret(testNamespace, hyperv1.AESCBC,
						aescbcProviderKeyName("old-key-data"),
						aescbcProviderKeyName("new-key-data")),
				}
			}(),
			migrator: newFakeMigrator,
			validate: func(t *testing.T, g Gomega, cl client.Client, _ *fakeMigrator) {
				hcp := getHCP(context.Background(), g, cl)
				g.Expect(hcp.Status.SecretEncryption.History[0].State).To(Equal(hyperv1.EncryptionMigrationStateWritePromote))

				cond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(hyperv1.WritePromotionInProgressReason))
			},
		},
		{
			name: "When in WritePromote phase and KAS is converged it should advance to Migrating",
			cpObjects: func() []client.Object {
				oldHash := secretencryption.DataHash([]byte("old-key-data"))
				newHash := secretencryption.DataHash([]byte("new-key-data"))
				oldKS := aescbcKeyStatus("aescbc-key-1", oldHash)
				newKS := aescbcKeyStatus("aescbc-key-1", newHash)
				return []client.Object{
					newHCP(
						withAESCBCEncryption("aescbc-key-1"),
						withActiveKey(oldKS),
						withTargetKey(newKS),
						withHistory(hyperv1.EncryptionMigrationHistory{
							From:        secretencryption.KeyReferenceFromStatus(oldKS),
							To:          secretencryption.KeyReferenceFromStatus(newKS),
							State:       hyperv1.EncryptionMigrationStateWritePromote,
							StartedTime: metav1.Time{Time: fixedTime},
						}),
					),
					aescbcKeySecret("aescbc-key-1", testNamespace, "new-key-data"),
					convergedKASDeployment(testNamespace),
					// Target key promoted to write (first), old key demoted to read-only (second).
					encryptionConfigSecret(testNamespace, hyperv1.AESCBC,
						aescbcProviderKeyName("new-key-data"),
						aescbcProviderKeyName("old-key-data")),
				}
			}(),
			migrator: newFakeMigrator,
			// The controller derives Migrating from observable state (target key is write + KAS converged)
			// and immediately starts migrations which are not yet finished, so it requeues.
			expectResult: reconcile.Result{RequeueAfter: 30 * time.Second},
			validate: func(t *testing.T, g Gomega, cl client.Client, _ *fakeMigrator) {
				hcp := getHCP(context.Background(), g, cl)
				g.Expect(hcp.Status.SecretEncryption.History[0].State).To(Equal(hyperv1.EncryptionMigrationStateMigrating))

				cond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(hyperv1.ReEncryptionInProgressReason))
			},
		},
		{
			name: "When in Migrating phase and migrations are in progress it should wait",
			cpObjects: func() []client.Object {
				oldHash := secretencryption.DataHash([]byte("old-key-data"))
				newHash := secretencryption.DataHash([]byte("new-key-data"))
				oldKS := aescbcKeyStatus("aescbc-key-1", oldHash)
				newKS := aescbcKeyStatus("aescbc-key-1", newHash)
				return []client.Object{
					newHCP(
						withAESCBCEncryption("aescbc-key-1"),
						withActiveKey(oldKS),
						withTargetKey(newKS),
						withHistory(hyperv1.EncryptionMigrationHistory{
							From:        secretencryption.KeyReferenceFromStatus(oldKS),
							To:          secretencryption.KeyReferenceFromStatus(newKS),
							State:       hyperv1.EncryptionMigrationStateMigrating,
							StartedTime: metav1.Time{Time: fixedTime},
						}),
					),
					aescbcKeySecret("aescbc-key-1", testNamespace, "new-key-data"),
					convergedKASDeployment(testNamespace),
					// Target key is write (first), old key is read-only (second).
					encryptionConfigSecret(testNamespace, hyperv1.AESCBC,
						aescbcProviderKeyName("new-key-data"),
						aescbcProviderKeyName("old-key-data")),
				}
			}(),
			migrator:     newFakeMigrator,
			expectResult: reconcile.Result{RequeueAfter: 30 * time.Second},
			validate: func(t *testing.T, g Gomega, cl client.Client, _ *fakeMigrator) {
				hcp := getHCP(context.Background(), g, cl)
				g.Expect(hcp.Status.SecretEncryption.History[0].State).To(Equal(hyperv1.EncryptionMigrationStateMigrating))
			},
		},
		{
			name: "When in Migrating phase and all AESCBC migrations complete it should complete rotation",
			cpObjects: func() []client.Object {
				oldHash := secretencryption.DataHash([]byte("old-key-data"))
				newHash := secretencryption.DataHash([]byte("new-key-data"))
				oldKS := aescbcKeyStatus("aescbc-key-1", oldHash)
				newKS := aescbcKeyStatus("aescbc-key-1", newHash)
				return []client.Object{
					newHCP(
						withAESCBCEncryption("aescbc-key-1"),
						withActiveKey(oldKS),
						withTargetKey(newKS),
						withHistory(hyperv1.EncryptionMigrationHistory{
							From:        secretencryption.KeyReferenceFromStatus(oldKS),
							To:          secretencryption.KeyReferenceFromStatus(newKS),
							State:       hyperv1.EncryptionMigrationStateMigrating,
							StartedTime: metav1.Time{Time: fixedTime},
						}),
					),
					aescbcKeySecret("aescbc-key-1", testNamespace, "new-key-data"),
					convergedKASDeployment(testNamespace),
					// Target key is write (first), old key is read-only (second).
					encryptionConfigSecret(testNamespace, hyperv1.AESCBC,
						aescbcProviderKeyName("new-key-data"),
						aescbcProviderKeyName("old-key-data")),
				}
			}(),
			migrator: func() *fakeMigrator {
				m := newFakeMigrator()
				newHash := secretencryption.DataHash([]byte("new-key-data"))
				fp := secretencryption.FingerprintAESCBCKey("aescbc-key-1", newHash)
				writeKey := fmt.Sprintf("encryption-key-%s", fp)
				// AESCBC only encrypts secrets.
				m.completeMigration(schema.GroupResource{Resource: "secrets"}, writeKey)
				return m
			},
			validate: func(t *testing.T, g Gomega, cl client.Client, _ *fakeMigrator) {
				hcp := getHCP(context.Background(), g, cl)
				g.Expect(hcp.Status.SecretEncryption.TargetKey.Provider).To(BeEmpty())
				g.Expect(hcp.Status.SecretEncryption.ActiveKey.Provider).ToNot(BeEmpty())
				g.Expect(hcp.Status.SecretEncryption.ActiveKey.Provider).To(Equal(hyperv1.SecretEncryptionProviderAESCBC))
				newHash := secretencryption.DataHash([]byte("new-key-data"))
				g.Expect(hcp.Status.SecretEncryption.ActiveKey.AESCBC.DataHash).To(Equal(newHash))

				g.Expect(hcp.Status.SecretEncryption.History[0].State).To(Equal(hyperv1.EncryptionMigrationStateCompleted))
				g.Expect(hcp.Status.SecretEncryption.History[0].CompletionTime.IsZero()).To(BeFalse())

				cond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Reason).To(Equal(hyperv1.ReEncryptionCompletedReason))
			},
		},
		{
			name: "When in Migrating phase and a migration fails it should set failed condition",
			cpObjects: func() []client.Object {
				oldHash := secretencryption.DataHash([]byte("old-key-data"))
				newHash := secretencryption.DataHash([]byte("new-key-data"))
				oldKS := aescbcKeyStatus("aescbc-key-1", oldHash)
				newKS := aescbcKeyStatus("aescbc-key-1", newHash)
				return []client.Object{
					newHCP(
						withAESCBCEncryption("aescbc-key-1"),
						withActiveKey(oldKS),
						withTargetKey(newKS),
						withHistory(hyperv1.EncryptionMigrationHistory{
							From:        secretencryption.KeyReferenceFromStatus(oldKS),
							To:          secretencryption.KeyReferenceFromStatus(newKS),
							State:       hyperv1.EncryptionMigrationStateMigrating,
							StartedTime: metav1.Time{Time: fixedTime},
						}),
					),
					aescbcKeySecret("aescbc-key-1", testNamespace, "new-key-data"),
					convergedKASDeployment(testNamespace),
					// Target key is write (first), old key is read-only (second).
					encryptionConfigSecret(testNamespace, hyperv1.AESCBC,
						aescbcProviderKeyName("new-key-data"),
						aescbcProviderKeyName("old-key-data")),
				}
			}(),
			migrator: func() *fakeMigrator {
				m := newFakeMigrator()
				newHash := secretencryption.DataHash([]byte("new-key-data"))
				fp := secretencryption.FingerprintAESCBCKey("aescbc-key-1", newHash)
				writeKey := fmt.Sprintf("encryption-key-%s", fp)
				m.failMigration(schema.GroupResource{Resource: "secrets"}, writeKey, fmt.Errorf("migration failed: timeout"))
				return m
			},
			expectResult: reconcile.Result{RequeueAfter: 60 * time.Second},
			validate: func(t *testing.T, g Gomega, cl client.Client, _ *fakeMigrator) {
				hcp := getHCP(context.Background(), g, cl)
				g.Expect(hcp.Status.SecretEncryption.History[0].State).To(Equal(hyperv1.EncryptionMigrationStateMigrating))

				cond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(hyperv1.ReEncryptionFailedReason))
			},
		},
		{
			name: "When using AWS KMS and key ARN changes it should start rotation with 5 encrypted resources",
			cpObjects: func() []client.Object {
				oldKS := awsKeyStatus("arn:aws:kms:us-east-1:123456789012:key/old-key")
				return []client.Object{
					newHCP(
						withKMSEncryption(),
						withActiveKey(oldKS),
					),
					convergedKASDeployment(testNamespace),
				}
			}(),
			migrator: newFakeMigrator,
			validate: func(t *testing.T, g Gomega, cl client.Client, _ *fakeMigrator) {
				hcp := getHCP(context.Background(), g, cl)
				g.Expect(hcp.Status.SecretEncryption.TargetKey.Provider).ToNot(BeEmpty())
				g.Expect(hcp.Status.SecretEncryption.TargetKey.Provider).To(Equal(hyperv1.SecretEncryptionProviderAWS))
				g.Expect(hcp.Status.SecretEncryption.TargetKey.AWS.ARN).To(Equal("arn:aws:kms:us-east-1:123456789012:key/test-key-1"))

				g.Expect(hcp.Status.SecretEncryption.History).To(HaveLen(1))
				g.Expect(hcp.Status.SecretEncryption.History[0].State).To(Equal(hyperv1.EncryptionMigrationStateReadOnlyDeploy))
			},
		},
		{
			name: "When in Migrating phase with KMS and all 5 migrations complete it should complete rotation",
			cpObjects: func() []client.Object {
				oldKS := awsKeyStatus("arn:aws:kms:us-east-1:123456789012:key/old-key")
				newKS := awsKeyStatus("arn:aws:kms:us-east-1:123456789012:key/test-key-1")
				return []client.Object{
					newHCP(
						withKMSEncryption(),
						withActiveKey(oldKS),
						withTargetKey(newKS),
						withHistory(hyperv1.EncryptionMigrationHistory{
							From:        secretencryption.KeyReferenceFromStatus(oldKS),
							To:          secretencryption.KeyReferenceFromStatus(newKS),
							State:       hyperv1.EncryptionMigrationStateMigrating,
							StartedTime: metav1.Time{Time: fixedTime},
						}),
					),
					convergedKASDeployment(testNamespace),
					// Target key is write (first KMS provider), old key is read-only (second).
					encryptionConfigSecret(testNamespace, hyperv1.KMS,
						awsProviderKeyName("arn:aws:kms:us-east-1:123456789012:key/test-key-1"),
						awsProviderKeyName("arn:aws:kms:us-east-1:123456789012:key/old-key")),
				}
			}(),
			migrator: func() *fakeMigrator {
				m := newFakeMigrator()
				fp := secretencryption.FingerprintAWSKMSKey("arn:aws:kms:us-east-1:123456789012:key/test-key-1")
				writeKey := fmt.Sprintf("encryption-key-%s", fp)
				resources := []schema.GroupResource{
					{Resource: "secrets"},
					{Resource: "configmaps"},
					{Resource: "routes", Group: "route.openshift.io"},
					{Resource: "oauthaccesstokens", Group: "oauth.openshift.io"},
					{Resource: "oauthauthorizetokens", Group: "oauth.openshift.io"},
				}
				m.completeAll(resources, writeKey)
				return m
			},
			validate: func(t *testing.T, g Gomega, cl client.Client, _ *fakeMigrator) {
				hcp := getHCP(context.Background(), g, cl)
				g.Expect(hcp.Status.SecretEncryption.TargetKey.Provider).To(BeEmpty())
				g.Expect(hcp.Status.SecretEncryption.ActiveKey.Provider).To(Equal(hyperv1.SecretEncryptionProviderAWS))
				g.Expect(hcp.Status.SecretEncryption.ActiveKey.AWS.ARN).To(Equal("arn:aws:kms:us-east-1:123456789012:key/test-key-1"))

				g.Expect(hcp.Status.SecretEncryption.History[0].State).To(Equal(hyperv1.EncryptionMigrationStateCompleted))

				cond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			},
		},
		{
			name: "When spec key changes mid-rotation it should let current rotation complete first",
			cpObjects: func() []client.Object {
				oldHash := secretencryption.DataHash([]byte("old-key-data"))
				midHash := secretencryption.DataHash([]byte("mid-key-data"))
				oldKS := aescbcKeyStatus("aescbc-key-1", oldHash)
				midKS := aescbcKeyStatus("aescbc-key-2", midHash)

				hcp := newHCP(
					withActiveKey(oldKS),
					withTargetKey(midKS),
					withHistory(hyperv1.EncryptionMigrationHistory{
						From:        secretencryption.KeyReferenceFromStatus(oldKS),
						To:          secretencryption.KeyReferenceFromStatus(midKS),
						State:       hyperv1.EncryptionMigrationStateWritePromote,
						StartedTime: metav1.Time{Time: fixedTime},
					}),
				)
				// Spec now points to a third key (simulates user changing spec again).
				hcp.Spec.SecretEncryption = &hyperv1.SecretEncryptionSpec{
					Type: hyperv1.AESCBC,
					AESCBC: &hyperv1.AESCBCSpec{
						ActiveKey: corev1.LocalObjectReference{Name: "aescbc-key-3"},
					},
				}
				return []client.Object{
					hcp,
					aescbcKeySecret("aescbc-key-2", testNamespace, "mid-key-data"),
					aescbcKeySecret("aescbc-key-3", testNamespace, "third-key-data"),
					convergedKASDeployment(testNamespace),
					// Target (mid) key is write (first), old key is read-only (second).
					encryptionConfigSecret(testNamespace, hyperv1.AESCBC,
						aescbcProviderKeyName("mid-key-data"),
						aescbcProviderKeyName("old-key-data")),
				}
			}(),
			migrator: newFakeMigrator,
			// The controller derives Migrating from observable state (target key is write + KAS converged)
			// and immediately starts migrations which are not yet finished, so it requeues.
			expectResult: reconcile.Result{RequeueAfter: 30 * time.Second},
			validate: func(t *testing.T, g Gomega, cl client.Client, _ *fakeMigrator) {
				hcp := getHCP(context.Background(), g, cl)
				// The current rotation should continue: WritePromote -> Migrating.
				g.Expect(hcp.Status.SecretEncryption.History[0].State).To(Equal(hyperv1.EncryptionMigrationStateMigrating))
				// TargetKey should remain the mid-rotation key, not the new spec key.
				g.Expect(hcp.Status.SecretEncryption.TargetKey.Provider).ToNot(BeEmpty())
			},
		},
		{
			name: "When in Migrating phase with KMS and some resources are not discoverable it should skip them and complete",
			cpObjects: func() []client.Object {
				oldKS := awsKeyStatus("arn:aws:kms:us-east-1:123456789012:key/old-key")
				newKS := awsKeyStatus("arn:aws:kms:us-east-1:123456789012:key/test-key-1")
				return []client.Object{
					newHCP(
						withKMSEncryption(),
						withActiveKey(oldKS),
						withTargetKey(newKS),
						withHistory(hyperv1.EncryptionMigrationHistory{
							From:        secretencryption.KeyReferenceFromStatus(oldKS),
							To:          secretencryption.KeyReferenceFromStatus(newKS),
							State:       hyperv1.EncryptionMigrationStateMigrating,
							StartedTime: metav1.Time{Time: fixedTime},
						}),
					),
					convergedKASDeployment(testNamespace),
					encryptionConfigSecret(testNamespace, hyperv1.KMS,
						awsProviderKeyName("arn:aws:kms:us-east-1:123456789012:key/test-key-1"),
						awsProviderKeyName("arn:aws:kms:us-east-1:123456789012:key/old-key")),
				}
			}(),
			migrator: func() *fakeMigrator {
				m := newFakeMigrator()
				fp := secretencryption.FingerprintAWSKMSKey("arn:aws:kms:us-east-1:123456789012:key/test-key-1")
				writeKey := fmt.Sprintf("encryption-key-%s", fp)
				// Complete discoverable resources.
				m.completeMigration(schema.GroupResource{Resource: "secrets"}, writeKey)
				m.completeMigration(schema.GroupResource{Resource: "configmaps"}, writeKey)
				m.completeMigration(schema.GroupResource{Resource: "routes", Group: "route.openshift.io"}, writeKey)
				// Simulate discovery failure for oauth resources (not served or not discoverable).
				m.discoveryErrors[schema.GroupResource{Resource: "oauthaccesstokens", Group: "oauth.openshift.io"}] =
					fmt.Errorf("failed to find version for oauthaccesstokens.oauth.openshift.io, discoveryErr=<nil>")
				m.discoveryErrors[schema.GroupResource{Resource: "oauthauthorizetokens", Group: "oauth.openshift.io"}] =
					fmt.Errorf("failed to find version for oauthauthorizetokens.oauth.openshift.io, discoveryErr=<nil>")
				return m
			},
			validate: func(t *testing.T, g Gomega, cl client.Client, _ *fakeMigrator) {
				hcp := getHCP(context.Background(), g, cl)
				g.Expect(hcp.Status.SecretEncryption.TargetKey.Provider).To(BeEmpty())
				g.Expect(hcp.Status.SecretEncryption.ActiveKey.Provider).To(Equal(hyperv1.SecretEncryptionProviderAWS))
				g.Expect(hcp.Status.SecretEncryption.History[0].State).To(Equal(hyperv1.EncryptionMigrationStateCompleted))

				cond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()

			cpClient := buildCPClient(tt.cpObjects...)
			migrator := tt.migrator()
			r := newReconciler(cpClient, nil, migrator)

			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: testNamespace,
					Name:      testHCPName,
				},
			})

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			g.Expect(result).To(Equal(tt.expectResult))

			if tt.validate != nil {
				tt.validate(t, g, cpClient, migrator)
			}
		})
	}
}

func TestParseGroupResource(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected schema.GroupResource
	}{
		{
			name:     "When parsing a core resource it should return empty group",
			input:    "secrets",
			expected: schema.GroupResource{Group: "", Resource: "secrets"},
		},
		{
			name:     "When parsing a core resource configmaps it should return empty group",
			input:    "configmaps",
			expected: schema.GroupResource{Group: "", Resource: "configmaps"},
		},
		{
			name:     "When parsing a route resource it should split group correctly",
			input:    "routes.route.openshift.io",
			expected: schema.GroupResource{Group: "route.openshift.io", Resource: "routes"},
		},
		{
			name:     "When parsing an oauth resource it should split group correctly",
			input:    "oauthaccesstokens.oauth.openshift.io",
			expected: schema.GroupResource{Group: "oauth.openshift.io", Resource: "oauthaccesstokens"},
		},
		{
			name:     "When parsing oauthauthorizetokens resource it should split group correctly",
			input:    "oauthauthorizetokens.oauth.openshift.io",
			expected: schema.GroupResource{Group: "oauth.openshift.io", Resource: "oauthauthorizetokens"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(parseGroupResource(tt.input)).To(Equal(tt.expected))
		})
	}
}

func TestPrependHistory(t *testing.T) {
	t.Run("When prepending to empty history it should contain only the new entry", func(t *testing.T) {
		g := NewWithT(t)
		entry := hyperv1.EncryptionMigrationHistory{
			State:       hyperv1.EncryptionMigrationStateReadOnlyDeploy,
			StartedTime: metav1.Time{Time: fixedTime},
		}
		result := prependHistory(nil, entry)
		g.Expect(result).To(HaveLen(1))
		g.Expect(result[0].State).To(Equal(hyperv1.EncryptionMigrationStateReadOnlyDeploy))
	})

	t.Run("When prepending to full history it should trim to max entries", func(t *testing.T) {
		g := NewWithT(t)
		existing := make([]hyperv1.EncryptionMigrationHistory, maxHistoryEntries)
		for i := range existing {
			existing[i] = hyperv1.EncryptionMigrationHistory{
				State:       hyperv1.EncryptionMigrationStateCompleted,
				StartedTime: metav1.Time{Time: fixedTime.Add(-time.Duration(i) * time.Hour)},
			}
		}
		entry := hyperv1.EncryptionMigrationHistory{
			State:       hyperv1.EncryptionMigrationStateReadOnlyDeploy,
			StartedTime: metav1.Time{Time: fixedTime},
		}
		result := prependHistory(existing, entry)
		g.Expect(result).To(HaveLen(maxHistoryEntries))
		g.Expect(result[0].State).To(Equal(hyperv1.EncryptionMigrationStateReadOnlyDeploy))
		g.Expect(result[maxHistoryEntries-1].State).To(Equal(hyperv1.EncryptionMigrationStateCompleted))
	})
}

func TestEncryptedResources(t *testing.T) {
	t.Run("When encryption type is AESCBC it should return only secrets", func(t *testing.T) {
		g := NewWithT(t)
		r := &Reconciler{}
		hcp := newHCP(withAESCBCEncryption("key"))
		resources := r.encryptedResources(hcp)
		g.Expect(resources).To(HaveLen(1))
		g.Expect(resources[0]).To(Equal(schema.GroupResource{Resource: "secrets"}))
	})

	t.Run("When encryption type is KMS it should return 5 resources", func(t *testing.T) {
		g := NewWithT(t)
		r := &Reconciler{}
		hcp := newHCP(withKMSEncryption())
		resources := r.encryptedResources(hcp)
		g.Expect(resources).To(HaveLen(5))
		g.Expect(resources).To(ContainElement(schema.GroupResource{Resource: "secrets"}))
		g.Expect(resources).To(ContainElement(schema.GroupResource{Resource: "configmaps"}))
		g.Expect(resources).To(ContainElement(schema.GroupResource{Group: "route.openshift.io", Resource: "routes"}))
		g.Expect(resources).To(ContainElement(schema.GroupResource{Group: "oauth.openshift.io", Resource: "oauthaccesstokens"}))
		g.Expect(resources).To(ContainElement(schema.GroupResource{Group: "oauth.openshift.io", Resource: "oauthauthorizetokens"}))
	})

	t.Run("When encryption type is KMS and OAuth is disabled it should exclude oauth resources", func(t *testing.T) {
		g := NewWithT(t)
		r := &Reconciler{}
		hcp := newHCP(withKMSEncryption(), withExternalOIDC())
		resources := r.encryptedResources(hcp)
		g.Expect(resources).To(HaveLen(3))
		g.Expect(resources).To(ContainElement(schema.GroupResource{Resource: "secrets"}))
		g.Expect(resources).To(ContainElement(schema.GroupResource{Resource: "configmaps"}))
		g.Expect(resources).To(ContainElement(schema.GroupResource{Group: "route.openshift.io", Resource: "routes"}))
		g.Expect(resources).NotTo(ContainElement(schema.GroupResource{Group: "oauth.openshift.io", Resource: "oauthaccesstokens"}))
		g.Expect(resources).NotTo(ContainElement(schema.GroupResource{Group: "oauth.openshift.io", Resource: "oauthauthorizetokens"}))
	})

	t.Run("When encryption is not configured it should return nil", func(t *testing.T) {
		g := NewWithT(t)
		r := &Reconciler{}
		hcp := newHCP()
		resources := r.encryptedResources(hcp)
		g.Expect(resources).To(BeNil())
	})
}
