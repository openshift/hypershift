package integration

import (
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/internal/testtrace"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	hccomanifests "github.com/openshift/hypershift/pkg/manifests/hcco"
	pkgoauth "github.com/openshift/hypershift/pkg/oauth"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"golang.org/x/crypto/bcrypt"
)

func TestReconcileKubeadminPasswordHashSecret(t *testing.T) {
	t.Parallel()
	passwordSecret := func(password string) *corev1.Secret {
		secret := hccomanifests.KubeadminPasswordSecret(testNamespace)
		secret.Data = map[string][]byte{"password": []byte(password)}
		return secret
	}
	hashSecret := func(hash []byte) *corev1.Secret {
		secret := manifests.KubeadminPasswordHashSecret()
		secret.Data = map[string][]byte{"kubeadmin": hash}
		return secret
	}
	oauthDeployment := func() *appsv1.Deployment {
		return &appsv1.Deployment{ObjectMeta: hccomanifests.OAuthDeployment(testNamespace).ObjectMeta}
	}
	matchingHash, err := bcrypt.GenerateFromPassword([]byte("adminpass"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]struct {
		controlPlane       []client.Object
		existingHashSecret *corev1.Secret
		cpGetFailures      map[string]error
		expectedErr        string
		expectHashSecret   bool
		expectHashKept     bool
	}{
		"When kubeadminPasswordSecret exists, it should create the hash secret and annotate the control-plane secret": {
			controlPlane:     []client.Object{passwordSecret("adminpass"), oauthDeployment()},
			expectHashSecret: true,
		},
		"When existing hash does not match the password it should regenerate the hash": {
			controlPlane:       []client.Object{passwordSecret("adminpass"), oauthDeployment()},
			existingHashSecret: hashSecret([]byte("stale-non-matching-hash")),
			expectHashSecret:   true,
		},
		"When hash already matches password it should not regenerate": {
			controlPlane:       []client.Object{passwordSecret("adminpass"), oauthDeployment()},
			existingHashSecret: hashSecret(matchingHash),
			expectHashSecret:   true,
			expectHashKept:     true,
		},
		"When kubeadminPasswordSecret does not exist, it should not create the hash secret": {
			controlPlane: []client.Object{oauthDeployment()},
		},
		"When kubeadminPasswordSecret does not exist and a hash secret exists, it should delete the hash secret": {
			controlPlane:       []client.Object{oauthDeployment()},
			existingHashSecret: hashSecret([]byte("stale-hash")),
		},
		"When reading kubeadminPasswordSecret fails, it should return the wrapped error and keep the hash secret": {
			controlPlane:       []client.Object{passwordSecret("adminpass"), oauthDeployment()},
			existingHashSecret: hashSecret([]byte("existing-hash")),
			cpGetFailures:      map[string]error{testtrace.ObjectKey(passwordSecret("")): errors.New("cp unavailable")},
			expectedErr:        "failed to get kubeadmin password secret: cp unavailable",
			expectHashSecret:   true,
			expectHashKept:     true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			var hosted []client.Object
			if test.existingHashSecret != nil {
				hosted = append(hosted, test.existingHashSecret)
			}
			run := newProjectionRun(projectionFixture{controlPlane: test.controlPlane, hosted: hosted, cpGetFailures: test.cpGetFailures})

			err := reconcileKubeadminPasswordHashSecret(t.Context(), run.hosted, run.controlPlane, testNamespace)
			controlPlaneOperations := run.trace.Cluster("control-plane")

			if test.expectedErr != "" {
				g.Expect(err).To(MatchError(test.expectedErr))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
			actual := manifests.KubeadminPasswordHashSecret()
			getErr := run.hostedClient.Get(t.Context(), client.ObjectKeyFromObject(actual), actual)
			if !test.expectHashSecret {
				g.Expect(apierrors.IsNotFound(getErr)).To(BeTrue())
				g.Expect(controlPlaneOperations).NotTo(ContainElement(HavePrefix("update ")))
				return
			}
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(actual.Data["kubeadmin"]).NotTo(BeEmpty())
			if test.expectHashKept {
				g.Expect(actual.Data["kubeadmin"]).To(Equal(test.existingHashSecret.Data["kubeadmin"]))
			}
			if test.expectedErr != "" {
				g.Expect(controlPlaneOperations).NotTo(ContainElement(HavePrefix("update ")))
				return
			}
			password := hccomanifests.KubeadminPasswordSecret(testNamespace)
			g.Expect(run.cpClient.Get(t.Context(), client.ObjectKeyFromObject(password), password)).To(Succeed())
			g.Expect(bcrypt.CompareHashAndPassword(actual.Data["kubeadmin"], password.Data["password"])).To(Succeed())
			g.Expect(password.Annotations).To(HaveKeyWithValue(pkgoauth.KubeadminSecretHashAnnotation, string(actual.Data["kubeadmin"])))
			deployment := &appsv1.Deployment{}
			g.Expect(run.cpClient.Get(t.Context(), client.ObjectKeyFromObject(oauthDeployment()), deployment)).To(Succeed())
		})
	}
}

func TestDeleteKubeadminPasswordHashSecret(t *testing.T) {
	t.Run("When the hash secret exists, it should delete it", func(t *testing.T) {
		g := NewWithT(t)
		run := newProjectionRun(projectionFixture{hosted: []client.Object{manifests.KubeadminPasswordHashSecret()}})

		g.Expect(deleteKubeadminPasswordHashSecret(t.Context(), run.hosted)).To(Succeed())

		g.Expect(apierrors.IsNotFound(run.hostedClient.Get(t.Context(), client.ObjectKeyFromObject(manifests.KubeadminPasswordHashSecret()), manifests.KubeadminPasswordHashSecret()))).To(BeTrue())
	})

	t.Run("When the hash secret does not exist, it should succeed without deleting", func(t *testing.T) {
		g := NewWithT(t)
		run := newProjectionRun(projectionFixture{})

		g.Expect(deleteKubeadminPasswordHashSecret(t.Context(), run.hosted)).To(Succeed())

		g.Expect(run.trace.Operations()).NotTo(ContainElement(HavePrefix("delete ")))
	})
}
