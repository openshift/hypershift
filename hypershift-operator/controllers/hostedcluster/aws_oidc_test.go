package hostedcluster

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"go.uber.org/mock/gomock"
)

func TestCleanupAWSOIDCBucketData(t *testing.T) {
	tests := []struct {
		name            string
		hcluster        *hyperv1.HostedCluster
		setupS3Mock     func(*gomock.Controller) awsapi.S3API
		bucketName      string
		expectErr       bool
		expectErrMsg    string
		expectFinalizer bool
	}{
		{
			name: "When cleanup succeeds, it should delete objects and remove finalizer",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  "clusters",
					Finalizers: []string{oidcDocumentsFinalizer},
				},
				Spec: hyperv1.HostedClusterSpec{InfraID: "test-infra"},
			},
			setupS3Mock: func(ctrl *gomock.Controller) awsapi.S3API {
				m := awsapi.NewMockS3API(ctrl)
				m.EXPECT().DeleteObjects(gomock.Any(), gomock.Any()).
					Return(&s3.DeleteObjectsOutput{}, nil)
				return m
			},
			bucketName:      "my-bucket",
			expectFinalizer: false,
		},
		{
			name: "When DeleteObjects fails with a non-NoSuchBucket error, it should return error and keep finalizer",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  "clusters",
					Finalizers: []string{oidcDocumentsFinalizer},
				},
				Spec: hyperv1.HostedClusterSpec{InfraID: "test-infra"},
			},
			setupS3Mock: func(ctrl *gomock.Controller) awsapi.S3API {
				m := awsapi.NewMockS3API(ctrl)
				m.EXPECT().DeleteObjects(gomock.Any(), gomock.Any()).
					Return(nil, fmt.Errorf("access denied"))
				return m
			},
			bucketName:      "my-bucket",
			expectErr:       true,
			expectErrMsg:    "failed to delete OIDC objects",
			expectFinalizer: true,
		},
		{
			name: "When DeleteObjects fails with NoSuchBucket, it should succeed and remove finalizer",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  "clusters",
					Finalizers: []string{oidcDocumentsFinalizer},
				},
				Spec: hyperv1.HostedClusterSpec{InfraID: "test-infra"},
			},
			setupS3Mock: func(ctrl *gomock.Controller) awsapi.S3API {
				m := awsapi.NewMockS3API(ctrl)
				m.EXPECT().DeleteObjects(gomock.Any(), gomock.Any()).
					Return(nil, &s3types.NoSuchBucket{Message: aws.String("bucket not found")})
				return m
			},
			bucketName:      "my-bucket",
			expectFinalizer: false,
		},
		{
			name: "When DeleteObjects returns partial failure, it should return error and keep finalizer",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  "clusters",
					Finalizers: []string{oidcDocumentsFinalizer},
				},
				Spec: hyperv1.HostedClusterSpec{InfraID: "test-infra"},
			},
			setupS3Mock: func(ctrl *gomock.Controller) awsapi.S3API {
				m := awsapi.NewMockS3API(ctrl)
				m.EXPECT().DeleteObjects(gomock.Any(), gomock.Any()).
					Return(&s3.DeleteObjectsOutput{
						Errors: []s3types.Error{
							{
								Key:     aws.String("test-infra/.well-known/openid-configuration"),
								Code:    aws.String("AccessDenied"),
								Message: aws.String("Access Denied"),
							},
						},
					}, nil)
				return m
			},
			bucketName:      "my-bucket",
			expectErr:       true,
			expectErrMsg:    "partial failure deleting OIDC objects",
			expectFinalizer: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(tc.hcluster).
				Build()

			mockCtrl := gomock.NewController(t)
			var s3Client awsapi.S3API
			if tc.setupS3Mock != nil {
				s3Client = tc.setupS3Mock(mockCtrl)
			}

			r := &HostedClusterReconciler{
				Client:                          fakeClient,
				S3Client:                        s3Client,
				OIDCStorageProviderS3BucketName: tc.bucketName,
			}

			err := r.cleanupOIDCBucketData(t.Context(), ctrl.Log, tc.hcluster)

			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectErrMsg))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			updatedHC := &hyperv1.HostedCluster{}
			g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(tc.hcluster), updatedHC)).To(Succeed())

			if tc.expectFinalizer {
				g.Expect(updatedHC.Finalizers).To(ContainElement(oidcDocumentsFinalizer))
			} else {
				g.Expect(updatedHC.Finalizers).ToNot(ContainElement(oidcDocumentsFinalizer))
			}
		})
	}
}
