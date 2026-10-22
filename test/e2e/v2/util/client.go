//go:build e2ev2

package util

import (
	"fmt"
	"time"

	hyperapi "github.com/openshift/hypershift/support/api"

	autoscalingv1 "github.com/openshift/cluster-autoscaler-operator/pkg/apis/autoscaling/v1"
	autoscalingv1beta1 "github.com/openshift/cluster-autoscaler-operator/pkg/apis/autoscaling/v1beta1"

	awskarpenterapis "github.com/aws/karpenter-provider-aws/pkg/apis"
	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

func init() {
	runtime.Must(operatorsv1.AddToScheme(hyperapi.Scheme))
	runtime.Must(operatorsv1alpha1.AddToScheme(hyperapi.Scheme))
	awsGroupVersion := schema.GroupVersion{Group: awskarpenterapis.Group, Version: "v1"}
	metav1.AddToGroupVersion(hyperapi.Scheme, awsGroupVersion)
	hyperapi.Scheme.AddKnownTypes(awsGroupVersion, &awskarpenterv1.EC2NodeClass{}, &awskarpenterv1.EC2NodeClassList{})
	runtime.Must(autoscalingv1.SchemeBuilder.AddToScheme(hyperapi.Scheme))
	runtime.Must(autoscalingv1beta1.SchemeBuilder.AddToScheme(hyperapi.Scheme))
}

// GetConfig loads the management cluster REST configuration with client-side
// rate limiting disabled and a bounded request timeout.
func GetConfig() (*rest.Config, error) {
	config, err := ctrl.GetConfig()
	if err != nil {
		return nil, err
	}
	config.QPS = -1
	config.Burst = -1
	config.Timeout = 5 * time.Minute
	return config, nil
}

// GetClient returns a management cluster client with the v2 test resource scheme.
func GetClient() (crclient.Client, error) {
	config, err := GetConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes config: %w", err)
	}
	client, err := crclient.New(config, crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes client: %w", err)
	}
	return client, nil
}
