package etcd

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ShardParams contains all parameters needed to reconcile a single etcd shard
type ShardParams struct {
	// OwnerRef is the owner reference for created resources
	OwnerRef metav1.OwnerReference

	// EtcdImage is the container image to use for etcd
	EtcdImage string

	// ControlPlaneOperatorImage is the image for the control plane operator (used for defrag controller and init containers)
	ControlPlaneOperatorImage string

	// ClusterEtcdOperatorImage is the image for the cluster-etcd-operator (used for healthz sidecar)
	ClusterEtcdOperatorImage string

	// ClusterName is the name of the hosted cluster
	ClusterName string

	// Namespace is the namespace where etcd resources are deployed
	Namespace string

	// IPv4 indicates whether the cluster is using IPv4
	IPv4 bool

	// AvailabilityPolicy is the controller availability policy
	AvailabilityPolicy hyperv1.AvailabilityPolicy

	// RestoreSnapshotURL is the URL to restore etcd snapshot from (optional)
	RestoreSnapshotURL []string

	// SnapshotRestored indicates whether the snapshot has been restored
	SnapshotRestored bool

	// NeedsDefragController indicates whether to deploy the defrag controller
	NeedsDefragController bool

	// DefaultStorageSize is the default storage size for persistent volumes
	DefaultStorageSize string

	// ClusterID is the cluster ID for metrics labeling
	ClusterID string

	// SecurityContext contains pod security context settings
	SecurityContext *corev1.PodSecurityContext
}

// NewShardParams constructs ShardParams from HostedControlPlane
func NewShardParams(
	hcp *hyperv1.HostedControlPlane,
	etcdImage string,
	controlPlaneOperatorImage string,
	clusterEtcdOperatorImage string,
	snapshotRestored bool,
	securityContext *corev1.PodSecurityContext,
) (*ShardParams, error) {
	ipv4, err := util.IsIPv4CIDR(hcp.Spec.Networking.ClusterNetwork[0].CIDR.String())
	if err != nil {
		return nil, fmt.Errorf("error checking the ClusterNetworkCIDR: %w", err)
	}

	restoreSnapshotURL := []string{}
	if hcp.Spec.Etcd.ManagementType == hyperv1.Managed &&
		hcp.Spec.Etcd.Managed != nil &&
		len(hcp.Spec.Etcd.Managed.Storage.RestoreSnapshotURL) > 0 {
		restoreSnapshotURL = hcp.Spec.Etcd.Managed.Storage.RestoreSnapshotURL
	}

	needsDefragController := hcp.Spec.ControllerAvailabilityPolicy == hyperv1.HighlyAvailable

	return &ShardParams{
		OwnerRef: metav1.OwnerReference{
			APIVersion: hyperv1.GroupVersion.String(),
			Kind:       "HostedControlPlane",
			Name:       hcp.Name,
			UID:        hcp.UID,
		},
		EtcdImage:                 etcdImage,
		ControlPlaneOperatorImage: controlPlaneOperatorImage,
		ClusterEtcdOperatorImage:  clusterEtcdOperatorImage,
		ClusterName:               hcp.Name,
		Namespace:                 hcp.Namespace,
		IPv4:                      ipv4,
		AvailabilityPolicy:        hcp.Spec.ControllerAvailabilityPolicy,
		RestoreSnapshotURL:        restoreSnapshotURL,
		SnapshotRestored:          snapshotRestored,
		NeedsDefragController:     needsDefragController,
		DefaultStorageSize:        hyperv1.DefaultPersistentVolumeEtcdStorageSize.String(),
		ClusterID:                 hcp.Spec.ClusterID,
		SecurityContext:           securityContext,
	}, nil
}
