package omoperator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperclient "github.com/openshift/hypershift/client/clientset/clientset"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/util"
	"github.com/openshift/library-go/pkg/manifestclient"
	"github.com/openshift/multi-operator-manager/pkg/applyconfiguration"
	"github.com/openshift/multi-operator-manager/pkg/library/libraryapplyconfiguration"
	"github.com/openshift/multi-operator-manager/pkg/library/libraryinputresources"
	"github.com/openshift/multi-operator-manager/pkg/library/libraryoutputresources"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/yaml"
)

var (
	coreEventGR = schema.GroupResource{
		Group:    "",
		Resource: "events",
	}
	eventGR = schema.GroupResource{
		Group:    "events.k8s.io",
		Resource: "events",
	}
)

const (
	// TODO: figure out the best naming scheme
	// for cluster-wide resources
	// operator.openshift.io-v1-authentications-cluster
	operatorAuthenticationConfigMapName = "operator.openshift.io-v1-authentications-cluster"
)

func NewCommand() *cobra.Command {
	operator := newOpenshiftManagerOperator()
	cmd := &cobra.Command{
		Use:   "om",
		Short: "Runs the OpenshiftManager Operator",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := operator.Validate(); err != nil {
				return err
			}
			if err := operator.Run(ctrl.SetupSignalHandler()); err != nil {
				ctrl.Log.Error(err, "Error running Openshift Manager Operator")
				os.Exit(1)
			}
			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&operator.Namespace, "namespace", operator.Namespace, "the namespace for control plane components on management cluster.")
	flags.StringVar(&operator.HostedControlPlaneName, "hosted-control-plane", operator.HostedControlPlaneName, "Name of the hosted control plane that owns this operator.")
	flags.StringVar(&operator.InputDirectory, "input-dir", operator.InputDirectory, "The directory where the input resources are stored.")
	flags.StringVar(&operator.OutputDirectory, "output-dir", operator.OutputDirectory, "The directory where the output resources are stored.")
	flags.StringVar(&operator.ManagementClusterKubeconfigPath, "management-cluster-kubeconfig", operator.ManagementClusterKubeconfigPath, "path to kubeconfig file for the management cluster.")
	flags.StringVar(&operator.GuestClusterKubeconfigPath, "guest-cluster-kubeconfig", operator.GuestClusterKubeconfigPath, "path to kubeconfig file for the guest cluster.")

	return cmd
}

func newOpenshiftManagerOperator() *OpenshiftManagerOperator {
	return &OpenshiftManagerOperator{}
}

type OpenshiftManagerOperator struct {
	// Namespace is the namespace on the management cluster where the control plane components run.
	Namespace string

	// HostedControlPlaneName is the name of the hosted control plane that owns this operator instance.
	HostedControlPlaneName string

	// InputDirectory the directory where the input resources are stored
	InputDirectory string

	// OutputDirectory the directory where the output resources are stored
	OutputDirectory string

	// ManagementClusterKubeconfigPath path to kubeconfig for the management cluster
	ManagementClusterKubeconfigPath string

	// GuestClusterKubeconfigPath path to kubeconfig for the guest cluster
	GuestClusterKubeconfigPath string
}

func (o *OpenshiftManagerOperator) Validate() error {
	if len(o.Namespace) == 0 {
		return fmt.Errorf("the namespace for control plane components is required")
	}
	if len(o.HostedControlPlaneName) == 0 {
		return fmt.Errorf("the hosted control plane components is required")
	}
	if len(o.InputDirectory) == 0 {
		return fmt.Errorf("the input directory is required")
	}
	if len(o.OutputDirectory) == 0 {
		return fmt.Errorf("the output directory is required")
	}
	if len(o.ManagementClusterKubeconfigPath) == 0 {
		return fmt.Errorf("the management cluster kubeconfig is required")
	}
	if len(o.GuestClusterKubeconfigPath) == 0 {
		return fmt.Errorf("the guest cluster kubeconfig is required")
	}
	return nil
}

