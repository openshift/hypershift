package gcputil

import (
	"errors"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

func TestReconcileLBResourceLabelAnnotations(t *testing.T) {
	tests := []struct {
		name            string
		annotations     map[string]string
		desired         map[string]string
		want            map[string]string
		changed         bool
		wantErr         string
		wantNativeError bool
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
		{
			name:        "When no labels are managed or desired, it should ignore malformed native input",
			annotations: map[string]string{LBResourceLabelsAnnotation: "not-a-label"},
			want:        map[string]string{LBResourceLabelsAnnotation: "not-a-label"},
		},
		{
			name:        "When no labels are desired but ownership metadata is invalid, it should return an error",
			annotations: map[string]string{ManagedLBResourceLabelsAnnotation: "INVALID"},
			wantErr:     "invalid " + ManagedLBResourceLabelsAnnotation,
		},
		{
			name:            "When withdrawing managed labels from malformed native input, it should return a typed native error",
			annotations:     map[string]string{LBResourceLabelsAnnotation: "not-a-label", ManagedLBResourceLabelsAnnotation: "env"},
			wantErr:         "must use key=value format",
			wantNativeError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			got, changed, err := ReconcileLBResourceLabelAnnotations(tt.annotations, tt.desired)
			if tt.wantErr != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.wantErr))
				var nativeErr *LBResourceLabelsValidationError
				g.Expect(errors.As(err, &nativeErr)).To(Equal(tt.wantNativeError))
				g.Expect(changed).To(BeFalse())
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(changed).To(Equal(tt.changed))
			g.Expect(got).To(Equal(tt.want))
		})
	}
}

func TestReconcileLBResourceLabelAnnotationsFirstTimeCollisionAdoption(t *testing.T) {
	tests := []struct {
		name         string
		serviceValue string
		hcpValue     string
	}{
		{
			name:         "When an HCP label matches a Service-owned label, it should adopt the key",
			serviceValue: "payments",
			hcpValue:     "payments",
		},
		{
			name:         "When an HCP label differs from a Service-owned label, it should adopt and replace the key",
			serviceValue: "payments",
			hcpValue:     "platform",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			initial := map[string]string{LBResourceLabelsAnnotation: "team=" + tt.serviceValue}

			adopted, changed, err := ReconcileLBResourceLabelAnnotations(initial, map[string]string{"team": tt.hcpValue})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(changed).To(BeTrue())
			g.Expect(adopted).To(Equal(map[string]string{
				LBResourceLabelsAnnotation:        "team=" + tt.hcpValue,
				ManagedLBResourceLabelsAnnotation: "team",
			}))

			withdrawn, changed, err := ReconcileLBResourceLabelAnnotations(adopted, nil)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(changed).To(BeTrue())
			g.Expect(withdrawn).To(Equal(map[string]string{LBResourceLabelsAnnotation: ""}))

			unchanged, changed, err := ReconcileLBResourceLabelAnnotations(withdrawn, nil)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(changed).To(BeFalse())
			g.Expect(unchanged).To(Equal(withdrawn))
		})
	}
}

func TestParseLBResourceLabelsAnnotation(t *testing.T) {
	for _, tt := range []struct {
		name, value, reason, detail string
	}{
		{name: "When a pair is malformed, it should expose a safe format reason", value: "private-input", reason: "expected key=value format", detail: "private-input"},
		{name: "When a key is invalid, it should expose a safe validation reason", value: "PRIVATE=value", reason: "invalid label key or value", detail: "PRIVATE"},
		{name: "When a value is invalid, it should expose a safe validation reason", value: "team=PRIVATE", reason: "invalid label key or value", detail: "PRIVATE"},
		{name: "When a key is duplicated, it should expose a safe duplicate reason", value: "private=a,private=b", reason: "duplicate label key", detail: "private"},
		{name: "When there are too many labels, it should expose a safe limit reason", value: strings.Repeat("team=a,", MaxResourceLabels) + "team=a", reason: "too many labels", detail: "at most"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			_, present, err := ParseLBResourceLabelsAnnotation(map[string]string{LBResourceLabelsAnnotation: tt.value})
			g.Expect(present).To(BeTrue())
			var validationErr *LBResourceLabelsValidationError
			g.Expect(errors.As(err, &validationErr)).To(BeTrue())
			g.Expect(validationErr.Reason).To(Equal(tt.reason))
			g.Expect(err.Error()).To(ContainSubstring(tt.detail))
		})
	}
}
