/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package management

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	hypv1alpha1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/infra/aws"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	"github.com/openshift/hypershift/cmd/util"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// PlatformConfigurationReconciler reconciles a PlatformConfiguration object
type PlatformConfigurationReconciler struct {
	client.Client
	Log logr.Logger
	ctx context.Context
}

const (
	destroyFinalizer       = "hypershift.openshift.io/finalizer"
	HostedClusterFinalizer = "hypershift.openshift.io/used-by-hostedcluster"
	oidcStorageProvider    = "oidc-storage-provider-s3-config"
	oidcSPNamespace        = "kube-public"
	AutoInfraLabelName     = "hypershift.openshift.io/auto-created-for-infra"
	InfraLabelName         = "hypershift.openshift.io/infra-id"
)

// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=PlatformConfiguration,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=PlatformConfiguration/status,verbs=get;update;patch

func (r *PlatformConfigurationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log, _ = logr.FromContext(ctx)
	r.ctx = ctx
	log := r.Log

	// your logic here
	var pp hypv1alpha1.PlatformConfiguration

	err := r.Client.Get(ctx, req.NamespacedName, &pp)
	if err != nil {
		log.Info("PlatformConfiguration resource has been deleted " + req.NamespacedName.Name)
		return ctrl.Result{}, nil
	}

	if pp.Spec.InfraID == "" {
		pp.Spec.InfraID = fmt.Sprintf("%s-%s", pp.GetName(), utilrand.String(5))

		controllerutil.AddFinalizer(&pp, destroyFinalizer)
		metav1.SetMetaDataLabel(&pp.ObjectMeta, InfraLabelName, pp.Spec.InfraID)

		if err := r.updatePlatformConfigurationResource(&pp); err != nil || pp.Spec.InfraID == "" {
			return ctrl.Result{}, fmt.Errorf("failed to update infra-id: %w", err)
		}

		//Update the status.conditions. This only works the first time, so if you fix an issue, it will still be set to PlatformXXXMisConfigured
		setStatusCondition(&pp, hyperv1.PlatformConfigured, metav1.ConditionFalse, "Configuring platform with infra-id: "+pp.Spec.InfraID, hyperv1.PlatformBeingConfigured)
		setStatusCondition(&pp, hyperv1.PlatformIAMConfigured, metav1.ConditionFalse, "Configuring platform IAM with infra-id: "+pp.Spec.InfraID, hyperv1.PlatformIAMBeingConfigured)

		if err := r.Client.Status().Update(ctx, &pp); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
		}
	}

	var providerSecret v1.Secret
	var pullSecret v1.Secret

	err = r.Client.Get(ctx, types.NamespacedName{Namespace: pp.Namespace, Name: pp.Spec.Platform.AWS.ControlPlaneOperatorCreds.Name}, &providerSecret)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.Client.Get(ctx, types.NamespacedName{Namespace: pp.Namespace, Name: pp.Spec.PullSecret.Name}, &pullSecret)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Destroying Platform infrastructure used by the PlatformConfiguration scheduled for deletion
	if pp.DeletionTimestamp != nil && !controllerutil.ContainsFinalizer(&pp, HostedClusterFinalizer) {
		return r.destroyPlatformConfiguration(&pp, &providerSecret)
	}

	// Skip reconcile based on condition
	if !meta.IsStatusConditionTrue(pp.Status.Conditions, string(hyperv1.PlatformConfigured)) {
		// Creating Platform infrastructure used by the PlatformConfiguration NodePools and ingress
		o := awsinfra.CreateInfraOptions{
			AWSKey:       string(providerSecret.Data["aws_access_key_id"]),
			AWSSecretKey: string(providerSecret.Data["aws_secret_access_key"]),
			Region:       pp.Spec.Platform.AWS.Region,
			InfraID:      pp.Spec.InfraID,
			Name:         pp.GetName(),
			BaseDomain:   pp.Spec.DNS.BaseDomain,
		}

		result, err := o.CreateInfra(ctx)
		if err != nil {
			log.Error(err, "Could not create infrastructure")

			return ctrl.Result{RequeueAfter: 1 * time.Minute, Requeue: true},
				r.updateStatusConditionsOnChange(
					&pp, hyperv1.PlatformConfigured,
					metav1.ConditionFalse,
					err.Error(),
					hyperv1.PlatformMisConfiguredReason)
		}

		copyResults(&pp, result)
		log.Info(fmt.Sprintf("\n%s\n", pp))

		if err := r.updatePlatformConfigurationResource(&pp); err != nil {
			return ctrl.Result{}, r.updateStatusConditionsOnChange(&pp, hyperv1.PlatformConfigured, metav1.ConditionFalse, err.Error(), hyperv1.PlatformMisConfiguredReason)
		}
		r.updateStatusConditionsOnChange(&pp, hyperv1.PlatformConfigured, metav1.ConditionTrue, "", hyperv1.PlatformConfiguredAsExpected)
		log.Info("Applied Platform Configuration changes to PlatformConfiguration resource")
	}

	if !meta.IsStatusConditionTrue(pp.Status.Conditions, string(hyperv1.PlatformIAMConfigured)) {
		oidcSPName, oidcSPRegion, iamErr := oidcDiscoveryURL(r, pp.Spec.InfraID)
		if iamErr == nil {
			iamOpt := awsinfra.CreateIAMOptions{
				Region:                          pp.Spec.Platform.AWS.Region,
				AWSKey:                          string(providerSecret.Data["aws_access_key_id"]),
				AWSSecretKey:                    string(providerSecret.Data["aws_secret_access_key"]),
				InfraID:                         pp.Spec.InfraID,
				IssuerURL:                       pp.Spec.IssuerURL,
				AdditionalTags:                  []string{},
				OIDCStorageProviderS3BucketName: oidcSPName,
				OIDCStorageProviderS3Region:     oidcSPRegion,
			}

			var iamInfo *awsinfra.CreateIAMOutput
			iamInfo, iamErr = iamOpt.CreateIAM(ctx, r.Client)
			if iamErr == nil {
				// todo, create more paramaters to cover all resources created by CreateIAM
				pp.Spec.Platform.AWS.Roles = iamInfo.Roles
				pp.Spec.IssuerURL = iamInfo.IssuerURL
				pp.Spec.IAM.InstanceProfile = iamInfo.ProfileName
				pp.Spec.IAM.OIDCProvider = iamInfo.ControlPlaneOperatorRoleARN
				if iamErr = createOIDCSecrets(r, &pp, iamInfo); iamErr == nil {
					if err := r.updatePlatformConfigurationResource(&pp); err != nil {
						return ctrl.Result{}, r.updateStatusConditionsOnChange(&pp, hyperv1.PlatformIAMConfigured, metav1.ConditionFalse, err.Error(), hyperv1.PlatformIAMMisConfiguredReason)
					}
					r.updateStatusConditionsOnChange(&pp, hyperv1.PlatformIAMConfigured, metav1.ConditionTrue, "", hyperv1.PlatformIAMConfiguredAsExpected)
					log.Info("Finished reconciling PlatformConfiguration")
				}
			}
		}
		if iamErr != nil {
			return ctrl.Result{RequeueAfter: 1 * time.Minute, Requeue: true}, r.updateStatusConditionsOnChange(&pp, hyperv1.PlatformIAMConfigured, metav1.ConditionFalse, iamErr.Error(), hyperv1.PlatformIAMMisConfiguredReason)
		}
	}

	return ctrl.Result{}, nil
}

