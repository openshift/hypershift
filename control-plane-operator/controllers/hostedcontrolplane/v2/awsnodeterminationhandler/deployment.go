package awsnodeterminationhandler

import (
	"fmt"
	"strconv"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

const tokenMinterKubeContainerName = "token-minter-kube"

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP

	// Get AWS region from HCP spec
	awsRegion := ""
	if hcp.Spec.Platform.AWS != nil {
		awsRegion = hcp.Spec.Platform.AWS.Region
	}

	// Get SQS queue URL from API
	queueURL := getTerminationHandlerQueueURL(hcp)

	// Get OIDC provider URL for token audience
	issuerURL := ""
	if hcp.Spec.IssuerURL != "" {
		issuerURL = hcp.Spec.IssuerURL
	}

	// Update the aws-node-termination-handler container environment variables and image
	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		// Set the image - AWS Node Termination Handler is not in the OCP release payload
		c.Image = DefaultAWSNodeTerminationHandlerImage

		for i := range c.Env {
			switch c.Env[i].Name {
			case "AWS_REGION":
				c.Env[i].Value = awsRegion
			case "QUEUE_URL":
				c.Env[i].Value = queueURL
			case "KUBERNETES_SERVICE_HOST":
				c.Env[i].Value = "kube-apiserver"
			case "KUBERNETES_SERVICE_PORT":
				c.Env[i].Value = strconv.Itoa(config.KASSVCPort)
			}
		}
	})

	// Update the token-minter-kube container with the proper audience
	// The token-minter command is embedded in a shell script, so we need to do string replacement
	util.UpdateContainer(tokenMinterKubeContainerName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		for i := range c.Args {
			if strings.Contains(c.Args[i], "--token-audience=") {
				c.Args[i] = strings.Replace(c.Args[i], "--token-audience=", fmt.Sprintf("--token-audience=%s", issuerURL), 1)
				break
			}
		}
	})

	// Set replicas based on whether termination handler is needed
	// If the disable annotation is present, scale to 0 replicas
	deployment.Spec.Replicas = ptr.To[int32](1)
	if _, exists := hcp.Annotations[hyperv1.DisableAWSNodeTerminationHandlerAnnotation]; exists {
		deployment.Spec.Replicas = ptr.To[int32](0)
	}

	return nil
}
