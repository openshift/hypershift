package karpenterignition

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestNewNodeClass(t *testing.T) {
	testCases := map[string]struct {
		gvk schema.GroupVersionKind
	}{
		"When the AWS GVK is configured, it should construct an AWS NodeClass": {
			gvk: schema.GroupVersionKind{
				Group:   "karpenter.hypershift.openshift.io",
				Version: "v1",
				Kind:    "OpenshiftEC2NodeClass",
			},
		},
		"When another GVK is configured, it should preserve its group version and kind": {
			gvk: schema.GroupVersionKind{
				Group:   "karpenter.k8s.aws",
				Version: "v1beta1",
				Kind:    "EC2NodeClass",
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)

			nodeClass := newNodeClass(tc.gvk)

			g.Expect(nodeClass.object.GroupVersionKind()).To(Equal(tc.gvk))
			g.Expect(nodeClass.object.GetName()).To(BeEmpty())
			g.Expect(nodeClass.object.GetNamespace()).To(BeEmpty())
		})
	}
}

func TestNewNodeClassList(t *testing.T) {
	testCases := map[string]struct {
		gvk         schema.GroupVersionKind
		expectedGVK schema.GroupVersionKind
	}{
		"When the AWS GVK is configured, it should construct the corresponding NodeClass list": {
			gvk: schema.GroupVersionKind{
				Group:   "karpenter.hypershift.openshift.io",
				Version: "v1",
				Kind:    "OpenshiftEC2NodeClass",
			},
			expectedGVK: schema.GroupVersionKind{
				Group:   "karpenter.hypershift.openshift.io",
				Version: "v1",
				Kind:    "OpenshiftEC2NodeClassList",
			},
		},
		"When another GVK is configured, it should construct a list of that resource": {
			gvk: schema.GroupVersionKind{
				Group:   "karpenter.k8s.aws",
				Version: "v1beta1",
				Kind:    "EC2NodeClass",
			},
			expectedGVK: schema.GroupVersionKind{
				Group:   "karpenter.k8s.aws",
				Version: "v1beta1",
				Kind:    "EC2NodeClassList",
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)

			nodeClassList := newNodeClassList(tc.gvk)

			g.Expect(nodeClassList.GroupVersionKind()).To(Equal(tc.expectedGVK))
			g.Expect(nodeClassList.Items).To(BeEmpty())
		})
	}
}

