package auditlogpersistence

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	auditlogpersistencev1alpha1 "github.com/openshift/hypershift/api/auditlogpersistence/v1alpha1"
	hyperapi "github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
)

func TestMutatePod(t *testing.T) {
	tests := []struct {
		name           string
		pod            *corev1.Pod
		config         *auditlogpersistencev1alpha1.AuditLogPersistenceConfig
		existingPVC    *corev1.PersistentVolumeClaim
		expectedError  bool
		validateResult func(*WithT, client.Client, *corev1.Pod)
	}{
		{
			name: "Pod with name creates PVC and replaces emptyDir volume",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123",
					Namespace: "hcp-namespace",
					Labels: map[string]string{
						"app": "kube-apiserver",
					},
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "logs",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Storage: auditlogpersistencev1alpha1.StorageConfig{
						Size:             resource.MustParse("10Gi"),
						StorageClassName: "",
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				// Verify PVC was created
				pvc := &corev1.PersistentVolumeClaim{}
				err := c.Get(context.Background(), types.NamespacedName{
					Name:      "kas-audit-logs-kube-apiserver-abc123",
					Namespace: "hcp-namespace",
				}, pvc)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(pvc.Spec.AccessModes).To(ContainElement(corev1.ReadWriteOnce))
				g.Expect(pvc.Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(resource.MustParse("10Gi")))
				g.Expect(pvc.Spec.StorageClassName).To(BeNil())

				// Verify pod volume was replaced
				var logsVolume *corev1.Volume
				for i := range pod.Spec.Volumes {
					if pod.Spec.Volumes[i].Name == logsVolumeName {
						logsVolume = &pod.Spec.Volumes[i]
						break
					}
				}
				g.Expect(logsVolume).ToNot(BeNil())
				g.Expect(logsVolume.EmptyDir).To(BeNil())
				g.Expect(logsVolume.PersistentVolumeClaim).ToNot(BeNil())
				g.Expect(logsVolume.PersistentVolumeClaim.ClaimName).To(Equal("kas-audit-logs-kube-apiserver-abc123"))
			},
		},
		{
			name: "Pod with name and GenerateName uses Name for PVC (already generated)",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:         "kube-apiserver-7f6b67c88-abc12",
					GenerateName: "",
					Namespace:    "hcp-namespace",
					Labels: map[string]string{
						"app": "kube-apiserver",
					},
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "logs",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Storage: auditlogpersistencev1alpha1.StorageConfig{
						Size: resource.MustParse("5Gi"),
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				// Verify PVC was created with the full pod name
				pvc := &corev1.PersistentVolumeClaim{}
				err := c.Get(context.Background(), types.NamespacedName{
					Name:      "kas-audit-logs-kube-apiserver-7f6b67c88-abc12",
					Namespace: "hcp-namespace",
				}, pvc)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(pvc.Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(resource.MustParse("5Gi")))
			},
		},
		{
			name: "Pod with only GenerateName generates name and creates PVC",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:         "",
					GenerateName: "kube-apiserver-7f6b67c88-",
					Namespace:    "hcp-namespace",
					Labels: map[string]string{
						"app": "kube-apiserver",
					},
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "logs",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Storage: auditlogpersistencev1alpha1.StorageConfig{
						Size: resource.MustParse("5Gi"),
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				// Verify pod name was generated and GenerateName was cleared
				g.Expect(pod.Name).ToNot(BeEmpty())
				g.Expect(pod.Name).To(HavePrefix("kube-apiserver-7f6b67c88-"))
				g.Expect(len(pod.Name)).To(Equal(len("kube-apiserver-7f6b67c88-") + 5)) // generateName + 5 random chars
				g.Expect(pod.GenerateName).To(BeEmpty())

				// Verify PVC was created with the generated pod name
				pvc := &corev1.PersistentVolumeClaim{}
				err := c.Get(context.Background(), types.NamespacedName{
					Name:      "kas-audit-logs-" + pod.Name,
					Namespace: "hcp-namespace",
				}, pvc)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(pvc.Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(resource.MustParse("5Gi")))

				// Verify pod volume was replaced with PVC
				var logsVolume *corev1.Volume
				for i := range pod.Spec.Volumes {
					if pod.Spec.Volumes[i].Name == logsVolumeName {
						logsVolume = &pod.Spec.Volumes[i]
						break
					}
				}
				g.Expect(logsVolume).ToNot(BeNil())
				g.Expect(logsVolume.EmptyDir).To(BeNil())
				g.Expect(logsVolume.PersistentVolumeClaim).ToNot(BeNil())
				g.Expect(logsVolume.PersistentVolumeClaim.ClaimName).To(Equal("kas-audit-logs-" + pod.Name))
			},
		},
		{
			name: "Pod without name or GenerateName returns error",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "",
					Namespace: "hcp-namespace",
				},
				Spec: corev1.PodSpec{},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Storage: auditlogpersistencev1alpha1.StorageConfig{
						Size: resource.MustParse("5Gi"),
					},
				},
			},
			expectedError: true,
		},
		{
			name: "Pod with ReplicaSet owner sets PVC owner reference",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-xyz789",
					Namespace: "hcp-namespace",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "apps/v1",
							Kind:       "ReplicaSet",
							Name:       "kube-apiserver-abc123",
							UID:        "replicaset-uid",
							Controller: func() *bool { b := true; return &b }(),
						},
					},
					Labels: map[string]string{
						"app": "kube-apiserver",
					},
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "logs",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Storage: auditlogpersistencev1alpha1.StorageConfig{
						Size: resource.MustParse("10Gi"),
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				pvc := &corev1.PersistentVolumeClaim{}
				err := c.Get(context.Background(), types.NamespacedName{
					Name:      "kas-audit-logs-kube-apiserver-xyz789",
					Namespace: "hcp-namespace",
				}, pvc)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(pvc.OwnerReferences).To(HaveLen(1))
				g.Expect(pvc.OwnerReferences[0].Kind).To(Equal("ReplicaSet"))
				g.Expect(pvc.OwnerReferences[0].Name).To(Equal("kube-apiserver-abc123"))
				g.Expect(pvc.OwnerReferences[0].UID).To(Equal(types.UID("replicaset-uid")))
			},
		},
		{
			name: "PVC with StorageClassName sets StorageClassName on PVC",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-def456",
					Namespace: "hcp-namespace",
					Labels: map[string]string{
						"app": "kube-apiserver",
					},
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "logs",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Storage: auditlogpersistencev1alpha1.StorageConfig{
						Size:             resource.MustParse("20Gi"),
						StorageClassName: "fast-ssd",
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				pvc := &corev1.PersistentVolumeClaim{}
				err := c.Get(context.Background(), types.NamespacedName{
					Name:      "kas-audit-logs-kube-apiserver-def456",
					Namespace: "hcp-namespace",
				}, pvc)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(pvc.Spec.StorageClassName).ToNot(BeNil())
				g.Expect(*pvc.Spec.StorageClassName).To(Equal("fast-ssd"))
			},
		},
		{
			name: "Existing PVC updates owner reference if missing",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-ghi789",
					Namespace: "hcp-namespace",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "apps/v1",
							Kind:       "ReplicaSet",
							Name:       "kube-apiserver-rs",
							UID:        "replicaset-uid-2",
							Controller: func() *bool { b := true; return &b }(),
						},
					},
					Labels: map[string]string{
						"app": "kube-apiserver",
					},
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "logs",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Storage: auditlogpersistencev1alpha1.StorageConfig{
						Size: resource.MustParse("10Gi"),
					},
				},
			},
			existingPVC: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kas-audit-logs-kube-apiserver-ghi789",
					Namespace: "hcp-namespace",
					// No owner references
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("10Gi"),
						},
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				pvc := &corev1.PersistentVolumeClaim{}
				err := c.Get(context.Background(), types.NamespacedName{
					Name:      "kas-audit-logs-kube-apiserver-ghi789",
					Namespace: "hcp-namespace",
				}, pvc)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(pvc.OwnerReferences).To(HaveLen(1))
				g.Expect(pvc.OwnerReferences[0].Name).To(Equal("kube-apiserver-rs"))
			},
		},
		{
			name: "Pod without logs volume adds PVC volume",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-jkl012",
					Namespace: "hcp-namespace",
					Labels: map[string]string{
						"app": "kube-apiserver",
					},
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "other-volume",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "some-config",
									},
								},
							},
						},
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Storage: auditlogpersistencev1alpha1.StorageConfig{
						Size: resource.MustParse("5Gi"),
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				// Verify PVC was created
				pvc := &corev1.PersistentVolumeClaim{}
				err := c.Get(context.Background(), types.NamespacedName{
					Name:      "kas-audit-logs-kube-apiserver-jkl012",
					Namespace: "hcp-namespace",
				}, pvc)
				g.Expect(err).ToNot(HaveOccurred())

				// Verify logs volume was added
				var logsVolume *corev1.Volume
				for i := range pod.Spec.Volumes {
					if pod.Spec.Volumes[i].Name == logsVolumeName {
						logsVolume = &pod.Spec.Volumes[i]
						break
					}
				}
				g.Expect(logsVolume).ToNot(BeNil())
				g.Expect(logsVolume.PersistentVolumeClaim).ToNot(BeNil())
				g.Expect(logsVolume.PersistentVolumeClaim.ClaimName).To(Equal("kas-audit-logs-kube-apiserver-jkl012"))

				// Verify other volume is preserved
				var otherVolume *corev1.Volume
				for i := range pod.Spec.Volumes {
					if pod.Spec.Volumes[i].Name == "other-volume" {
						otherVolume = &pod.Spec.Volumes[i]
						break
					}
				}
				g.Expect(otherVolume).ToNot(BeNil())
			},
		},
		{
			name: "Realistic kube-apiserver pod from fixture",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-abc123def456",
					Namespace: "hcp-namespace",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "apps/v1",
							Kind:       "ReplicaSet",
							Name:       "kube-apiserver-abc123",
							UID:        "replicaset-uid-fixture",
							Controller: func() *bool { b := true; return &b }(),
						},
					},
					Labels: map[string]string{
						"app": "kube-apiserver",
						"hypershift.openshift.io/control-plane-component": "kube-apiserver",
						"hypershift.openshift.io/hosted-control-plane":    "hcp-namespace",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "kube-apiserver",
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "logs",
									MountPath: "/var/log/kube-apiserver",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "logs",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "kas-config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "kas-config",
									},
								},
							},
						},
					},
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Storage: auditlogpersistencev1alpha1.StorageConfig{
						Size:             resource.MustParse("5Gi"),
						StorageClassName: "gp3-csi",
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, c client.Client, pod *corev1.Pod) {
				// Verify PVC was created with correct settings
				pvc := &corev1.PersistentVolumeClaim{}
				err := c.Get(context.Background(), types.NamespacedName{
					Name:      "kas-audit-logs-kube-apiserver-abc123def456",
					Namespace: "hcp-namespace",
				}, pvc)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(pvc.Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(resource.MustParse("5Gi")))
				g.Expect(*pvc.Spec.StorageClassName).To(Equal("gp3-csi"))
				g.Expect(pvc.OwnerReferences).To(HaveLen(1))
				g.Expect(pvc.OwnerReferences[0].Name).To(Equal("kube-apiserver-abc123"))

				// Verify logs volume was replaced
				var logsVolume *corev1.Volume
				for i := range pod.Spec.Volumes {
					if pod.Spec.Volumes[i].Name == logsVolumeName {
						logsVolume = &pod.Spec.Volumes[i]
						break
					}
				}
				g.Expect(logsVolume).ToNot(BeNil())
				g.Expect(logsVolume.EmptyDir).To(BeNil())
				g.Expect(logsVolume.PersistentVolumeClaim).ToNot(BeNil())
				g.Expect(logsVolume.PersistentVolumeClaim.ClaimName).To(Equal("kas-audit-logs-kube-apiserver-abc123def456"))

				// Verify other volumes are preserved
				var kasConfigVolume *corev1.Volume
				for i := range pod.Spec.Volumes {
					if pod.Spec.Volumes[i].Name == "kas-config" {
						kasConfigVolume = &pod.Spec.Volumes[i]
						break
					}
				}
				g.Expect(kasConfigVolume).ToNot(BeNil())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Setup fake client
			var objects []client.Object
			if tt.existingPVC != nil {
				objects = append(objects, tt.existingPVC)
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(hyperapi.Scheme).
				WithObjects(objects...).
				Build()

			handler := &PodWebhookHandler{
				log:    logr.Discard(),
				client: fakeClient,
			}

			// Create a copy of the pod for mutation
			podCopy := tt.pod.DeepCopy()

			err := handler.mutatePod(context.Background(), podCopy, &tt.config.Spec)

			if tt.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				if tt.validateResult != nil {
					tt.validateResult(g, fakeClient, podCopy)
				}
			}
		})
	}
}
