package common

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
)

func TestPullSecret(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		namespace string
	}{
		{
			name:      "When namespace is provided, it should return a secret named pull-secret in the given namespace",
			namespace: "test-ns",
		},
		{
			name:      "When namespace is empty, it should return a secret named pull-secret with empty namespace",
			namespace: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			secret := PullSecret(tt.namespace)
			g.Expect(secret).ToNot(BeNil())
			g.Expect(secret.Name).To(Equal("pull-secret"))
			g.Expect(secret.Namespace).To(Equal(tt.namespace))
		})
	}
}

func TestDefaultServiceAccount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		namespace string
	}{
		{
			name:      "When namespace is provided, it should return a service account named default in the given namespace",
			namespace: "test-ns",
		},
		{
			name:      "When namespace is empty, it should return a service account named default with empty namespace",
			namespace: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			sa := DefaultServiceAccount(tt.namespace)
			g.Expect(sa).ToNot(BeNil())
			g.Expect(sa.Name).To(Equal("default"))
			g.Expect(sa.Namespace).To(Equal(tt.namespace))
		})
	}
}

func TestKubeadminPasswordSecret(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		namespace string
	}{
		{
			name:      "When namespace is provided, it should return a secret named kubeadmin-password in the given namespace",
			namespace: "test-ns",
		},
		{
			name:      "When namespace is empty, it should return a secret named kubeadmin-password with empty namespace",
			namespace: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			secret := KubeadminPasswordSecret(tt.namespace)
			g.Expect(secret).ToNot(BeNil())
			g.Expect(secret.Name).To(Equal("kubeadmin-password"))
			g.Expect(secret.Namespace).To(Equal(tt.namespace))
		})
	}
}

func TestVolumeTotalClientCA(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
	}{
		{
			name: "When called, it should return a volume named client-ca",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			vol := VolumeTotalClientCA()
			g.Expect(vol).ToNot(BeNil())
			g.Expect(vol.Name).To(Equal("client-ca"))
		})
	}
}

func TestBuildVolumeTotalClientCA(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
	}{
		{
			name: "When called, it should set the ConfigMap source with the correct name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			vol := VolumeTotalClientCA()
			BuildVolumeTotalClientCA(vol)
			g.Expect(vol.ConfigMap).ToNot(BeNil())
			g.Expect(vol.ConfigMap.Name).To(Equal(manifests.TotalClientCABundle("").Name))
		})
	}
}

func TestVolumeAggregatorCA(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
	}{
		{
			name: "When called, it should return a volume named aggregator-ca",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			vol := VolumeAggregatorCA()
			g.Expect(vol).ToNot(BeNil())
			g.Expect(vol.Name).To(Equal("aggregator-ca"))
		})
	}
}

func TestBuildVolumeAggregatorCA(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
	}{
		{
			name: "When called, it should set the ConfigMap source with the correct name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			vol := VolumeAggregatorCA()
			BuildVolumeAggregatorCA(vol)
			g.Expect(vol.ConfigMap).ToNot(BeNil())
			g.Expect(vol.ConfigMap.Name).To(Equal(manifests.AggregatorClientCAConfigMap("").Name))
		})
	}
}
