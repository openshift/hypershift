package main

import (
	"bytes"
	"context"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/api"

	configapi "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetImageRegistryCABundle(t *testing.T) {
	testCases := []struct {
		name               string
		objects            []crclient.Object
		clusterImageConfig *configapi.Image
		configmap          *corev1.ConfigMap
		expectedCert       *bytes.Buffer
		expectedError      bool
	}{
		{
			name:               "The image.config.openshift.io object doesn't exist",
			objects:            []crclient.Object{},
			clusterImageConfig: nil,
			configmap:          nil,
			expectedCert:       nil,
			expectedError:      true,
		},
		{
			name: "The image.config.openshift.io object doesn't specify a trusted CA",
			objects: []crclient.Object{
				&configapi.Image{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster",
					},
					Spec: configapi.ImageSpec{},
				},
			},
			clusterImageConfig: nil,
			configmap:          nil,
			expectedCert:       nil,
			expectedError:      false,
		},
		{
			name: "The trusted CA configmap doesn't exist",
			objects: []crclient.Object{
				&configapi.Image{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster",
					},
					Spec: configapi.ImageSpec{
						AdditionalTrustedCA: configapi.ConfigMapNameReference{
							Name: "registry-config",
						},
					},
				},
			},
			clusterImageConfig: nil,
			configmap:          nil,
			expectedCert:       nil,
			expectedError:      true,
		},
		{
			name: "The trusted CA configmap has no data",
			objects: []crclient.Object{
				&configapi.Image{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster",
					},
					Spec: configapi.ImageSpec{
						AdditionalTrustedCA: configapi.ConfigMapNameReference{
							Name: "registry-config",
						},
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "registry-config",
						Namespace: "openshift-config",
					},
				},
			},
			clusterImageConfig: nil,
			expectedCert:       nil,
			expectedError:      false,
		},
		{
			name: "The trusted CA configmap has data",
			objects: []crclient.Object{
				&configapi.Image{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster",
					},
					Spec: configapi.ImageSpec{
						AdditionalTrustedCA: configapi.ConfigMapNameReference{
							Name: "registry-config",
						},
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "registry-config",
						Namespace: "openshift-config",
					},
					Data: map[string]string{
						"mirror.registry.com": "test",
					},
				},
			},
			clusterImageConfig: nil,
			expectedCert:       bytes.NewBufferString("test\n"),
			expectedError:      false,
		},
		{
			name: "The trusted CA configmap has more than one certificate entry",
			objects: []crclient.Object{
				&configapi.Image{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster",
					},
					Spec: configapi.ImageSpec{
						AdditionalTrustedCA: configapi.ConfigMapNameReference{
							Name: "registry-config",
						},
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "registry-config",
						Namespace: "openshift-config",
					},
					Data: map[string]string{
						"mirror1.registry.com": "test1",
						"mirror2.registry.com": "test2",
						"mirror3.registry.com": "test3",
						"mirror4.registry.com": "test4",
					},
				},
			},
			clusterImageConfig: nil,
			expectedCert:       bytes.NewBufferString("test1\ntest2\ntest3\ntest4\n"),
			expectedError:      false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.objects...).Build()
			ctx := context.Background()
			cert, err := getImageRegistryCABundle(ctx, client)
			if tc.expectedError {
				g.Expect(err).NotTo(BeNil())
			}

			if tc.expectedCert != nil {
				for _, value := range bytes.Split(cert.Bytes(), []byte("\n")) {
					g.Expect(value).Should(BeElementOf(bytes.Split(tc.expectedCert.Bytes(), []byte("\n"))))
				}
			}
		})
	}
}