func TestSpecVersion(t *testing.T) {
	testCases := map[string]struct {
		object          map[string]interface{}
		expectedVersion string
		expectedError   string
	}{
		"When spec.version is set, it should return the requested version": {
			object:          map[string]interface{}{"spec": map[string]interface{}{"version": "4.21.10"}},
			expectedVersion: "4.21.10",
		},
		"When spec.version is empty, it should return an empty version": {
			object:          map[string]interface{}{"spec": map[string]interface{}{"version": ""}},
			expectedVersion: "",
		},
		"When spec.version is absent, it should return an empty version": {
			object:          map[string]interface{}{"spec": map[string]interface{}{}},
			expectedVersion: "",
		},
		"When spec is absent, it should return an empty version": {
			object:          map[string]interface{}{},
			expectedVersion: "",
		},
		"When spec.version is not a string, it should return a type error": {
			object:        map[string]interface{}{"spec": map[string]interface{}{"version": int64(421)}},
			expectedError: "expected string",
		},
		"When spec is not an object, it should return a type error": {
			object:        map[string]interface{}{"spec": "invalid"},
			expectedError: "expected map[string]interface{}",
		},
		"When spec.version is null, it should return a type error": {
			object:        map[string]interface{}{"spec": map[string]interface{}{"version": nil}},
			expectedError: "expected string",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			nc := &nodeClass{object: &unstructured.Unstructured{Object: tc.object}}
			original := nc.object.DeepCopy()

			version, err := nc.specVersion()

			g.Expect(nc.object).To(Equal(original))
			g.Expect(version).To(Equal(tc.expectedVersion))
			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedError)))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestStatusVersion(t *testing.T) {
	testCases := map[string]struct {
		object          map[string]interface{}
		expectedVersion string
		expectedError   string
	}{
		"When status.version is set, it should return the previously resolved version": {
			object:          map[string]interface{}{"status": map[string]interface{}{"version": "4.21.10"}},
			expectedVersion: "4.21.10",
		},
		"When status.version is empty, it should return an empty version": {
			object:          map[string]interface{}{"status": map[string]interface{}{"version": ""}},
			expectedVersion: "",
		},
		"When status.version is absent, it should return an empty version": {
			object:          map[string]interface{}{"status": map[string]interface{}{}},
			expectedVersion: "",
		},
		"When status is absent, it should return an empty version": {
			object:          map[string]interface{}{},
			expectedVersion: "",
		},
		"When status.version is not a string, it should return a type error": {
			object:        map[string]interface{}{"status": map[string]interface{}{"version": true}},
			expectedError: "expected string",
		},
		"When status is not an object, it should return a type error": {
			object:        map[string]interface{}{"status": "invalid"},
			expectedError: "expected map[string]interface{}",
		},
		"When status.version is null, it should return a type error": {
			object:        map[string]interface{}{"status": map[string]interface{}{"version": nil}},
			expectedError: "expected string",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			nc := &nodeClass{object: &unstructured.Unstructured{Object: tc.object}}
			original := nc.object.DeepCopy()

			version, err := nc.statusVersion()

			g.Expect(nc.object).To(Equal(original))
			g.Expect(version).To(Equal(tc.expectedVersion))
			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedError)))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestKubeletConfig(t *testing.T) {
	g := NewWithT(t)
	kubeletConfig := hyperkarpenterv1.KubeletConfiguration{
		MaxPods:        500,
		SystemReserved: map[string]string{"cpu": "200m", "memory": "512Mi"},
		Overflow: runtime.RawExtension{
			Raw: []byte(`{"allowedUnsafeSysctls":["net.ipv4.tcp_syncookies"],"containerLogMaxSize":"50Mi","serializeImagePulls":false}`),
		},
	}
	kubeletObject, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&kubeletConfig)
	g.Expect(err).NotTo(HaveOccurred())

	testCases := map[string]struct {
		object         map[string]interface{}
		expectedConfig hyperkarpenterv1.KubeletConfiguration
		expectedError  string
	}{
		"When kubelet configuration contains typed and additional settings, it should decode both": {
			object:         map[string]interface{}{"spec": map[string]interface{}{"kubelet": kubeletObject}},
			expectedConfig: kubeletConfig,
		},
		"When spec.kubelet is empty, it should return no custom configuration": {
			object: map[string]interface{}{"spec": map[string]interface{}{"kubelet": map[string]interface{}{}}},
		},
		"When spec.kubelet is absent, it should return no custom configuration": {
			object: map[string]interface{}{"spec": map[string]interface{}{}},
		},
		"When spec is absent, it should return no custom configuration": {
			object: map[string]interface{}{},
		},
		"When spec.kubelet is not an object, it should return a type error": {
			object:        map[string]interface{}{"spec": map[string]interface{}{"kubelet": "invalid"}},
			expectedError: "failed to read NodeClass spec.kubelet",
		},
		"When spec is not an object, it should return a type error": {
			object:        map[string]interface{}{"spec": true},
			expectedError: "failed to read NodeClass spec.kubelet",
		},
		"When spec.kubelet is null, it should return a type error": {
			object:        map[string]interface{}{"spec": map[string]interface{}{"kubelet": nil}},
			expectedError: "failed to read NodeClass spec.kubelet",
		},
		"When a typed kubelet field has the wrong type, it should return a decoding error": {
			object:        map[string]interface{}{"spec": map[string]interface{}{"kubelet": map[string]interface{}{"maxPods": "invalid"}}},
			expectedError: "failed to decode NodeClass spec.kubelet",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			nc := &nodeClass{object: &unstructured.Unstructured{Object: tc.object}}
			original := nc.object.DeepCopy()

			config, err := nc.kubeletConfig()

			g.Expect(nc.object).To(Equal(original))
			g.Expect(config).To(Equal(tc.expectedConfig))
			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedError)))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())

			if config.SystemReserved != nil {
				config.SystemReserved["cpu"] = "500m"
				g.Expect(nc.object).To(Equal(original))
			}
		})
	}
}

