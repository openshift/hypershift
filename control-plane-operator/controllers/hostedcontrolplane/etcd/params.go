package etcd

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

type EtcdParams struct {
	ClusterSize        int
	ClusterVersion     string
	EtcdOperatorImage  string
	OwnerReference     *metav1.OwnerReference            `json:"ownerReference"`
	OperatorScheduling config.Scheduling                 `json:"operatorScheduling"`
	EtcdScheduling     config.Scheduling                 `json:"etcdScheduling"`
	AdditionalLabels   config.AdditionalLabels           `json:"additionalLabels"`
	SecurityContexts   config.SecurityContextSpec        `json:"securityContexts"`
	LivenessProbes     config.LivenessProbes             `json:"livenessProbes"`
	ReadinessProbes    config.ReadinessProbes            `json:"readinessProbes"`
	Resources          config.ResourcesSpec              `json:"resources"`
	PVCClaim           *corev1.PersistentVolumeClaimSpec `json:"pvcClaim"`
}

func NewEtcdParams(hcp *hyperv1.HostedControlPlane, images map[string]string) *EtcdParams {
	p := &EtcdParams{
		EtcdOperatorImage: images["etcd-operator"],
		OwnerReference:    config.ControllerOwnerRef(hcp),
		ClusterVersion:    config.DefaultEtcdClusterVersion,
		Resources: config.ResourcesSpec{
			etcdOperatorContainer().Name: {
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("50Mi"),
					corev1.ResourceCPU:    resource.MustParse("10m"),
				},
			},
			etcdContainer().Name: {
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("600Mi"),
					corev1.ResourceCPU:    resource.MustParse("300m"),
				},
			},
		},
	}
	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		p.ClusterSize = 3
	default:
		p.ClusterSize = 1
	}
	return p
}
