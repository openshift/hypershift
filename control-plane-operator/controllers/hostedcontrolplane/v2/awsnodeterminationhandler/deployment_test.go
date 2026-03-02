package awsnodeterminationhandler

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestAdaptDeployment(t *testing.T) {
	testCases := []struct {
		name             string
		hcpAnnotations   map[string]string
		queueURL         string
		awsRegion        string
		issuerURL        string
		expectedReplicas int32
		expectedRegion   string
		expectedQueueURL string
	}{
		{
			name:             "When no annotations it should set replicas to 1",
			hcpAnnotations:   nil,
			awsRegion:        "us-east-1",
			expectedReplicas: 1,
			expectedRegion:   "us-east-1",
			expectedQueueURL: "",
		},
		{
			name:             "When queue URL is set in the API it should configure the queue URL",
			queueURL:         "https://sqs.us-west-2.amazonaws.com/123456789/my-queue",
			awsRegion:        "us-west-2",
			expectedReplicas: 1,
			expectedRegion:   "us-west-2",
			expectedQueueURL: "https://sqs.us-west-2.amazonaws.com/123456789/my-queue",
		},
		{
			name: "When disable annotation is set it should set replicas to 0",
			hcpAnnotations: map[string]string{
				hyperv1.DisableAWSNodeTerminationHandlerAnnotation: "true",
			},
			awsRegion:        "us-east-1",
			expectedReplicas: 0,
			expectedRegion:   "us-east-1",
		},
		{
			name: "When queue URL is set and disable annotation is set it should set replicas to 0",
			hcpAnnotations: map[string]string{
				hyperv1.DisableAWSNodeTerminationHandlerAnnotation: "true",
			},
			queueURL:         "https://sqs.us-east-1.amazonaws.com/123456789/my-queue",
			awsRegion:        "us-east-1",
			expectedReplicas: 0,
			expectedRegion:   "us-east-1",
			expectedQueueURL: "https://sqs.us-east-1.amazonaws.com/123456789/my-queue",
		},
	}

	// Note: Tests for queue URL from API are covered in TestGetTerminationHandlerQueueURL
	// and the adaptDeployment function uses the same helper

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hcp",
					Namespace:   "hcp-namespace",
					Annotations: tc.hcpAnnotations,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID:   "test-infra-id",
					IssuerURL: tc.issuerURL,
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: tc.awsRegion,
						},
					},
				},
			}
			if tc.queueURL != "" {
				hcp.Spec.Platform.AWS.TerminationHandlerQueueURL = ptr.To(tc.queueURL)
			}

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(deployment.Spec.Replicas).To(HaveValue(Equal(tc.expectedReplicas)))

			// Check env vars in the aws-node-termination-handler container
			var regionValue, queueURLValue string
			for _, container := range deployment.Spec.Template.Spec.Containers {
				if container.Name == ComponentName {
					for _, env := range container.Env {
						switch env.Name {
						case "AWS_REGION":
							regionValue = env.Value
						case "QUEUE_URL":
							queueURLValue = env.Value
						}
					}
				}
			}

			g.Expect(regionValue).To(Equal(tc.expectedRegion))
			g.Expect(queueURLValue).To(Equal(tc.expectedQueueURL))
		})
	}
}

func TestGetTerminationHandlerQueueURL(t *testing.T) {
	testCases := []struct {
		name        string
		hcp         *hyperv1.HostedControlPlane
		expectedURL string
	}{
		{
			name:        "When HCP is nil, it should return empty string",
			hcp:         nil,
			expectedURL: "",
		},
		{
			name: "When API has queue URL, it should return API value",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							TerminationHandlerQueueURL: ptr.To("https://sqs.us-east-1.amazonaws.com/123456789/api-queue"),
						},
					},
				},
			},
			expectedURL: "https://sqs.us-east-1.amazonaws.com/123456789/api-queue",
		},
		{
			name: "When API has no URL, it should return empty string",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS:  &hyperv1.AWSPlatformSpec{},
					},
				},
			},
			expectedURL: "",
		},
		{
			name: "When AWS platform spec is nil, it should return empty string",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			expectedURL: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := getTerminationHandlerQueueURL(tc.hcp)
			g.Expect(result).To(Equal(tc.expectedURL))
		})
	}
}