func (r *PlatformConfigurationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hypv1alpha1.PlatformConfiguration{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}

func oidcDiscoveryURL(r *PlatformConfigurationReconciler, infraID string) (string, string, error) {

	cm := &v1.ConfigMap{}
	if err := r.Client.Get(context.Background(), types.NamespacedName{Name: oidcStorageProvider, Namespace: oidcSPNamespace}, cm); err != nil {
		return "", "", err
	}
	return cm.Data["name"], cm.Data["region"], nil
}

func createOIDCSecrets(r *PlatformConfigurationReconciler, pp *hypv1alpha1.PlatformConfiguration, iamInfo *awsinfra.CreateIAMOutput) error {

	buildAWSCreds := func(name, arn string) *corev1.Secret {
		return &corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: corev1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: pp.Namespace,
				Name:      name,
				Labels: map[string]string{
					AutoInfraLabelName: pp.Spec.InfraID,
				},
			},
			Data: map[string][]byte{
				"credentials": []byte(fmt.Sprintf(`[default]
	role_arn = %s
	web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token
	`, arn)),
			},
		}
	}

	secretResource := buildAWSCreds(pp.Name+"-cloud-ctrl-creds", iamInfo.KubeCloudControllerRoleARN)
	pp.Spec.Platform.AWS.KubeCloudControllerCreds = corev1.LocalObjectReference{Name: secretResource.Name}
	if err := r.Create(r.ctx, secretResource); apierrors.IsAlreadyExists(err) {
		if err := r.Update(r.ctx, secretResource); err != nil {
			return err
		}
	}

	secretResource = buildAWSCreds(pp.Name+"-node-mgmt-creds", iamInfo.NodePoolManagementRoleARN)
	pp.Spec.Platform.AWS.NodePoolManagementCreds = corev1.LocalObjectReference{Name: secretResource.Name}
	if err := r.Create(r.ctx, secretResource); apierrors.IsAlreadyExists(err) {
		if err := r.Update(r.ctx, secretResource); err != nil {
			return err
		}

	}
	return nil
}