func (o *OpenshiftManagerOperator) Run(ctx context.Context) error {
	ctrl.SetLogger(zap.New(zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))
	ctrl.Log.Info("Starting Openshift Manager Operator")

	mgmtCfg := operator.CfgFromFile(o.ManagementClusterKubeconfigPath)
	mgmtHcpClient, err := hyperclient.NewForConfig(mgmtCfg)
	if err != nil {
		return err
	}
	// HCP uses controller-runtime but for experimentation
	// we are going to use the dynamic client
	//
	// TODO: use controller-runtime
	mgmtKubeClient, err := dynamic.NewForConfig(mgmtCfg)
	if err != nil {
		return nil
	}

	guestClusterCfg := operator.CfgFromFile(o.GuestClusterKubeconfigPath)
	guestClusterKubeClient, err := dynamic.NewForConfig(guestClusterCfg)
	if err != nil {
		return err
	}

	if err := o.bootstrap(ctx, mgmtKubeClient, guestClusterKubeClient); err != nil {
		return err
	}

	return o.runInternal(ctx, mgmtKubeClient, guestClusterKubeClient, mgmtHcpClient)
}

func (o *OpenshiftManagerOperator) bootstrap(ctx context.Context, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient) error {
	return o.bootstrapOperatorAuthenticationsResource(ctx, mgmtKubeClient, guestClusterKubeClient)
}

// so, the operator authentication resource is cluster-wide, and we cannot assume that the management cluster will have its definition.
// tt seems we also need a real server to handle at least validation and SSA for this resource.
// additionally, this resource must be stored on the management cluster otherwise, the operator will not be able to function without the guest cluster.
//
// for the POC, we are going to store the resource on the guest cluster and wrap it in a ConfigMap that is stored on the management cluster.
//
// Note:
// the guest cluster didn't have the definition, so I had to create it manually.
// TODO: do ^ automatically
func (o *OpenshiftManagerOperator) bootstrapOperatorAuthenticationsResource(ctx context.Context, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient) error {
	gvr := schema.GroupVersionResource{Group: operatorv1.SchemeGroupVersion.Group, Version: operatorv1.SchemeGroupVersion.Version, Resource: "authentications"}
	unstructuredAuthOperator, err := guestClusterKubeClient.Resource(gvr).Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		authOperator := &operatorv1.Authentication{
			TypeMeta:   metav1.TypeMeta{Kind: "Authentication", APIVersion: gvr.GroupVersion().String()},
			ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
			Spec: operatorv1.AuthenticationSpec{
				OperatorSpec: operatorv1.OperatorSpec{
					ManagementState: operatorv1.Managed,
				},
			},
		}
		rawAuthOperator, err := runtime.DefaultUnstructuredConverter.ToUnstructured(authOperator)
		if err != nil {
			return err
		}
		unstructuredAuthOperator, err = guestClusterKubeClient.Resource(gvr).Create(ctx, &unstructured.Unstructured{Object: rawAuthOperator}, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	}

	// preserve the content of the openshift authentications
	// in a namespaced configmap
	gvr = corev1.SchemeGroupVersion.WithResource("configmaps")
	_, err = mgmtKubeClient.Resource(gvr).Namespace(o.Namespace).Get(ctx, operatorAuthenticationConfigMapName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		authOperatorYaml, err := serializeUnstructuredObjToYAML(unstructuredAuthOperator)
		if err != nil {
			return err
		}
		authOperatorConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: o.Namespace, Name: operatorAuthenticationConfigMapName},
			Data:       map[string]string{},
		}
		authOperatorConfigMap.Data["cluster.yaml"] = authOperatorYaml

		rawAuthOperatorConfigMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(authOperatorConfigMap)
		if err != nil {
			return err
		}

		_, err = mgmtKubeClient.Resource(gvr).Namespace(o.Namespace).Create(ctx, &unstructured.Unstructured{Object: rawAuthOperatorConfigMap}, metav1.CreateOptions{})
		return err
	}
	return nil
}

func (o *OpenshiftManagerOperator) runInternal(ctx context.Context, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient, mgmtHcpClient *hyperclient.Clientset) error {
	actualResources, err := o.getRequiredInputResourcesFromCluster(ctx, mgmtKubeClient, guestClusterKubeClient, mgmtHcpClient, getStaticInputResourcesForAuthOperator())
	if err != nil {
		return err
	}
	if err = o.writeRequiredInputResources(actualResources, o.InputDirectory); err != nil {
		return err
	}
	outputResourcesGetter, err := o.execAuthOperatorApplyConfiguration(ctx)
	if err != nil {
		return err
	}
	return o.applyOutputResources(ctx, mgmtKubeClient, guestClusterKubeClient, outputResourcesGetter)
}