func TestStatus(t *testing.T) {
	transitionTime := metav1.NewTime(time.Date(2026, time.October, 15, 10, 0, 0, 0, time.UTC))
	readyCondition := map[string]interface{}{
		"type":               "Ready",
		"status":             "False",
		"observedGeneration": int64(7),
		"lastTransitionTime": "2026-10-15T10:00:00Z",
		"reason":             "AwaitingReconciliation",
		"message":            "Waiting for version resolution",
		"providerDetail":     "reconciliation pending",
	}
	versionCondition := map[string]interface{}{
		"type":               "VersionResolved",
		"status":             "True",
		"observedGeneration": int64(7),
		"lastTransitionTime": "2026-10-15T10:00:00Z",
		"reason":             "VersionResolved",
		"message":            "Version 4.21.10 resolved to quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64",
	}
	testCases := map[string]struct {
		object             map[string]interface{}
		expectedConditions []metav1.Condition
		expectedImage      string
		expectedVersion    string
		expectedError      string
	}{
		"When version fields and provider conditions exist, it should decode the common status and retain condition order": {
			object: map[string]interface{}{
				"status": map[string]interface{}{
					"instanceProfile": "hypershift-worker-profile",
					"conditions":      []interface{}{readyCondition, versionCondition},
					"releaseImage":    "quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64",
					"version":         "4.21.10",
				},
			},
			expectedImage:   "quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64",
			expectedVersion: "4.21.10",
			expectedConditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionFalse,
					ObservedGeneration: 7,
					LastTransitionTime: transitionTime,
					Reason:             "AwaitingReconciliation",
					Message:            "Waiting for version resolution",
				},
				{
					Type:               "VersionResolved",
					Status:             metav1.ConditionTrue,
					ObservedGeneration: 7,
					LastTransitionTime: transitionTime,
					Reason:             "VersionResolved",
					Message:            "Version 4.21.10 resolved to quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64",
				},
			},
		},
		"When status is absent, it should return empty status": {
			object: map[string]interface{}{},
		},
		"When common status fields are absent, it should return empty status": {
			object: map[string]interface{}{"status": map[string]interface{}{}},
		},
		"When only releaseImage is present, it should return it with other fields empty": {
			object: map[string]interface{}{"status": map[string]interface{}{
				"releaseImage": "quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64",
			}},
			expectedImage: "quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64",
		},
		"When only version is present, it should return it with other fields empty": {
			object:          map[string]interface{}{"status": map[string]interface{}{"version": "4.21.10"}},
			expectedVersion: "4.21.10",
		},
		"When version fields are empty, it should return empty strings": {
			object: map[string]interface{}{"status": map[string]interface{}{"version": "", "releaseImage": ""}},
		},
		"When status.version is not a string, it should return a type error": {
			object:        map[string]interface{}{"status": map[string]interface{}{"version": true}},
			expectedError: "failed to read NodeClass status.version",
		},
		"When status.version is null, it should return a type error": {
			object:        map[string]interface{}{"status": map[string]interface{}{"version": nil}},
			expectedError: "failed to read NodeClass status.version",
		},
		"When status.releaseImage is not a string, it should return a type error": {
			object:        map[string]interface{}{"status": map[string]interface{}{"releaseImage": int64(421)}},
			expectedError: "failed to read NodeClass status.releaseImage",
		},
		"When status.releaseImage is null, it should return a type error": {
			object:        map[string]interface{}{"status": map[string]interface{}{"releaseImage": nil}},
			expectedError: "failed to read NodeClass status.releaseImage",
		},
		"When status.conditions is empty, it should return no conditions": {
			object: map[string]interface{}{"status": map[string]interface{}{"conditions": []interface{}{}}},
		},
		"When status is not an object, it should return a type error": {
			object:        map[string]interface{}{"status": "invalid"},
			expectedError: "failed to read NodeClass status.version",
		},
		"When status.conditions is not a list, it should return a type error": {
			object:        map[string]interface{}{"status": map[string]interface{}{"conditions": map[string]interface{}{}}},
			expectedError: "failed to read NodeClass status.conditions",
		},
		"When a condition is not an object, it should return its index in a type error": {
			object:        map[string]interface{}{"status": map[string]interface{}{"conditions": []interface{}{"Ready"}}},
			expectedError: "NodeClass status.conditions[0] must be an object",
		},
		"When a condition field has the wrong type, it should return a decoding error": {
			object: map[string]interface{}{
				"status": map[string]interface{}{
					"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": true}},
				},
			},
			expectedError: "failed to decode NodeClass status.conditions[0]",
		},
		"When a condition timestamp is invalid, it should return a decoding error": {
			object: map[string]interface{}{
				"status": map[string]interface{}{
					"conditions": []interface{}{map[string]interface{}{"type": "Ready", "lastTransitionTime": "invalid"}},
				},
			},
			expectedError: "failed to decode NodeClass status.conditions[0]",
		},
		"When a later condition is malformed, it should return its index without partial conditions": {
			object: map[string]interface{}{
				"status": map[string]interface{}{"conditions": []interface{}{readyCondition, "invalid"}},
			},
			expectedError: "NodeClass status.conditions[1] must be an object",
		},
		"When status.conditions is null, it should return a type error": {
			object:        map[string]interface{}{"status": map[string]interface{}{"conditions": nil}},
			expectedError: "failed to read NodeClass status.conditions",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			nc := &nodeClass{object: &unstructured.Unstructured{Object: tc.object}}
			original := nc.object.DeepCopy()

			status, err := nc.status()

			g.Expect(nc.object).To(Equal(original))
			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedError)))
				g.Expect(status).To(Equal(nodeClassStatus{}))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(status.ReleaseImage).To(Equal(tc.expectedImage))
			g.Expect(status.Version).To(Equal(tc.expectedVersion))
			if len(tc.expectedConditions) == 0 {
				g.Expect(status.Conditions).To(BeEmpty())
			} else {
				g.Expect(status.Conditions).To(BeComparableTo(tc.expectedConditions))
				status.Conditions[0].Message = "Modified locally"
			}
			status.ReleaseImage = ""
			status.Version = ""
			g.Expect(nc.object).To(Equal(original))
		})
	}
}

