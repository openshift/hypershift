package machinedeployment

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/openshift/hypershift/hypershift-operator/controllers/machinedeployment/utils"
	"github.com/openshift/hypershift/support/awsclient"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
)

const (
	// These expose compute information based on the providerSpec input.
	// This is needed by the autoscaler to foresee upcoming capacity when scaling from zero.
	// https://github.com/openshift/enhancements/pull/186
	cpuKey       = "machine.openshift.io/vCPU"
	memoryKey    = "machine.openshift.io/memoryMb"
	gpuKey       = "machine.openshift.io/GPU"
	labelsKey    = "capacity.cluster-autoscaler.kubernetes.io/labels"
	archLabelKey = "kubernetes.io/arch"
)

// Reconciler reconciles MachineDeployments for scale-from-zero autoscaling.
type Reconciler struct {
	Client             client.Client
	Log                logr.Logger
	CredentialsFile    string
	RegionCache        awsclient.RegionCache
	InstanceTypesCache InstanceTypesCache

	recorder record.EventRecorder
	scheme   *runtime.Scheme
}

// SetupWithManager creates a new controller for a manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&clusterv1.MachineDeployment{}).
		Build(r)

	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}

	r.recorder = mgr.GetEventRecorderFor("machinedeployment-controller")
	r.scheme = mgr.GetScheme()
	return nil
}

// Reconcile implements controller runtime Reconciler interface.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("machinedeployment", req.Name, "namespace", req.Namespace)
	logger.V(3).Info("Reconciling")

	machineDeployment := &clusterv1.MachineDeployment{}
	if err := r.Client.Get(ctx, req.NamespacedName, machineDeployment); err != nil {
		if apierrors.IsNotFound(err) {
			// Object not found, return. Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	// Ignore deleted MachineDeployments, this can happen when foregroundDeletion
	// is enabled
	if !machineDeployment.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	originalMachineDeploymentToPatch := client.MergeFrom(machineDeployment.DeepCopy())

	result, err := r.reconcile(ctx, machineDeployment)
	if err != nil {
		logger.Error(err, "Failed to reconcile MachineDeployment")
		r.recorder.Eventf(machineDeployment, corev1.EventTypeWarning, "ReconcileError", "%v", err)
		// we don't return here so we want to attempt to patch the machine regardless of an error.
	}

	if err := r.Client.Patch(ctx, machineDeployment, originalMachineDeploymentToPatch); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to patch machineDeployment: %v", err)
	}

	return result, err
}

func (r *Reconciler) reconcile(ctx context.Context, machineDeployment *clusterv1.MachineDeployment) (ctrl.Result, error) {
	klog.V(3).Infof("%v: Reconciling MachineDeployment", machineDeployment.Name)

	// Resolve AWSMachineTemplate
	awsMachineTemplate, err := utils.ResolveAWSMachineTemplate(ctx, r.Client, machineDeployment)
	if err != nil {
		klog.V(3).Infof("Failed to resolve AWSMachineTemplate: %v (skipping, may not be AWS)", err)
		// Not an error - this MachineDeployment may not be for AWS platform
		return ctrl.Result{}, nil
	}

	// Extract instance type
	instanceType, err := utils.ExtractInstanceType(awsMachineTemplate)
	if err != nil {
		klog.Errorf("Failed to extract instance type: %v", err)
		r.recorder.Eventf(machineDeployment, corev1.EventTypeWarning, "FailedUpdate", "Failed to extract instance type: %v", err)
		return ctrl.Result{}, err
	}

	// Resolve AWS region
	region, err := utils.ResolveRegion(ctx, r.Client, machineDeployment)
	if err != nil {
		klog.Errorf("Failed to resolve AWS region: %v", err)
		r.recorder.Eventf(machineDeployment, corev1.EventTypeWarning, "FailedUpdate", "Failed to resolve AWS region: %v", err)
		return ctrl.Result{}, err
	}

	// Create AWS client with region validation
	awsClient, err := awsclient.NewValidatedClient(region, r.CredentialsFile, r.RegionCache)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error creating aws client: %w", err)
	}

	// Get instance type information
	instanceTypeInfo, err := r.InstanceTypesCache.GetInstanceType(awsClient, region, instanceType)
	if err != nil {
		klog.Errorf("Unable to set scale from zero annotations: unknown instance type %s: %v", instanceType, err)
		klog.Errorf("Autoscaling from zero will not work. To fix this, manually populate machine annotations for your instance type: %v", []string{cpuKey, memoryKey, gpuKey})

		r.recorder.Eventf(machineDeployment, corev1.EventTypeWarning, "FailedUpdate", "Failed to set autoscaling from zero annotations, instance type unknown")
		return ctrl.Result{}, nil
	}

	// Set annotations
	if machineDeployment.Annotations == nil {
		machineDeployment.Annotations = make(map[string]string)
	}

	machineDeployment.Annotations[cpuKey] = strconv.FormatInt(int64(instanceTypeInfo.VCPU), 10)
	machineDeployment.Annotations[memoryKey] = strconv.FormatInt(instanceTypeInfo.MemoryMb, 10)
	machineDeployment.Annotations[gpuKey] = strconv.FormatInt(int64(instanceTypeInfo.GPU), 10)

	// Parse existing labels, update architecture, and preserve user-provided labels
	labelsMap := make(map[string]string)
	if existingLabels, ok := machineDeployment.Annotations[labelsKey]; ok && existingLabels != "" {
		// Parse comma-separated labels into map
		for _, label := range strings.Split(existingLabels, ",") {
			parts := strings.SplitN(strings.TrimSpace(label), "=", 2)
			if len(parts) == 2 {
				labelsMap[parts[0]] = parts[1]
			}
		}
	}

	// Update or add architecture label
	labelsMap[archLabelKey] = string(instanceTypeInfo.CPUArchitecture)

	// Serialize back to comma-separated format
	labels := make([]string, 0, len(labelsMap))
	for k, v := range labelsMap {
		labels = append(labels, fmt.Sprintf("%s=%s", k, v))
	}
	// Sort for deterministic output in tests
	sort.Strings(labels)
	machineDeployment.Annotations[labelsKey] = strings.Join(labels, ",")

	return ctrl.Result{}, nil
}
