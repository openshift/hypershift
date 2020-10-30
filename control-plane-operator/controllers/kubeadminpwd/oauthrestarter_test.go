package kubeadminpwd

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/stretchr/testify/assert"
)

const (
	hostedNamespace = "hosted-namespace"
)

func TestReconcile(t *testing.T) {

	tests := []struct {
		name               string
		secret             string
		existingAnnotation string
		validate           func(*testing.T, map[string]string)
	}{
		{
			name:   "add annotation",
			secret: "12345",
			validate: func(t *testing.T, annotations map[string]string) {
				assert.Contains(t, annotations, SecretHashAnnotation, "annotations should include a secret hash annotation")
				assert.Equal(t, hashFor(t, "12345"), annotations[SecretHashAnnotation])
			},
		},
		{
			name:               "remove annotation",
			existingAnnotation: hashFor(t, "12345"),
			validate: func(t *testing.T, annotations map[string]string) {
				assert.NotContains(t, annotations, SecretHashAnnotation, "annotation should have been removed")
			},
		},
		{
			name:               "update annotation",
			secret:             "67890",
			existingAnnotation: hashFor(t, "12345"),
			validate: func(t *testing.T, annotations map[string]string) {
				assert.Contains(t, annotations, SecretHashAnnotation, "annotations should include a secret hash annotation")
				assert.Equal(t, hashFor(t, "67890"), annotations[SecretHashAnnotation])
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oauthDeployment := fakeOAuthDeployment(test.existingAnnotation)
			secretIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			targetSecretLister := corelisters.NewSecretLister(secretIndexer)
			if len(test.secret) > 0 {
				secretIndexer.Add(fakeSecret(test.secret))
			}
			hostClient := fake.NewSimpleClientset(oauthDeployment)
			oauthRestarter := &OAuthRestarter{
				Client:       hostClient,
				Log:          ctrl.Log.WithName("reconcile-test"),
				Namespace:    hostedNamespace,
				SecretLister: targetSecretLister,
			}
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: metav1.NamespaceSystem,
					Name:      KubeAdminSecret,
				},
			}
			_, err := oauthRestarter.Reconcile(context.TODO(), req)
			assert.NoError(t, err, "Unexpected error")
			if test.validate != nil {
				var d, err = hostClient.AppsV1().Deployments(hostedNamespace).Get(context.TODO(), OAuthDeploymentName, metav1.GetOptions{})
				assert.NoError(t, err)
				test.validate(t, d.Spec.Template.ObjectMeta.Annotations)
			}
		})
	}
}

func fakeOAuthDeployment(annotationValue string) *appsv1.Deployment {
	annotations := map[string]string{}
	if len(annotationValue) > 0 {
		annotations[SecretHashAnnotation] = annotationValue
	}
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedNamespace,
			Name:      OAuthDeploymentName,
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: annotations,
				},
			},
		},
	}
	return d
}

func fakeSecret(value string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KubeAdminSecret,
			Namespace: metav1.NamespaceSystem,
		},
		Data: map[string][]byte{
			"password": []byte(value),
		},
	}
}

func hashFor(t *testing.T, value string) string {
	s := fakeSecret(value)
	hash, err := calculateHash(s.Data)
	assert.NoError(t, err)
	return hash
}