func destroyOIDCSecrets(r *PlatformConfigurationReconciler, pp *hypv1alpha1.PlatformConfiguration) error {
	//clean up CLI generated secrets
	return r.DeleteAllOf(r.ctx, &v1.Secret{}, client.InNamespace(pp.GetNamespace()), client.MatchingLabels{util.AutoInfraLabelName: pp.Spec.InfraID})

}

func setStatusCondition(pp *hypv1alpha1.PlatformConfiguration, conditionType hyperv1.ConditionType, status metav1.ConditionStatus, message string, reason string) metav1.Condition {
	if pp.Status.Conditions == nil {
		pp.Status.Conditions = []metav1.Condition{}
	}
	condition := metav1.Condition{
		Type:               string(conditionType),
		ObservedGeneration: pp.Generation,
		Status:             status,
		Message:            message,
		Reason:             reason,
	}
	meta.SetStatusCondition(&pp.Status.Conditions, condition)
	return condition
}

func (r *PlatformConfigurationReconciler) updateStatusConditionsOnChange(pp *hypv1alpha1.PlatformConfiguration, conditionType hyperv1.ConditionType, conditionStatus metav1.ConditionStatus, message string, reason string) error {

	var err error = nil
	cc := meta.FindStatusCondition(pp.Status.Conditions, string(conditionType))
	if cc == nil || cc.ObservedGeneration != pp.Generation || cc.Status != conditionStatus {
		setStatusCondition(pp, conditionType, conditionStatus, message, reason)
		err = r.Client.Status().Update(r.ctx, pp)
		if err != nil {
			if apierrors.IsConflict(err) {
				r.Log.Error(err, "Conflict encountered when updating ProviderPlatform.Status")
			} else {
				r.Log.Error(err, "Failed to update PlatformConfiguration.Status")
			}
		}
	}
	return err
}

func (r *PlatformConfigurationReconciler) updatePlatformConfigurationResource(pp *hypv1alpha1.PlatformConfiguration) error {
	err := r.Client.Update(r.ctx, pp)
	if err != nil {
		if apierrors.IsConflict(err) {
			r.Log.Error(err, "Conflict encountered when updating ProviderPlatform")
		} else {
			r.Log.Error(err, "Failed to update PlatformConfiguration resource")
		}
	}
	return err
}

