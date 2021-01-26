package controllers

import (
	"context"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	k8sutilspointer "k8s.io/utils/pointer"
	hyperv1 "openshift.io/hypershift/api/v1alpha1"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/api/v1alpha3"
	capiv1 "sigs.k8s.io/cluster-api/api/v1alpha4"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type NodePoolReconciler struct {
	ctrlclient.Client
	recorder record.EventRecorder
	Infra    *configv1.Infrastructure
	Log      logr.Logger
}

func (r *NodePoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.NodePool{}).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
		}).
		Build(r)
	if err != nil {
		return errors.Wrap(err, "failed setting up with a controller manager")
	}

	var infra configv1.Infrastructure
	if err := mgr.GetAPIReader().Get(context.Background(), client.ObjectKey{Name: "cluster"}, &infra); err != nil {
		return fmt.Errorf("failed to get cluster infra: %w", err)
	}
	r.Infra = &infra

	r.recorder = mgr.GetEventRecorderFor("nodepool-controller")

	return nil
}

func (r *NodePoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log = ctrl.LoggerFrom(ctx)
	r.Log.Info("Reconciling")

	// Fetch the nodePool instance
	nodePool := &hyperv1.NodePool{}
	err := r.Client.Get(ctx, req.NamespacedName, nodePool)
	if err != nil {
		if apierrors.IsNotFound(err) {
			r.Log.Info("not found")
			return ctrl.Result{}, nil
		}
		r.Log.Error(err, "error getting nodePool")
		return ctrl.Result{}, err
	}

	// Ignore deleted nodePools, this can happen when foregroundDeletion
	// is enabled
	if !nodePool.DeletionTimestamp.IsZero() {
		machineSet, _, err := generateScalableResources(r, ctx, r.Infra.Status.InfrastructureName, r.Infra.Status.PlatformStatus.AWS.Region, nodePool)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to generate worker machineset: %w", err)
		}
		if err := r.Delete(ctx, machineSet); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, fmt.Errorf("failed to delete nodePool: %w", err)
		}

		if controllerutil.ContainsFinalizer(nodePool, finalizer) {
			controllerutil.RemoveFinalizer(nodePool, finalizer)
			if err := r.Update(ctx, nodePool); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from nodePool: %w", err)
			}
		}
		r.Log.Info("Deleted machineSet", "machineset", machineSet.GetName())
		return ctrl.Result{}, nil
	}

	// Ensure the nodePool has a finalizer for cleanup
	if !controllerutil.ContainsFinalizer(nodePool, finalizer) {
		controllerutil.AddFinalizer(nodePool, finalizer)
		if err := r.Update(ctx, nodePool); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to nodePool: %w", err)
		}
	}

	ocluster, err := GetOClusterByName(ctx, r.Client, nodePool.GetNamespace(), nodePool.Spec.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Initialize the patch helper
	patchHelper, err := patch.NewHelper(nodePool, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	result, err := r.reconcile(ctx, ocluster, nodePool)
	if err != nil {
		r.Log.Error(err, "Failed to reconcile nodePool")
		r.recorder.Eventf(nodePool, corev1.EventTypeWarning, "ReconcileError", "%v", err)
		return result, err
	}

	if err := patchHelper.Patch(ctx, nodePool); err != nil {
		r.Log.Error(err, "failed to patch")
		return ctrl.Result{}, fmt.Errorf("failed to patch: %w", err)
	}

	r.Log.Info("Successfully reconciled")
	return result, nil
}

func (r *NodePoolReconciler) reconcile(ctx context.Context, ocluster *hyperv1.OpenShiftCluster, nodePool *hyperv1.NodePool) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconcile nodePool")

	nodePool.OwnerReferences = util.EnsureOwnerRef(nodePool.OwnerReferences, metav1.OwnerReference{
		APIVersion: hyperv1.GroupVersion.String(),
		Kind:       "OpenshiftCluster",
		Name:       ocluster.Name,
		UID:        ocluster.UID,
	})

	// Create a machine scalable resources for the new cluster's worker nodes
	machineSet, AWSMachineTemplate, err := generateScalableResources(r, ctx, r.Infra.Status.InfrastructureName, r.Infra.Status.PlatformStatus.AWS.Region, nodePool)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to generate worker machineset: %w", err)
	}

	if _, err := ctrl.CreateOrUpdate(ctx, r.Client, AWSMachineTemplate, func() error { return nil }); err != nil {
		return ctrl.Result{}, err
	}
	if _, err := ctrl.CreateOrUpdate(ctx, r.Client, machineSet, func() error {
		machineSet.Spec.Replicas = k8sutilspointer.Int32Ptr(int32(nodePool.Spec.NodeCount))
		return nil
	}); err != nil {
		return ctrl.Result{}, err
	}

	nodePool.Status.NodeCount = int(machineSet.Status.AvailableReplicas)
	if nodePool.Status.NodeCount != nodePool.Spec.NodeCount {
		log.Info("Requeueing nodePool", "expected available nodes", nodePool.Spec.NodeCount, "current available nodes", nodePool.Status.NodeCount)
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

// GetClusterByName finds and return an OpenshiftCluster object using the specified params.
func GetOClusterByName(ctx context.Context, c client.Client, namespace, name string) (*hyperv1.OpenShiftCluster, error) {
	ocluster := &hyperv1.OpenShiftCluster{}
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}

	if err := c.Get(ctx, key, ocluster); err != nil {
		return nil, err
	}

	return ocluster, nil
}

