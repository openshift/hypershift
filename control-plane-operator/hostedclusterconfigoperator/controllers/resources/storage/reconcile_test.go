package storage

import (
	"testing"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileClusterCSIDriverKMSKey(t *testing.T) {
	now := metav1.NewTime(time.Now())

	tests := []struct {
		name         string
		driver       *operatorv1.ClusterCSIDriver
		kmsKeyARN    string
		expectKMSKey string
	}{
		{
			name: "When resource is being created and initialKMSKeyARN is set, it should write the key",
			driver: &operatorv1.ClusterCSIDriver{
				ObjectMeta: metav1.ObjectMeta{Name: "ebs.csi.aws.com"},
			},
			kmsKeyARN:    "arn:aws:kms:us-east-1:123456789012:key/test-key",
			expectKMSKey: "arn:aws:kms:us-east-1:123456789012:key/test-key",
		},
		{
			name: "When resource is being created and initialKMSKeyARN is empty, it should not write DriverConfig",
			driver: &operatorv1.ClusterCSIDriver{
				ObjectMeta: metav1.ObjectMeta{Name: "ebs.csi.aws.com"},
			},
			kmsKeyARN:    "",
			expectKMSKey: "",
		},
		{
			name: "When resource already exists, it should skip even if initialKMSKeyARN is set",
			driver: &operatorv1.ClusterCSIDriver{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "ebs.csi.aws.com",
					CreationTimestamp: now,
				},
			},
			kmsKeyARN:    "arn:aws:kms:us-east-1:123456789012:key/test-key",
			expectKMSKey: "",
		},
		{
			name: "When resource already exists and admin cleared DriverConfig, it should not re-populate",
			driver: &operatorv1.ClusterCSIDriver{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "ebs.csi.aws.com",
					CreationTimestamp: now,
				},
				Spec: operatorv1.ClusterCSIDriverSpec{},
			},
			kmsKeyARN:    "arn:aws:kms:us-east-1:123456789012:key/test-key",
			expectKMSKey: "",
		},
		{
			name: "When resource already exists and admin changed DriverConfig, it should preserve admin key",
			driver: &operatorv1.ClusterCSIDriver{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "ebs.csi.aws.com",
					CreationTimestamp: now,
				},
				Spec: operatorv1.ClusterCSIDriverSpec{
					DriverConfig: operatorv1.CSIDriverConfigSpec{
						DriverType: operatorv1.AWSDriverType,
						AWS: &operatorv1.AWSCSIDriverConfigSpec{
							KMSKeyARN: "arn:aws:kms:us-east-1:123456789012:key/admin-rotated-key",
						},
					},
				},
			},
			kmsKeyARN:    "arn:aws:kms:us-east-1:123456789012:key/original-key",
			expectKMSKey: "arn:aws:kms:us-east-1:123456789012:key/admin-rotated-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ReconcileClusterCSIDriverKMSKey(tt.driver, tt.kmsKeyARN)

			// Check KMS key
			actualKey := ""
			if tt.driver.Spec.DriverConfig.AWS != nil {
				actualKey = tt.driver.Spec.DriverConfig.AWS.KMSKeyARN
			}
			if actualKey != tt.expectKMSKey {
				t.Errorf("expected KMSKeyARN=%q, got %q", tt.expectKMSKey, actualKey)
			}
		})
	}
}
