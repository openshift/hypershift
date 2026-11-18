package main

import (
	"bytes"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/api"

	configapi "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
)

func TestGetImageRegistryCABundle(t *testing.T) {
	// manyRegistryCAs has enough entries that Go's randomized map iteration practically never yields sorted key order,
	// so concatenating in map order fails reliably. Keys are zero-padded so that generation order is sorted key order,
	// and each host also has a "host..port" entry, which sorts directly after the host.
	manyRegistryCAs := map[string]string{}
	var manyRegistryCAsSorted string
	for i := range 20 {
		host := fmt.Sprintf("mirror-%02d.registry.example.com", i)
		manyRegistryCAs[host] = "cert-" + host
		manyRegistryCAs[host+"..5000"] = "cert-" + host + "-5000"
		manyRegistryCAsSorted += "cert-" + host + "\ncert-" + host + "-5000\n"
	}

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
		{
			name: "When the trusted CA configmap has multiple entries, it should concatenate them in sorted key order",
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
					Data: manyRegistryCAs,
				},
			},
			expectedCert:  bytes.NewBufferString(manyRegistryCAsSorted),
			expectedError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.objects...).Build()
			cert, err := getImageRegistryCABundle(t.Context(), client)
			if tc.expectedError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(cert).To(BeNil())
				return
			}
			g.Expect(err).NotTo(HaveOccurred())

			if tc.expectedCert == nil {
				g.Expect(cert).To(BeNil())
				return
			}
			g.Expect(cert).NotTo(BeNil())
			g.Expect(cert.String()).To(Equal(tc.expectedCert.String()))
		})
	}
}

func TestRunInit(t *testing.T) {
	t.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")

	g := NewWithT(t)
	err := runInit(t.Context(), logr.Discard())
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("unable to get kubernetes config"))
}