func (o *OpenshiftManagerOperator) getRequiredInputResourcesFromCluster(ctx context.Context, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient, mgmtHcpClient *hyperclient.Clientset, requiredInputResources *libraryinputresources.InputResources) ([]*libraryinputresources.Resource, error) {
	ret, err := o.getRequiredInputResourcesForResourceList(ctx, mgmtKubeClient, guestClusterKubeClient, mgmtHcpClient, requiredInputResources.ApplyConfigurationResources)
	if err != nil {
		return nil, err
	}

	return unstructuredToMustGatherFormat(ret)
}

// for POC we read data directly from the cluster, normally we would use the cache
// TODO: read resources from the cache
func (o *OpenshiftManagerOperator) getRequiredInputResourcesForResourceList(ctx context.Context, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient, mgmtHcpClient *hyperclient.Clientset, resourceList libraryinputresources.ResourceList) ([]*libraryinputresources.Resource, error) {
	hostedControlPlane, err := mgmtHcpClient.HypershiftV1beta1().HostedControlPlanes(o.Namespace).Get(ctx, o.HostedControlPlaneName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	ret := libraryinputresources.NewUniqueResourceSet()
	errs := []error{}

	handleResourceInstanceAndErrorFn := func(resourceInstance *libraryinputresources.Resource, err error) {
		if apierrors.IsNotFound(err) {
			return
		}
		if err != nil {
			errs = append(errs, err)
			return
		}
		ret.Insert(resourceInstance)
	}

	// for the POC, we only need to take into account the ExactResources
	// TODO:add support for other types
	for _, currResource := range resourceList.ExactResources {
		switch currResource {
		// operator.openshift.io
		case libraryinputresources.ExactLowLevelOperator("authentications"):
			handleResourceInstanceAndErrorFn(projectOperatorAuthentication(ctx, mgmtKubeClient, o.Namespace))
		// config.openshift.io
		case libraryinputresources.ExactConfigResource("apiservers"):
			handleResourceInstanceAndErrorFn(projectApiserverConfig(hostedControlPlane))
		case libraryinputresources.ExactConfigResource("authentications"):
			handleResourceInstanceAndErrorFn(projectAuthenticationConfig(hostedControlPlane))
		case libraryinputresources.ExactConfigResource("infrastructures"):
			handleResourceInstanceAndErrorFn(projectInfrastructureConfig(hostedControlPlane))
		case libraryinputresources.ExactConfigResource("oauths"):
			handleResourceInstanceAndErrorFn(projectOAuthConfig(hostedControlPlane))
		case libraryinputresources.ExactResource("config.openshift.io", "v1", "clusterversions", "", "version"):
			handleResourceInstanceAndErrorFn(projectClusterVersionConfig(hostedControlPlane))
		// oauth-server
		case libraryinputresources.ExactResource("route.openshift.io", "v1", "routes", "openshift-authentication", "oauth-openshift"):
			handleResourceInstanceAndErrorFn(getOauthOpenshiftRoute(ctx, mgmtKubeClient, hostedControlPlane))
		case libraryinputresources.ExactResource("", "v1", "services", "openshift-authentication", "oauth-openshift"):
			handleResourceInstanceAndErrorFn(getOauthOpenshiftService(ctx, mgmtKubeClient, hostedControlPlane))
		case libraryinputresources.ExactSecret("openshift-authentication", "v4-0-config-system-router-certs"):
			handleResourceInstanceAndErrorFn(projectConfigSystemRouterCerts())
		case libraryinputresources.ExactConfigMap("openshift-authentication", "v4-0-config-system-cliconfig"):
			handleResourceInstanceAndErrorFn(getOauthConfigSystemCliconfigConfigMap(ctx, mgmtKubeClient, o.Namespace))
		case libraryinputresources.ExactSecret("openshift-authentication", "v4-0-config-system-session"):
			handleResourceInstanceAndErrorFn(getOauthConfigSystemSessionSecret(ctx, mgmtKubeClient, o.Namespace))
		case libraryinputresources.ExactSecret("openshift-authentication", "v4-0-config-system-serving-cert"):
			handleResourceInstanceAndErrorFn(getOauthConfigSystemServingCert(ctx, mgmtKubeClient, o.Namespace))
		case libraryinputresources.ExactConfigMap("openshift-authentication", "v4-0-config-system-service-ca"):
			handleResourceInstanceAndErrorFn(getOauthConfigSystemServiceCA(ctx, mgmtKubeClient, o.Namespace))
		default:
			errs = append(errs, fmt.Errorf("unsupported resource %s", currResource))
		}
	}

	for _, currResource := range resourceList.GeneratedNameResources {
		ctrl.Log.Info("WARNING: skipping reconciling GeneratedNameResources", "generatedResourceID", currResource)
	}
	for _, currResource := range resourceList.LabelSelectedResources {
		ctrl.Log.Info("WARNING: skipping reconciling LabelSelectedResources", "labelSelectedResource", currResource)
	}

	return ret.List(), errors.Join(errs...)
}

func (o *OpenshiftManagerOperator) execAuthOperatorApplyConfiguration(ctx context.Context) (libraryapplyconfiguration.AllDesiredMutationsGetter, error) {
	res, err := applyconfiguration.ExecApplyConfiguration(
		ctx,
		"/Users/lszaszki/go/src/github.com/openshift/cluster-authentication-operator/authentication-operator",
		applyconfiguration.ApplyConfigurationFlagValues{
			InputDirectory:  o.InputDirectory,
			OutputDirectory: o.OutputDirectory,
			Now:             time.Time{},
			Controllers:     []string{"TODO-configObserver", "TODO-payloadConfigController", "TODO-deploymentController"},
		},
	)
	if err != nil {
		if res == nil {
			ctrl.Log.Error(err, "Failed executing apply-configuration for the Auth Operator. No results from apply-configuration.")
			return nil, err
		}

		ctrl.Log.Error(err, "Failed executing apply-configuration for the Auth Operator. STDERR:\n%s\n\nSTDOUT:\n%s\n", res.Stderr, res.Stdout)
		return nil, err
	}
	return res, nil
}

func (o *OpenshiftManagerOperator) applyOutputResources(ctx context.Context, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient, outputResourcesGetter libraryapplyconfiguration.AllDesiredMutationsGetter) error {
	for _, clusterType := range sets.List(libraryapplyconfiguration.AllClusterTypes) {
		if clusterType == libraryapplyconfiguration.ClusterTypeUserWorkload {
			ctrl.Log.Info("WARNING: skipping applying actions on an unsupported (not implemented) cluster", "type", clusterType)
			continue
		}
		// TODO: skip events ?
		// TODO: validate the output (more res than defined) ?
		if err := o.applyOutputResourcesOnManagementCluster(ctx, clusterType, mgmtKubeClient, guestClusterKubeClient, outputResourcesGetter.MutationsForClusterType(clusterType)); err != nil {
			return err
		}
	}

	return nil
}

func (o *OpenshiftManagerOperator) applyOutputResourcesOnManagementCluster(ctx context.Context, clusterType libraryapplyconfiguration.ClusterType, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient, resourcesToApply libraryapplyconfiguration.SingleClusterDesiredMutationGetter) error {
	ctrl.Log.Info("applying the output-resources on the management cluster", "type", clusterType)
	defer func() {
		ctrl.Log.Info("done applying the output-resources on the management cluster", "type", clusterType)
	}()

	for _, actionType := range resourcesToApply.Requests().ListActions() {
		var err error
		switch actionType {
		case manifestclient.ActionCreate:
			err = o.applyCreateOnOutputResourcesOnManagementCluster(ctx, clusterType, actionType, mgmtKubeClient, guestClusterKubeClient, resourcesToApply.Requests().RequestsForAction(actionType))
		case manifestclient.ActionUpdate:
			err = o.applyUpdateOnOutputResourcesOnManagementCluster(ctx, clusterType, actionType, mgmtKubeClient, guestClusterKubeClient, resourcesToApply.Requests().RequestsForAction(actionType))
		case manifestclient.ActionApplyStatus:
			err = o.applyApplyStatusOnOutputResourcesOnManagementCluster(ctx, clusterType, actionType, mgmtKubeClient, guestClusterKubeClient, resourcesToApply.Requests().RequestsForAction(actionType))
		default:
			skippedRequestsNumber := len(resourcesToApply.Requests().RequestsForAction(actionType))
			if skippedRequestsNumber > 0 {
				ctrl.Log.Info("WARNING: skipping applying an unsupported (not implemented) action", "type", actionType, "skippedRequests", skippedRequestsNumber)
			}
		}
		if err != nil {
			return err
		}
	}

	return nil
}

// TODO: consider storing the destination resource
var logActionOnOutputResourceFn = func(clusterType libraryapplyconfiguration.ClusterType, actionType manifestclient.Action, exactResourceID libraryoutputresources.ExactResourceID, err error) error {
	if err != nil {
		ctrl.Log.Error(err, "failed applying the output resource", "clusterType", clusterType, "actionType", actionType, "exactResourceID", exactResourceID)
		return err
	}
	ctrl.Log.Info("successfully applied the output resource", "clusterType", clusterType, "actionType", actionType, "exactResourceID", exactResourceID)
	return nil
}

func (o *OpenshiftManagerOperator) applyUpdateOnOutputResourcesOnManagementCluster(ctx context.Context, clusterType libraryapplyconfiguration.ClusterType, actionType manifestclient.Action, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient, updateRequests []manifestclient.SerializedRequestish) error {
	for _, request := range updateRequests {
		var err error
		currentResourceExactID := serializedRequestActionMetadataToExactResource(request.GetSerializedRequest().ActionMetadata)
		switch currentResourceExactID {
		// operator.openshift.io
		case libraryoutputresources.ExactLowLevelOperator("authentications"):
			err = logActionOnOutputResourceFn(clusterType, actionType, currentResourceExactID, applyActionToOperatorAuthentication(ctx, actionType, mgmtKubeClient, guestClusterKubeClient, request, o.Namespace))
		case libraryoutputresources.ExactConfigMap("openshift-authentication", "v4-0-config-system-cliconfig"):
			err = logActionOnOutputResourceFn(clusterType, actionType, currentResourceExactID, applyUpdateToOauthConfigSystemCliconfigConfigMap(ctx, mgmtKubeClient, request, o.Namespace))
		case libraryoutputresources.ExactSecret("openshift-authentication", "v4-0-config-system-session"):
			err = logActionOnOutputResourceFn(clusterType, actionType, currentResourceExactID, applyUpdateToOauthConfigSystemSessionSecret(ctx, mgmtKubeClient, request, o.Namespace))
		default:
			return fmt.Errorf("unable to apply Update on an unsupported (not impelemented) resource %v", currentResourceExactID)
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func (o *OpenshiftManagerOperator) applyApplyStatusOnOutputResourcesOnManagementCluster(ctx context.Context, clusterType libraryapplyconfiguration.ClusterType, actionType manifestclient.Action, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient, applyStatusRequests []manifestclient.SerializedRequestish) error {
	for _, request := range applyStatusRequests {
		var err error
		currentResourceExactID := serializedRequestActionMetadataToExactResource(request.GetSerializedRequest().ActionMetadata)
		switch currentResourceExactID {
		// operator.openshift.io
		case libraryoutputresources.ExactLowLevelOperator("authentications"):
			err = logActionOnOutputResourceFn(clusterType, actionType, currentResourceExactID, applyActionToOperatorAuthentication(ctx, actionType, mgmtKubeClient, guestClusterKubeClient, request, o.Namespace))
		default:
			return fmt.Errorf("unable to apply ApplyStatus on an unsupported (not impelemented) resource %v", currentResourceExactID)
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func (o *OpenshiftManagerOperator) applyCreateOnOutputResourcesOnManagementCluster(ctx context.Context, clusterType libraryapplyconfiguration.ClusterType, actionType manifestclient.Action, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient, createRequests []manifestclient.SerializedRequestish) error {
	for _, request := range createRequests {
		var err error
		currentResourceExactID := serializedRequestActionMetadataToExactResource(request.GetSerializedRequest().ActionMetadata)
		switch currentResourceExactID {
		case libraryoutputresources.ExactConfigMap("openshift-authentication", "v4-0-config-system-cliconfig"):
			err = logActionOnOutputResourceFn(clusterType, actionType, currentResourceExactID, applyCreateOauthConfigSystemCliconfigConfigMap(ctx, mgmtKubeClient, request, o.Namespace))
		case libraryoutputresources.ExactSecret("openshift-authentication", "v4-0-config-system-session"):
			err = logActionOnOutputResourceFn(clusterType, actionType, currentResourceExactID, applyCreateOauthConfigSystemSessionSecret(ctx, mgmtKubeClient, request, o.Namespace))
		case libraryoutputresources.ExactDeployment("openshift-authentication", "oauth-openshift"):
			err = logActionOnOutputResourceFn(clusterType, actionType, currentResourceExactID, applyCreateOauthDeployment(ctx, mgmtKubeClient, request, o.Namespace))
		default:
			currentResourceGR := schema.GroupResource{Group: currentResourceExactID.Group, Resource: currentResourceExactID.Resource}
			if currentResourceGR == coreEventGR || currentResourceGR == eventGR {
				ctrl.Log.Info("WARNING: skipping Create of an event resource", "clusterType", clusterType, "actionType", actionType, "gr", coreEventGR)
				continue
			}
			return fmt.Errorf("unable to apply Create on an unsupported (not impelemented) resource %v", currentResourceExactID)
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func (o *OpenshiftManagerOperator) writeRequiredInputResources(actualResources []*libraryinputresources.Resource, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("unable to create %q: %w", targetDir, err)
	}

	errs := []error{}
	for _, currResource := range actualResources {
		if err := libraryinputresources.WriteResource(currResource, targetDir); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func projectConfigSystemRouterCerts() (*libraryinputresources.Resource, error) {
	// In OCP openshift-authentication/v4-0-config-system-router-certs
	// holds a custom service certificate which (I think) can be recognised by an ingress
	//
	// I think that HCP doesn't support passing custom certs
	// thus for HCP we create an empty secret
	// TODO: check if the assumption above is true
	//
	// update:
	// found https://github.com/openshift/hypershift/blob/675f881923cfa312115ba9bd572f39c201bbe689/control-plane-operator/controllers/hostedcontrolplane/v2/oauth/config.go#L66
	// which indicates there might be some custom certs
	//
	// this cert holds a serving cert for the route

	gvr := corev1.SchemeGroupVersion.WithResource("secrets")
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: gvr.GroupVersion().String(),
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "v4-0-config-system-router-certs",
			Namespace: "openshift-authentication",
		},
	}

	return runtimeObjectToInputResource(secret, gvr)
}

func getOauthOpenshiftRoute(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, hostedControlPlane *hypershiftv1beta1.HostedControlPlane) (*libraryinputresources.Resource, error) {
	// atm reconciled in reconciled in https://github.com/openshift/hypershift/blob/6b4d6324de66b9aabdbe7be434b28a17c900074b/control-plane-operator/controllers/hostedcontrolplane/hostedcontrolplane_controller.go#L1305
	//
	//
	// route.Spec.Host is used to populate osinv1.OAuthConfig.MasterPublicURL
	// xref: https://github.com/openshift/cluster-authentication-operator/blob/817783a52d042f4ac3aa8faac7421ac013b42481/pkg/controllers/payload/payload_config_controller.go#L178
	//
	// For the POC we wil keep it simple and assume
	// ony one type of route
	//
	// TODO: production code will need cover all cases
	// https://github.com/openshift/hypershift/blob/6b4d6324de66b9aabdbe7be434b28a17c900074b/control-plane-operator/controllers/hostedcontrolplane/hostedcontrolplane_controller.go#L1646
	serviceStrategy := util.ServicePublishingStrategyByTypeForHCP(hostedControlPlane, hyperv1.OAuthServer)
	if serviceStrategy == nil {
		return nil, fmt.Errorf("OAuth strategy not specified")
	}
	if serviceStrategy.Type != hyperv1.Route {
		return nil, fmt.Errorf("unsupported (not implemented) service publishing strategy type: %v", serviceStrategy.Type)
	}
	if !util.IsPublicHCP(hostedControlPlane) {
		return nil, fmt.Errorf("unsupported (not implemented) publishing scope of cluster endpoints for: %s", hostedControlPlane.Name)
	}
	gvr := schema.GroupVersionResource{
		Group:    "route.openshift.io",
		Version:  "v1",
		Resource: "routes",
	}

	// TODO: export the route name
	// xref: https://github.com/openshift/hypershift/blob/8be1d9c6f8f79106444e48f2b7d0069b942ba0d7/control-plane-operator/controllers/hostedcontrolplane/manifests/infra.go#L104
	route, err := mgmtKubeClient.Resource(gvr).Namespace(hostedControlPlane.Namespace).Get(ctx, "oauth", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// we need to change the name and the namespace
	// so that the operator can find the resource
	//
	// TODO: should we record the orig name and namespace ?
	route.SetNamespace("openshift-authentication")
	route.SetName("oauth-openshift")
	return &libraryinputresources.Resource{
		ResourceType: gvr,
		Content:      route,
	}, nil
}

func getOauthOpenshiftService(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, hostedControlPlane *hypershiftv1beta1.HostedControlPlane) (*libraryinputresources.Resource, error) {
	// openshift-authentication/oauth-openshift service
	// is reconciled in https://github.com/openshift/hypershift/blob/6b4d6324de66b9aabdbe7be434b28a17c900074b/control-plane-operator/controllers/hostedcontrolplane/hostedcontrolplane_controller.go#L1305
	//
	// for the POC we simply assume the reconciler runs,
	// and we can read the service manifest

	serviceStrategy := util.ServicePublishingStrategyByTypeForHCP(hostedControlPlane, hyperv1.OAuthServer)
	if serviceStrategy == nil {
		return nil, fmt.Errorf("OAuth strategy not specified")
	}

	gvr := corev1.SchemeGroupVersion.WithResource("services")
	route, err := mgmtKubeClient.Resource(gvr).Namespace(hostedControlPlane.Namespace).Get(ctx, "oauth-openshift", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// we need to change the name and the namespace
	// so that the operator can find the resource
	//
	// TODO: should we record the orig name and namespace ?
	route.SetNamespace("openshift-authentication")
	route.SetName("oauth-openshift")
	return &libraryinputresources.Resource{
		ResourceType: gvr,
		Content:      route,
	}, nil

}

func projectOperatorAuthentication(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, controlPlaneNamespace string) (*libraryinputresources.Resource, error) {
	gvr := corev1.SchemeGroupVersion.WithResource("configmaps")
	authOperatorConfigMap, err := mgmtKubeClient.Resource(gvr).Namespace(controlPlaneNamespace).Get(ctx, operatorAuthenticationConfigMapName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	authOperatorYaml, found, err := unstructured.NestedString(authOperatorConfigMap.Object, "data", "cluster.yaml")
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("missing cluster.yaml field in %s/%s configmap", controlPlaneNamespace, operatorAuthenticationConfigMapName)
	}
	unstructuredAuthOperator, err := decodeIndividualObj([]byte(authOperatorYaml))
	if err != nil {
		return nil, err
	}

	gvr = schema.GroupVersionResource{Group: operatorv1.SchemeGroupVersion.Group, Version: operatorv1.SchemeGroupVersion.Version, Resource: "authentications"}
	ret := &libraryinputresources.Resource{
		ResourceType: gvr,
		Content:      unstructuredAuthOperator,
	}
	return ret, nil
}

// configv1.Authentication resource doesn't exist in HCP
// we need to project it from HostedControlPlane
func projectAuthenticationConfig(hostedControlPlane *hypershiftv1beta1.HostedControlPlane) (*libraryinputresources.Resource, error) {
	cfg := &configv1.Authentication{
		TypeMeta: metav1.TypeMeta{
			APIVersion: configv1.SchemeGroupVersion.String(),
			Kind:       "Authentication",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
	if hostedControlPlane != nil && hostedControlPlane.Spec.Configuration != nil && hostedControlPlane.Spec.Configuration.Authentication != nil {
		cfg.Spec = *hostedControlPlane.Spec.Configuration.Authentication
	}

	return runtimeObjectToInputResource(cfg, configv1.SchemeGroupVersion.WithResource("authentications"))
}

// configv1.ClusterVersion resource doesn't exist in HCP
// we need to project it from HostedControlPlane
func projectClusterVersionConfig(hostedControlPlane *hypershiftv1beta1.HostedControlPlane) (*libraryinputresources.Resource, error) {
	clusterVersion := &configv1.ClusterVersion{
		TypeMeta: metav1.TypeMeta{
			APIVersion: configv1.SchemeGroupVersion.String(),
			Kind:       "ClusterVersion",
		},
		ObjectMeta: metav1.ObjectMeta{Name: "version"},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: configv1.ClusterID(hostedControlPlane.Spec.ClusterID),
			Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
				BaselineCapabilitySet:         configv1.ClusterVersionCapabilitySetNone,
				AdditionalEnabledCapabilities: capabilities.CalculateEnabledCapabilities(hostedControlPlane.Spec.Capabilities),
			},
			Upstream: hostedControlPlane.Spec.UpdateService,
			Channel:  hostedControlPlane.Spec.Channel,
		},
	}

	return runtimeObjectToInputResource(clusterVersion, configv1.SchemeGroupVersion.WithResource("clusterversions"))
}

func projectInfrastructureConfig(hostedControlPlane *hypershiftv1beta1.HostedControlPlane) (*libraryinputresources.Resource, error) {
	infra := globalconfig.InfrastructureConfig()
	infra.TypeMeta = metav1.TypeMeta{
		APIVersion: configv1.SchemeGroupVersion.String(),
		Kind:       "Infrastructure",
	}
	globalconfig.ReconcileInfrastructure(infra, hostedControlPlane)

	return runtimeObjectToInputResource(infra, configv1.SchemeGroupVersion.WithResource("infrastructures"))
}

func projectApiserverConfig(hostedControlPlane *hypershiftv1beta1.HostedControlPlane) (*libraryinputresources.Resource, error) {
	cfg := &configv1.APIServer{
		TypeMeta: metav1.TypeMeta{
			APIVersion: configv1.SchemeGroupVersion.String(),
			Kind:       "APIServer",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}

	if hostedControlPlane != nil && hostedControlPlane.Spec.Configuration != nil && hostedControlPlane.Spec.Configuration.APIServer != nil {
		cfg.Spec = *hostedControlPlane.Spec.Configuration.APIServer
	}

	return runtimeObjectToInputResource(cfg, configv1.SchemeGroupVersion.WithResource("apiservers"))
}

func projectOAuthConfig(hostedControlPlane *hypershiftv1beta1.HostedControlPlane) (*libraryinputresources.Resource, error) {
	cfg := &configv1.OAuth{
		TypeMeta: metav1.TypeMeta{
			APIVersion: configv1.SchemeGroupVersion.String(),
			Kind:       "OAuth",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}

	if hostedControlPlane != nil && hostedControlPlane.Spec.Configuration != nil && hostedControlPlane.Spec.Configuration.OAuth != nil {
		cfg.Spec = *hostedControlPlane.Spec.Configuration.OAuth
	}

	return runtimeObjectToInputResource(cfg, configv1.SchemeGroupVersion.WithResource("oauths"))
}

func runtimeObjectToInputResource(obj runtime.Object, gvr schema.GroupVersionResource) (*libraryinputresources.Resource, error) {
	rawObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}

	ret := &libraryinputresources.Resource{
		ResourceType: gvr,
		Content:      &unstructured.Unstructured{Object: rawObj},
	}
	return ret, nil
}

func serializedRequestActionMetadataToExactResource(actionMetadata manifestclient.ActionMetadata) libraryoutputresources.ExactResourceID {
	return libraryoutputresources.ExactResource(
		actionMetadata.ResourceType.Group,
		actionMetadata.ResourceType.Version,
		actionMetadata.ResourceType.Resource,
		actionMetadata.Namespace,
		actionMetadata.Name,
	)
}

func serializeUnstructuredObjToYAML(obj *unstructured.Unstructured) (string, error) {
	ret, err := yaml.Marshal(obj.Object)
	if err != nil {
		return "", err
	}
	return string(ret), nil
}

// normally, we would get that list by running the input-res command of the auth-operator.
// for now, just return a static list required to run controllers that manage the oauth-server.
//
// TODO: get the list directly from the auth-operator
func getStaticInputResourcesForAuthOperator() *libraryinputresources.InputResources {
	return &libraryinputresources.InputResources{
		ApplyConfigurationResources: libraryinputresources.ResourceList{
			ExactResources: []libraryinputresources.ExactResourceID{
				// operator.openshift.io
				libraryinputresources.ExactLowLevelOperator("authentications"),
				// config.openshift.io
				libraryinputresources.ExactConfigResource("apiservers"),
				libraryinputresources.ExactConfigResource("authentications"),
				libraryinputresources.ExactConfigResource("infrastructures"),
				libraryinputresources.ExactConfigResource("oauths"),
				libraryinputresources.ExactResource("config.openshift.io", "v1", "clusterversions", "", "version"),
				// oauth-openshift
				libraryinputresources.ExactResource("route.openshift.io", "v1", "routes", "openshift-authentication", "oauth-openshift"),
				libraryinputresources.ExactResource("", "v1", "services", "openshift-authentication", "oauth-openshift"),
				libraryinputresources.ExactSecret("openshift-authentication", "v4-0-config-system-router-certs"),
				libraryinputresources.ExactConfigMap("openshift-authentication", "v4-0-config-system-cliconfig"),
				libraryinputresources.ExactSecret("openshift-authentication", "v4-0-config-system-session"),
				libraryinputresources.ExactSecret("openshift-authentication", "v4-0-config-system-serving-cert"),
				libraryinputresources.ExactConfigMap("openshift-authentication", "v4-0-config-system-service-ca"),
			},
		},
	}
}