func (r *PlatformConfigurationReconciler) destroyPlatformConfiguration(pp *hypv1alpha1.PlatformConfiguration, providerSecret *corev1.Secret) (ctrl.Result, error) {
	log := r.Log
	ctx := r.ctx

	dOpts := awsinfra.DestroyInfraOptions{
		AWSCredentialsFile: "",
		AWSKey:             string(providerSecret.Data["aws_access_key_id"]),
		AWSSecretKey:       string(providerSecret.Data["aws_secret_access_key"]),
		Region:             pp.Spec.Platform.AWS.Region,
		BaseDomain:         pp.Spec.DNS.BaseDomain,
		InfraID:            pp.Spec.InfraID,
		Name:               pp.GetName(),
	}

	setStatusCondition(pp, hyperv1.PlatformConfigured, metav1.ConditionFalse, "Destroying PlatformConfiguration with infra-id: "+pp.Spec.InfraID, hyperv1.PlatfromDestroy)
	setStatusCondition(pp, hyperv1.PlatformIAMConfigured, metav1.ConditionFalse, "Removing PlatformConfiguration IAM with infra-id: "+pp.Spec.InfraID, hyperv1.PlatformIAMRemove)

	if err := r.Client.Status().Update(ctx, pp); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	if err := dOpts.DestroyInfra(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to destroy PlatformConfiguration: %w", err)
	}

	iamOpt := awsinfra.DestroyIAMOptions{
		Region:       pp.Spec.Platform.AWS.Region,
		AWSKey:       dOpts.AWSKey,
		AWSSecretKey: dOpts.AWSSecretKey,
		InfraID:      dOpts.InfraID,
	}

	if err := iamOpt.DestroyIAM(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to delete IAM PlatformConfiguration: %w", err)
	}

	if err := destroyOIDCSecrets(r, pp); err != nil {
		log.Error(err, "Encountered an issue while deleting secrets")
	}

	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: pp.Namespace, Name: pp.Name}, pp); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update PlatformConfiguration values when removing finalizer: %w", err)
	}

	controllerutil.RemoveFinalizer(pp, destroyFinalizer)

	if err := r.Client.Update(ctx, pp); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer, update status: %w", err)
	}
	return ctrl.Result{}, nil
}

func copyResults(pp *hypv1alpha1.PlatformConfiguration, result *aws.CreateInfraOutput) {

	// This covers all parameters currently be created
	pp.Spec.DNS.PrivateZoneID = result.PrivateZoneID
	pp.Spec.DNS.PublicZoneID = result.PublicZoneID
	pp.Spec.Networking.MachineCIDR = result.ComputeCIDR

	cpc := pp.Spec.Platform.AWS.CloudProviderConfig
	if cpc == nil {
		cpc = &hypv1alpha1.AWSCloudProviderConfig{}
	}
	cpc.DHCPOptionsSet = result.DHCPOptionsSet
	cpc.InternetGateway = result.IGWID
	cpc.NATGateway = result.NatGatewayID
	cpc.NatGatewayEIP = result.NatGatewayEIP
	cpc.PrivateRouteTable = result.PrivateRouteTable
	cpc.PulbicRouteTable = result.PublicRouteTable
	cpc.VPCS3Endpoint = result.VPCS3Endpoint
	if cpc.Subnet == nil {
		cpc.Subnet = &hypv1alpha1.AWSResourceReference{}
	}
	cpc.Subnet.ID = &result.PrivateSubnetID
	if cpc.PublicSubnet == nil {
		cpc.PublicSubnet = &hypv1alpha1.AWSResourceReference{}
	}
	cpc.PublicSubnet.ID = &result.PublicSubnetID
	cpc.Zone = result.Zone
	cpc.VPC = result.VPCID
	pp.Spec.IAM.SecurityGroups = []hypv1alpha1.AWSResourceReference{
		hypv1alpha1.AWSResourceReference{ID: &result.SecurityGroupID},
	}
	// Make sure we apply the cpc changes, required if the configuration was just generated
	pp.Spec.Platform.AWS.CloudProviderConfig = cpc
}
