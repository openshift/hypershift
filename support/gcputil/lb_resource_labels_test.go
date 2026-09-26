package gcputil

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestReconcileLBResourceLabelAnnotations(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		desired     map[string]string
		want        map[string]string
		changed     bool
	}{
		{
			name: "When adding HCP labels, it should preserve service-owner labels",
			annotations: map[string]string{
				LBResourceLabelsAnnotation: "team=payments",
			},
			desired: map[string]string{"env": "prod"},
			want: map[string]string{
				LBResourceLabelsAnnotation:        "env=prod,team=payments",
				ManagedLBResourceLabelsAnnotation: "env",
			},
			changed: true,
		},
		{
			name: "When updating HCP labels, it should preserve service-owner labels",
			annotations: map[string]string{
				LBResourceLabelsAnnotation:        "env=dev,team=payments",
				ManagedLBResourceLabelsAnnotation: "env",
			},
			desired: map[string]string{"env": "prod"},
			want: map[string]string{
				LBResourceLabelsAnnotation:        "env=prod,team=payments",
				ManagedLBResourceLabelsAnnotation: "env",
			},
			changed: true,
		},
		{
			name: "When HCP labels are withdrawn, it should remove only those labels",
			annotations: map[string]string{
				LBResourceLabelsAnnotation:        "env=prod,team=payments",
				ManagedLBResourceLabelsAnnotation: "env",
			},
			want: map[string]string{
				LBResourceLabelsAnnotation: "team=payments",
			},
			changed: true,
		},
		{
			name: "When the final HCP label is withdrawn, it should clear the native annotation",
			annotations: map[string]string{
				LBResourceLabelsAnnotation:        "env=prod",
				ManagedLBResourceLabelsAnnotation: "env",
			},
			want: map[string]string{
				LBResourceLabelsAnnotation: "",
			},
			changed: true,
		},
		{
			name: "When there are no HCP labels, it should leave an unmanaged annotation unchanged",
			annotations: map[string]string{
				LBResourceLabelsAnnotation: "team=payments",
			},
			want: map[string]string{
				LBResourceLabelsAnnotation: "team=payments",
			},
			changed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			got, changed, err := ReconcileLBResourceLabelAnnotations(tt.annotations, tt.desired)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(changed).To(Equal(tt.changed))
			g.Expect(got).To(Equal(tt.want))
		})
	}
}