func TestSetStatus(t *testing.T) {
	transitionTime := metav1.NewTime(time.Date(2026, time.October, 15, 10, 0, 0, 0, time.UTC))
	condition := metav1.Condition{
		Type:               "VersionResolved",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: 7,
		LastTransitionTime: transitionTime,
		Reason:             "VersionResolved",
		Message:            "Version resolved successfully",
	}
	rawCondition := map[string]interface{}{
		"type":               "VersionResolved",
		"status":             "True",
		"observedGeneration": int64(7),
		"lastTransitionTime": "2026-10-15T10:00:00Z",
		"reason":             "VersionResolved",
		"message":            "Version resolved successfully",
	}
	releaseImage := "quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64"
	testCases := map[string]struct {
		object         map[string]interface{}
		status         nodeClassStatus
		expectedStatus map[string]interface{}
		expectedError  string
	}{
		"When status is absent, it should create the common status fields": {
			object: map[string]interface{}{},
			status: nodeClassStatus{
				Conditions:   []metav1.Condition{condition},
				ReleaseImage: releaseImage,
				Version:      "4.21.10",
			},
			expectedStatus: map[string]interface{}{
				"conditions":   []interface{}{rawCondition},
				"releaseImage": releaseImage,
				"version":      "4.21.10",
			},
		},
		"When status is populated, it should replace the common fields and preserve provider fields spec and metadata": {
			object: map[string]interface{}{
				"apiVersion": "karpenter.hypershift.openshift.io/v1",
				"kind":       "OpenshiftEC2NodeClass",
				"metadata": map[string]interface{}{
					"name":            "default",
					"resourceVersion": "7",
					"annotations":     map[string]interface{}{"hypershift.openshift.io/nodeClassCurrentConfigVersion": "config-hash"},
				},
				"spec": map[string]interface{}{"version": "4.21.10"},
				"status": map[string]interface{}{
					"releaseImage":    "quay.io/openshift-release-dev/ocp-release:4.21.9-x86_64",
					"version":         "4.21.9",
					"conditions":      []interface{}{map[string]interface{}{"type": "VersionResolved", "status": "False"}},
					"instanceProfile": "hypershift-worker-profile",
					"subnets":         []interface{}{map[string]interface{}{"id": "subnet-0123456789abcdef0", "zone": "us-east-1a"}},
				},
			},
			status: nodeClassStatus{
				Conditions:   []metav1.Condition{condition},
				ReleaseImage: releaseImage,
				Version:      "4.21.10",
			},
			expectedStatus: map[string]interface{}{
				"conditions":      []interface{}{rawCondition},
				"releaseImage":    releaseImage,
				"version":         "4.21.10",
				"instanceProfile": "hypershift-worker-profile",
				"subnets":         []interface{}{map[string]interface{}{"id": "subnet-0123456789abcdef0", "zone": "us-east-1a"}},
			},
		},
		"When the desired image is empty, it should remove the existing releaseImage key": {
			object: map[string]interface{}{"status": map[string]interface{}{"releaseImage": releaseImage, "version": "4.21.10"}},
			status: nodeClassStatus{Version: "4.21.10"},
			expectedStatus: map[string]interface{}{
				"version": "4.21.10",
			},
		},
		"When the desired version is empty, it should remove the existing version key": {
			object: map[string]interface{}{"status": map[string]interface{}{"releaseImage": releaseImage, "version": "4.21.10"}},
			status: nodeClassStatus{ReleaseImage: releaseImage},
			expectedStatus: map[string]interface{}{
				"releaseImage": releaseImage,
			},
		},
		"When the desired status is empty, it should remove the common keys and retain provider status": {
			object: map[string]interface{}{"status": map[string]interface{}{
				"conditions":      []interface{}{rawCondition},
				"releaseImage":    releaseImage,
				"version":         "4.21.10",
				"instanceProfile": "hypershift-worker-profile",
			}},
			expectedStatus: map[string]interface{}{"instanceProfile": "hypershift-worker-profile"},
		},
		"When desired fields are empty and absent, it should not create empty field values": {
			object:         map[string]interface{}{},
			expectedStatus: map[string]interface{}{},
		},
		"When status is not an object, it should return an error without changing the resource": {
			object:        map[string]interface{}{"status": "invalid"},
			status:        nodeClassStatus{ReleaseImage: releaseImage},
			expectedError: "failed to read NodeClass status",
		},
		"When status is null, it should return an error without changing the resource": {
			object:        map[string]interface{}{"status": nil},
			status:        nodeClassStatus{ReleaseImage: releaseImage},
			expectedError: "failed to read NodeClass status",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			nc := &nodeClass{object: &unstructured.Unstructured{Object: tc.object}}
			original := nc.object.DeepCopy()

			err := nc.setStatus(tc.status)

			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedError)))
				g.Expect(nc.object).To(Equal(original))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			status, found, err := unstructured.NestedMap(nc.object.Object, "status")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(found).To(BeTrue())
			g.Expect(status).To(Equal(tc.expectedStatus))

			updated := nc.object.DeepCopy()
			unstructured.RemoveNestedField(updated.Object, "status")
			unstructured.RemoveNestedField(original.Object, "status")
			g.Expect(updated).To(Equal(original))
		})
	}

	t.Run("When a version condition is changed through typed status, it should preserve unrelated conditions and provider fields", func(t *testing.T) {
		g := NewWithT(t)
		readyCondition := map[string]interface{}{
			"type":               "Ready",
			"status":             "True",
			"observedGeneration": int64(7),
			"lastTransitionTime": "2026-10-15T10:00:00Z",
			"reason":             "Ready",
			"message":            "NodeClass is ready",
		}
		nc := &nodeClass{object: &unstructured.Unstructured{Object: map[string]interface{}{
			"status": map[string]interface{}{
				"conditions":      []interface{}{readyCondition, rawCondition},
				"releaseImage":    releaseImage,
				"version":         "4.21.10",
				"instanceProfile": "hypershift-worker-profile",
			},
		}}}

		status, err := nc.status()
		g.Expect(err).NotTo(HaveOccurred())
		updatedCondition := condition
		updatedCondition.Status = metav1.ConditionFalse
		updatedCondition.Reason = "ResolutionFailed"
		updatedCondition.Message = "Release image could not be resolved"
		meta.SetStatusCondition(&status.Conditions, updatedCondition)
		status.ReleaseImage = ""

		g.Expect(nc.setStatus(status)).To(Succeed())

		conditions, found, err := unstructured.NestedSlice(nc.object.Object, "status", "conditions")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(found).To(BeTrue())
		g.Expect(conditions).To(HaveLen(2))
		g.Expect(conditions[0]).To(Equal(readyCondition))
		g.Expect(conditions[1]).To(HaveKeyWithValue("status", "False"))
		g.Expect(conditions[1]).To(HaveKeyWithValue("reason", "ResolutionFailed"))
		g.Expect(nc.object.Object["status"]).To(HaveKeyWithValue("instanceProfile", "hypershift-worker-profile"))
		g.Expect(nc.object.Object["status"]).To(HaveKeyWithValue("version", "4.21.10"))
		g.Expect(nc.object.Object["status"]).NotTo(HaveKey("releaseImage"))
	})
}