func generateScalableResources(client ctrlclient.Client, ctx context.Context, infraName, region string, nodePool *hyperv1.NodePool) (*capiv1.MachineSet, *capiaws.AWSMachineTemplate, error) {
	// find AMI
	machineSets := &unstructured.UnstructuredList{}
	machineSets.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "machine.openshift.io",
		Version: "v1beta1",
		Kind:    "MachineSet",
	})
	if err := client.List(ctx, machineSets, ctrlclient.InNamespace("openshift-machine-api")); err != nil {
		return nil, nil, fmt.Errorf("failed to list machinesets: %w", err)
	}
	if len(machineSets.Items) == 0 {
		return nil, nil, fmt.Errorf("no machinesets found")
	}
	obj := machineSets.Items[0]
	object := obj.Object

	AMI, found, err := unstructured.NestedString(object, "spec", "template", "spec", "providerSpec", "value", "ami", "id")
	if err != nil || !found {
		return nil, nil, fmt.Errorf("error finding AMI. Found: %v. Error: %v", found, err)
	}

	subnet := &capiaws.AWSResourceReference{}
	if nodePool.Spec.Platform.AWS.Subnet != nil {
		subnet.ID = nodePool.Spec.Platform.AWS.Subnet.ID
		subnet.ARN = nodePool.Spec.Platform.AWS.Subnet.ARN
		for k := range nodePool.Spec.Platform.AWS.Subnet.Filters {
			filter := capiaws.Filter{
				Name:   nodePool.Spec.Platform.AWS.Subnet.Filters[k].Name,
				Values: nodePool.Spec.Platform.AWS.Subnet.Filters[k].Values,
			}
			subnet.Filters = append(subnet.Filters, filter)
		}
	} else {
		// TODO (alberto): remove hardcoded "a" zone and come up with a solution
		// for automation across az
		// e.g have a "locations" field in the nodeGroup or expose the subnet in the nodeGroup
		subnet = &capiaws.AWSResourceReference{
			Filters: []capiaws.Filter{
				{
					Name: "tag:Name",
					Values: []string{
						fmt.Sprintf("%s-private-%sa", infraName, region),
					},
				},
			},
		}
	}

	instanceProfile := fmt.Sprintf("%s-worker-profile", infraName)
	if nodePool.Spec.Platform.AWS.InstanceProfile != "" {
		instanceProfile = nodePool.Spec.Platform.AWS.InstanceProfile
	}

	instanceType := nodePool.Spec.Platform.AWS.InstanceType
	resourcesName := generateMachineSetName(infraName, nodePool.Spec.ClusterName, nodePool.GetName())
	dataSecretName := fmt.Sprintf("%s-user-data", nodePool.Spec.ClusterName)

	AWSMachineTemplate := &capiaws.AWSMachineTemplate{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourcesName,
			Namespace: nodePool.GetNamespace(),
		},
		Spec: capiaws.AWSMachineTemplateSpec{
			Template: capiaws.AWSMachineTemplateResource{
				Spec: capiaws.AWSMachineSpec{
					UncompressedUserData: k8sutilspointer.BoolPtr(true),
					CloudInit: capiaws.CloudInit{
						InsecureSkipSecretsManager: true,
						SecureSecretsBackend:       "secrets-manager",
					},
					IAMInstanceProfile: instanceProfile,
					InstanceType:       instanceType,
					AMI: capiaws.AWSResourceReference{
						ID: k8sutilspointer.StringPtr(AMI),
					},
					Subnet: subnet,
				},
			},
		},
	}

	machineSet := &capiv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourcesName,
			Namespace: nodePool.GetNamespace(),
			// TODO (alberto): drop/expose this annotation at the nodePool API
			Annotations: map[string]string{
				"machine.cluster.x-k8s.io/exclude-node-draining": "true",
			},
			Labels: map[string]string{
				capiv1.ClusterLabelName: infraName,
			},
			// TODO (alberto): pass autoscaler min/max annotations from nodePool API
		},
		TypeMeta: metav1.TypeMeta{},
		Spec: capiv1.MachineSetSpec{
			ClusterName: nodePool.Spec.ClusterName,
			Replicas:    k8sutilspointer.Int32Ptr(int32(nodePool.Spec.NodeCount)),
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					resourcesName: resourcesName,
				},
			},
			Template: capiv1.MachineTemplateSpec{
				ObjectMeta: capiv1.ObjectMeta{
					Labels: map[string]string{
						resourcesName:           resourcesName,
						capiv1.ClusterLabelName: infraName,
					},
				},
				Spec: capiv1.MachineSpec{
					Bootstrap: capiv1.Bootstrap{
						DataSecretName: &dataSecretName,
					},
					ClusterName: nodePool.Spec.ClusterName,
					InfrastructureRef: corev1.ObjectReference{
						Namespace:  nodePool.GetNamespace(),
						Name:       resourcesName,
						APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha3",
						Kind:       "AWSMachineTemplate",
					},
				},
			},
		},
	}
	if err := ctrl.SetControllerReference(nodePool, machineSet, client.Scheme()); err != nil {
		return nil, nil, err
	}
	return machineSet, AWSMachineTemplate, nil
}
