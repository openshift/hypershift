package oadp

import (
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
)

// CreateOptions contains all configuration for both backup and restore operations
type CreateOptions struct {
	// Required flags (common)
	HCName      string
	HCNamespace string

	// Backup-specific required flags
	// (none currently)

	// Restore-specific required flags
	BackupName   string
	ScheduleName string

	// Schedule-specific required flags
	Schedule string

	// Optional flags with defaults (common)
	OADPNamespace string
	Render        bool

	// Backup-specific optional flags
	BackupCustomName         string
	StorageLocation          string
	TTL                      time.Duration
	SnapshotMoveData         bool
	DefaultVolumesToFsBackup bool
	IncludedResources        []string

	// Restore-specific optional flags
	RestoreName            string
	ExistingResourcePolicy string
	IncludeNamespaces      []string
	RestorePVs             *bool
	PreserveNodePorts      *bool

	// Schedule-specific optional flags
	Paused             bool
	UseOwnerReferences bool
	SkipImmediately    bool

	// Etcd snapshot mode flag (common to backup, restore, schedule).
	// When true, etcd is backed up via HCPEtcdBackup CRD snapshots
	// instead of PV volume snapshots, producing lighter manifests.
	UseEtcdSnapshot bool

	// Client context (common)
	Log    logr.Logger
	Client client.Client
}

var (
	// Base resources common to all platforms
	baseResources = []string{
		"serviceaccounts", "roles", "rolebindings", "pods", "persistentvolumeclaims", "persistentvolumes", "configmaps",
		"priorityclasses", "poddisruptionbudgets", "hostedclusters.hypershift.openshift.io", "nodepools.hypershift.openshift.io",
		"secrets", "services", "deployments", "statefulsets",
		"hostedcontrolplanes.hypershift.openshift.io", "clusters.cluster.x-k8s.io",
		"machinedeployments.cluster.x-k8s.io", "machinesets.cluster.x-k8s.io", "machines.cluster.x-k8s.io",
		"routes.route.openshift.io", "clusterdeployments.hive.openshift.io",
	}

	// Platform-specific resources constants
	awsResources = []string{
		"awsclusters.infrastructure.cluster.x-k8s.io", "awsmachinetemplates.infrastructure.cluster.x-k8s.io", "awsmachines.infrastructure.cluster.x-k8s.io",
	}
	agentResources = []string{
		"agentclusters.capi-provider.agent-install.openshift.io", "agentmachinetemplates.capi-provider.agent-install.openshift.io", "agentmachines.capi-provider.agent-install.openshift.io",
		"agents.agent-install.openshift.io", "infraenvs.agent-install.openshift.io", "nmstateconfigs.agent-install.openshift.io", "baremetalhosts.metal3.io",
	}
	kubevirtResources = []string{
		"kubevirtclusters.infrastructure.cluster.x-k8s.io", "kubevirtmachinetemplates.infrastructure.cluster.x-k8s.io",
		"datavolumes.cdi.kubevirt.io",
	}
	openstackResources = []string{
		"openstackclusters.infrastructure.cluster.x-k8s.io", "openstackmachinetemplates.infrastructure.cluster.x-k8s.io", "openstackmachines.infrastructure.cluster.x-k8s.io",
	}
	azureResources = []string{
		"azureclusters.infrastructure.cluster.x-k8s.io", "azuremachinetemplates.infrastructure.cluster.x-k8s.io", "azuremachines.infrastructure.cluster.x-k8s.io",
	}

	// Platform resource mapping
	platformResourceMap = map[string][]string{
		"AWS":       awsResources,
		"AGENT":     agentResources,
		"KUBEVIRT":  kubevirtResources,
		"OPENSTACK": openstackResources,
		"AZURE":     azureResources,
	}

	// Default excluded resources for restore operations
	defaultExcludedResources = []string{
		"nodes",
		"events",
		"events.events.k8s.io",
		"backups.velero.io",
		"restores.velero.io",
		"resticrepositories.velero.io",
		"csinodes.storage.k8s.io",
		"volumeattachments.storage.k8s.io",
		"backuprepositories.velero.io",
	}

	// Base resources for etcd snapshot mode: same as baseResources but without PV-related
	// resources (persistentvolumeclaims, persistentvolumes) and workload controllers
	// (deployments, statefulsets) since etcd is backed up via CRD snapshots. Adds namespaces.
	baseResourcesEtcdSnapshot = []string{
		"serviceaccounts", "roles", "rolebindings", "pods", "configmaps",
		"priorityclasses", "poddisruptionbudgets",
		"hostedclusters.hypershift.openshift.io", "nodepools.hypershift.openshift.io",
		"secrets", "services",
		"hostedcontrolplanes.hypershift.openshift.io", "clusters.cluster.x-k8s.io",
		"machinedeployments.cluster.x-k8s.io", "machinesets.cluster.x-k8s.io", "machines.cluster.x-k8s.io",
		"routes.route.openshift.io", "clusterdeployments.hive.openshift.io",
		"namespaces",
	}

	// Excluded resources for restore in etcd snapshot mode
	excludedResourcesEtcdSnapshot = []string{
		"nodes",
		"events",
		"events.events.k8s.io",
		"backups.velero.io",
		"restores.velero.io",
		"resticrepositories.velero.io",
	}
)
