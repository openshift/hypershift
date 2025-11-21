package omoperator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"

	operatorv1 "github.com/openshift/api/operator/v1"
	hyperclient "github.com/openshift/hypershift/client/clientset/clientset"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
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
	// operator.openshift.io--authentications--cluster
	operatorAuthenticationConfigMapName = "operator.openshift.io--authentications--cluster"
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

	cmd.AddCommand(NewTransformDeploymentCommand())
	cmd.AddCommand(NewHTTPProxyCommand())
	cmd.AddCommand(NewHTTPProxy2Command())

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

func (o *OpenshiftManagerOperator) bootstrapAuthenticationCRD(ctx context.Context, guestClusterKubeClient *dynamic.DynamicClient) error {
	crdPath := "control-plane-operator/omoperator/manifests/0000_50_authentication_01_authentications.crd.yaml"

	crdBytes, err := os.ReadFile(crdPath)
	if err != nil {
		return fmt.Errorf("failed to read Authentication CRD from %s: %w", crdPath, err)
	}

	var crdObj map[string]interface{}
	if err := yaml.Unmarshal(crdBytes, &crdObj); err != nil {
		return fmt.Errorf("failed to parse Authentication CRD YAML: %w", err)
	}

	crd := &unstructured.Unstructured{Object: crdObj}

	gvr := schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}

	crdName := crd.GetName()
	_, err = guestClusterKubeClient.Resource(gvr).Get(ctx, crdName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to check if Authentication CRD exists: %w", err)
		}

		_, err = guestClusterKubeClient.Resource(gvr).Create(ctx, crd, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create Authentication CRD: %w", err)
		}
		ctrl.Log.Info("Successfully installed Authentication CRD in guest cluster")
	} else {
		ctrl.Log.Info("Authentication CRD already exists in guest cluster, skipping installation")
	}

	return nil
}

func (o *OpenshiftManagerOperator) bootstrap(ctx context.Context, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient) error {
	if err := o.bootstrapAuthenticationCRD(ctx, guestClusterKubeClient); err != nil {
		return err
	}
	return o.bootstrapOperatorAuthenticationClusterResource(ctx, mgmtKubeClient, guestClusterKubeClient)
}

// so, the operator authentication resource is cluster-wide, and we cannot assume that the management cluster will have its definition.
// tt seems we also need a real server to handle at least validation and SSA for this resource.
// additionally, this resource must be stored on the management cluster otherwise, the operator will not be able to function without the guest cluster.
//
// for the POC, we are going to store the resource on the guest cluster and wrap it in a ConfigMap that is stored on the management cluster.
func (o *OpenshiftManagerOperator) bootstrapOperatorAuthenticationClusterResource(ctx context.Context, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient) error {
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
	actualResources, err := o.getAuthOperatorRequiredInputResourcesFromCluster(ctx, mgmtKubeClient, guestClusterKubeClient, mgmtHcpClient, getAuthOperatorStaticInputResources())
	if err != nil {
		return err
	}
	if err = o.writeRequiredInputResources(actualResources, o.InputDirectory); err != nil {
		return err
	}
	outputResourcesGetter, err := o.execAuthOperatorApplyConfigurationCommand(ctx)
	if err != nil {
		return err
	}
	return o.applyAuthOperatorOutputResources(ctx, mgmtKubeClient, guestClusterKubeClient, outputResourcesGetter)
}

func (o *OpenshiftManagerOperator) getAuthOperatorRequiredInputResourcesFromCluster(ctx context.Context, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient, mgmtHcpClient *hyperclient.Clientset, requiredInputResources *libraryinputresources.InputResources) ([]*libraryinputresources.Resource, error) {
	ret, err := o.getAuthOperatorRequiredInputResourcesForResourceList(ctx, mgmtKubeClient, guestClusterKubeClient, mgmtHcpClient, requiredInputResources.ApplyConfigurationResources)
	if err != nil {
		return nil, err
	}

	return unstructuredToMustGatherFormat(ret)
}

// for POC we read data directly from the cluster, normally we would use the cache
// TODO: read resources from the cache
func (o *OpenshiftManagerOperator) getAuthOperatorRequiredInputResourcesForResourceList(ctx context.Context, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient, mgmtHcpClient *hyperclient.Clientset, resourceList libraryinputresources.ResourceList) ([]*libraryinputresources.Resource, error) {
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
			handleResourceInstanceAndErrorFn(projectOperatorAuthenticationCluster(ctx, mgmtKubeClient, o.Namespace))
		// config.openshift.io
		case libraryinputresources.ExactConfigResource("apiservers"):
			handleResourceInstanceAndErrorFn(projectConfigApiserverCluster(hostedControlPlane))
		case libraryinputresources.ExactConfigResource("authentications"):
			handleResourceInstanceAndErrorFn(projectConfigAuthenticationCluster(hostedControlPlane))
		case libraryinputresources.ExactConfigResource("infrastructures"):
			handleResourceInstanceAndErrorFn(projectConfigInfrastructureCluster(hostedControlPlane))
		case libraryinputresources.ExactConfigResource("oauths"):
			handleResourceInstanceAndErrorFn(projectConfigOAuthCluster(hostedControlPlane))
		case libraryinputresources.ExactResource("config.openshift.io", "v1", "clusterversions", "", "version"):
			handleResourceInstanceAndErrorFn(projectConfigClusterVersionCluster(hostedControlPlane))
		// oauth-server
		case libraryinputresources.ExactResource("route.openshift.io", "v1", "routes", "openshift-authentication", "oauth-openshift"):
			handleResourceInstanceAndErrorFn(getRouteOpenshiftAuthenticationOauthOpenshift(ctx, mgmtKubeClient, hostedControlPlane))
		case libraryinputresources.ExactResource("", "v1", "services", "openshift-authentication", "oauth-openshift"):
			handleResourceInstanceAndErrorFn(getServiceOpenshiftAuthenticationOauthOpenshift(ctx, mgmtKubeClient, hostedControlPlane))
		case libraryinputresources.ExactSecret("openshift-authentication", "v4-0-config-system-router-certs"):
			handleResourceInstanceAndErrorFn(projectSecretOpenshiftAuthenticationConfigSystemRouterCerts(ctx, mgmtKubeClient, o.Namespace, hostedControlPlane))
		case libraryinputresources.ExactConfigMap("openshift-authentication", "v4-0-config-system-cliconfig"):
			handleResourceInstanceAndErrorFn(getConfigMapOpenshiftAuthenticationConfigSystemCliconfig(ctx, mgmtKubeClient, o.Namespace))
		case libraryinputresources.ExactSecret("openshift-authentication", "v4-0-config-system-session"):
			handleResourceInstanceAndErrorFn(getSecretOpenshiftAuthenticationConfigSystemSession(ctx, mgmtKubeClient, o.Namespace))
		case libraryinputresources.ExactSecret("openshift-authentication", "v4-0-config-system-serving-cert"):
			handleResourceInstanceAndErrorFn(getSecretOpenshiftAuthenticationConfigSystemServingCert(ctx, mgmtKubeClient, o.Namespace))
		case libraryinputresources.ExactConfigMap("openshift-authentication", "v4-0-config-system-service-ca"):
			handleResourceInstanceAndErrorFn(getConfigMapOpenshiftAuthenticationConfigSystemServiceCA(ctx, mgmtKubeClient, o.Namespace))
		case libraryinputresources.ExactSecret("openshift-authentication", "v4-0-config-system-ocp-branding-template"):
			handleResourceInstanceAndErrorFn(getSecretOpenshiftAuthenticationConfigSystemOCPBrandingTemplate(ctx, mgmtKubeClient, o.Namespace))
		case libraryinputresources.ExactConfigMap("openshift-authentication", "audit"):
			handleResourceInstanceAndErrorFn(getConfigMapOpenshiftAuthenticationAudit(ctx, mgmtKubeClient, o.Namespace))
		default:
			errs = append(errs, fmt.Errorf("unable to GET an unknown (not implemented ?) input resource %s", currResource))
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

func (o *OpenshiftManagerOperator) execAuthOperatorApplyConfigurationCommand(ctx context.Context) (libraryapplyconfiguration.AllDesiredMutationsGetter, error) {
	res, err := applyconfiguration.ExecApplyConfiguration(
		ctx,
		"authentication-operator",
		applyconfiguration.ApplyConfigurationOptions{
			InputDirectory:  o.InputDirectory,
			OutputDirectory: o.OutputDirectory,
			Now:             time.Time{},
			Controllers: []string{
				"TODO-configObserver",
				"TODO-payloadConfigController",
				"TODO-deploymentController",
				"TODO-staticResourceController",
				// TODO: run TODO-customRouteController for openshift-authentication/oauth-openshift
			},

			// TODO: figure out how to pass the oauth-server img
			Env: []string{"IMAGE_OAUTH_SERVER=quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:9284d17da287a1b30e75394e6f62259c3a6944a4ecc7313c6220ea80da10d7e0"},
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

func (o *OpenshiftManagerOperator) applyAuthOperatorOutputResources(ctx context.Context, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient, outputResourcesGetter libraryapplyconfiguration.AllDesiredMutationsGetter) error {
	for _, clusterType := range sets.List(libraryapplyconfiguration.AllClusterTypes) {
		if clusterType == libraryapplyconfiguration.ClusterTypeUserWorkload {
			ctrl.Log.Info("WARNING: skipping applying actions on an unsupported (not implemented) cluster", "type", clusterType)
			continue
		}
		// TODO: skip events ?
		// TODO: validate the output (more res than defined) ?
		if err := o.applyAuthOperatorOutputResourcesOnManagementCluster(ctx, clusterType, mgmtKubeClient, guestClusterKubeClient, outputResourcesGetter.MutationsForClusterType(clusterType)); err != nil {
			return err
		}
	}

	return nil
}

func (o *OpenshiftManagerOperator) applyAuthOperatorOutputResourcesOnManagementCluster(ctx context.Context, clusterType libraryapplyconfiguration.ClusterType, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient, resourcesToApply libraryapplyconfiguration.SingleClusterDesiredMutationGetter) error {
	ctrl.Log.Info("applying auth operator's output resources on the cluster", "type", clusterType)
	defer func() {
		ctrl.Log.Info("done applying auth operator's output resources on the cluster", "type", clusterType)
	}()

	for _, actionType := range resourcesToApply.Requests().ListActions() {
		var err error
		switch actionType {
		case manifestclient.ActionCreate:
			err = o.applyAuthOperatorCreateOnOutputResourcesOnManagementCluster(ctx, clusterType, actionType, mgmtKubeClient, guestClusterKubeClient, resourcesToApply.Requests().RequestsForAction(actionType))
		case manifestclient.ActionUpdate:
			err = o.applyAuthOperatorUpdateOnOutputResourcesOnManagementCluster(ctx, clusterType, actionType, mgmtKubeClient, guestClusterKubeClient, resourcesToApply.Requests().RequestsForAction(actionType))
		case manifestclient.ActionApplyStatus:
			err = o.applyAuthOperatorApplyStatusOnOutputResourcesOnManagementCluster(ctx, clusterType, actionType, mgmtKubeClient, guestClusterKubeClient, resourcesToApply.Requests().RequestsForAction(actionType))
		default:
			skippedRequestsNumber := len(resourcesToApply.Requests().RequestsForAction(actionType))
			if skippedRequestsNumber > 0 {
				ctrl.Log.Info("WARNING: skipping applying an unsupported (not implemented) action", "actionType", actionType, "skippedRequests", skippedRequestsNumber, "clusterType", clusterType)
			}
		}
		if err != nil {
			return err
		}
	}

	return nil
}

// TODO: consider storing the destination resource
var logCompletedActionOnOutputResourceFn = func(clusterType libraryapplyconfiguration.ClusterType, actionType manifestclient.Action, exactResourceID libraryoutputresources.ExactResourceID, err error) error {
	if err != nil {
		ctrl.Log.Error(err, "failed applying auth operator's output resource", "clusterType", clusterType, "actionType", actionType, "exactResourceID", exactResourceID)
		return err
	}
	ctrl.Log.Info("successfully applied auth operator's output resource", "clusterType", clusterType, "actionType", actionType, "exactResourceID", exactResourceID)
	return nil
}

func (o *OpenshiftManagerOperator) applyAuthOperatorUpdateOnOutputResourcesOnManagementCluster(ctx context.Context, clusterType libraryapplyconfiguration.ClusterType, actionType manifestclient.Action, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient, updateRequests []manifestclient.SerializedRequestish) error {
	for _, request := range updateRequests {
		var err error
		currentResourceExactID := serializedRequestActionMetadataToExactResource(request.GetSerializedRequest().ActionMetadata)
		switch currentResourceExactID {
		// operator.openshift.io
		case libraryoutputresources.ExactLowLevelOperator("authentications"):
			err = logCompletedActionOnOutputResourceFn(clusterType, actionType, currentResourceExactID, applyActionOperatorAuthenticationCluster(ctx, actionType, mgmtKubeClient, guestClusterKubeClient, request, o.Namespace))
		case libraryoutputresources.ExactConfigMap("openshift-authentication", "v4-0-config-system-cliconfig"):
			err = logCompletedActionOnOutputResourceFn(clusterType, actionType, currentResourceExactID, applyUpdateConfigMapOpenshiftAuthenticationConfigSystemCliconfig(ctx, mgmtKubeClient, request, o.Namespace))
		case libraryoutputresources.ExactSecret("openshift-authentication", "v4-0-config-system-session"):
			err = logCompletedActionOnOutputResourceFn(clusterType, actionType, currentResourceExactID, applyUpdateSecretOpenshiftAuthenticationConfigSystemSession(ctx, mgmtKubeClient, request, o.Namespace))
		case libraryoutputresources.ExactSecret("openshift-authentication", "v4-0-config-system-ocp-branding-template"):
			err = logCompletedActionOnOutputResourceFn(clusterType, actionType, currentResourceExactID, applyUpdateSecretOpenshiftAuthenticationConfigSystemOCPBrandingTemplate(ctx, mgmtKubeClient, request, o.Namespace))
		case libraryoutputresources.ExactService("openshift-authentication", "oauth-openshift"):
			// TODO: fix me
			ctrl.Log.Info("WARNING:FIX:ME skipping Update on a resource", "clusterType", clusterType, "actionType", actionType, "resource", currentResourceExactID)
		default:
			return fmt.Errorf("unable to apply Update on an unsupported (not impelemented) resource: %v, clusterType: %v", currentResourceExactID, clusterType)
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func (o *OpenshiftManagerOperator) applyAuthOperatorApplyStatusOnOutputResourcesOnManagementCluster(ctx context.Context, clusterType libraryapplyconfiguration.ClusterType, actionType manifestclient.Action, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient, applyStatusRequests []manifestclient.SerializedRequestish) error {
	for _, request := range applyStatusRequests {
		var err error
		currentResourceExactID := serializedRequestActionMetadataToExactResource(request.GetSerializedRequest().ActionMetadata)
		switch currentResourceExactID {
		// operator.openshift.io
		case libraryoutputresources.ExactLowLevelOperator("authentications"):
			err = logCompletedActionOnOutputResourceFn(clusterType, actionType, currentResourceExactID, applyActionOperatorAuthenticationCluster(ctx, actionType, mgmtKubeClient, guestClusterKubeClient, request, o.Namespace))
		default:
			return fmt.Errorf("unable to apply ApplyStatus on an unsupported (not impelemented) resource: %v, clusterType: %v", currentResourceExactID, clusterType)
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func (o *OpenshiftManagerOperator) applyAuthOperatorCreateOnOutputResourcesOnManagementCluster(ctx context.Context, clusterType libraryapplyconfiguration.ClusterType, actionType manifestclient.Action, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient, createRequests []manifestclient.SerializedRequestish) error {
	for _, request := range createRequests {
		var err error
		currentResourceExactID := serializedRequestActionMetadataToExactResource(request.GetSerializedRequest().ActionMetadata)
		switch currentResourceExactID {
		case libraryoutputresources.ExactConfigMap("openshift-authentication", "v4-0-config-system-cliconfig"):
			err = logCompletedActionOnOutputResourceFn(clusterType, actionType, currentResourceExactID, applyCreateConfigMapOpenshiftAuthenticationConfigSystemCliconfig(ctx, mgmtKubeClient, request, o.Namespace))
		case libraryoutputresources.ExactSecret("openshift-authentication", "v4-0-config-system-session"):
			err = logCompletedActionOnOutputResourceFn(clusterType, actionType, currentResourceExactID, applyCreateSecretOpenshiftAuthenticationConfigSystemSession(ctx, mgmtKubeClient, request, o.Namespace))
		case libraryoutputresources.ExactDeployment("openshift-authentication", "oauth-openshift"):
			// TODO: fix me
			//err = logCompletedActionOnOutputResourceFn(clusterType, actionType, currentResourceExactID, applyCreateDeploymentOpenshiftAuthenticationOauthOpenshift(ctx, mgmtKubeClient, request, o.Namespace))
		case libraryoutputresources.ExactConfigMap("openshift-authentication", "audit"):
			err = logCompletedActionOnOutputResourceFn(clusterType, actionType, currentResourceExactID, applyCreateConfigMapOpenshiftAuthenticationAudit(ctx, mgmtKubeClient, request, o.Namespace))
		case libraryoutputresources.ExactSecret("openshift-authentication", "v4-0-config-system-ocp-branding-template"):
			err = logCompletedActionOnOutputResourceFn(clusterType, actionType, currentResourceExactID, applyCreateSecretOpenshiftAuthenticationConfigSystemOcpBrandingTemplate(ctx, mgmtKubeClient, request, o.Namespace))
		//
		// BELOW ARE RESOURCES WHICH ARE SKIPPED
		// WE NEED TO FIGURE OUT WHICH ARE REQUIRED/NEEDED
		// TODO: ^
		case libraryoutputresources.ExactRole("openshift-config-managed", "system:openshift:oauth-servercert-trust"):
			// TODO: fix me
			ctrl.Log.Info("WARNING:FIX:ME: skipping Create of a resource", "clusterType", clusterType, "actionType", actionType, "resource", currentResourceExactID)
		case libraryoutputresources.ExactRoleBinding("openshift-config-managed", "system:openshift:oauth-servercert-trust"):
			// TODO: fix me
			ctrl.Log.Info("WARNING:FIX:ME skipping Create of a resource", "clusterType", clusterType, "actionType", actionType, "resource", currentResourceExactID)
		case libraryoutputresources.ExactNamespace("openshift-authentication"):
			// TODO: fix me
			ctrl.Log.Info("WARNING:FIX:ME skipping Create of a resource", "clusterType", clusterType, "actionType", actionType, "resource", currentResourceExactID)
		case libraryoutputresources.ExactConfigMap("openshift-authentication", "v4-0-config-system-trusted-ca-bundle"):
			// TODO: perhaps we need to find a way to deliver system-trusted-ca-bundle
			//   to the oauth-server.This needs to be figured out.
			//   On standalone (I think) that CNO provides content to this CM
			ctrl.Log.Info("WARNING: skipping Create of a resource (not clear it's required/needed)", "clusterType", clusterType, "actionType", actionType, "resource", currentResourceExactID)
		case libraryoutputresources.ExactServiceAccount("openshift-authentication", "oauth-openshift"):
			// TODO: fix me
			ctrl.Log.Info("WARNING:FIX:ME skipping Create of a resource", "clusterType", clusterType, "actionType", actionType, "resource", currentResourceExactID)
		default:
			currentResourceGR := schema.GroupResource{Group: currentResourceExactID.Group, Resource: currentResourceExactID.Resource}
			if currentResourceGR == coreEventGR || currentResourceGR == eventGR {
				//ctrl.Log.Info("WARNING: skipping Create of an event resource", "clusterType", clusterType, "actionType", actionType, "gr", coreEventGR)
				continue
			}
			return fmt.Errorf("unable to apply Create on an unsupported (not impelemented) resource: %v, clusterType: %v", currentResourceExactID, clusterType)
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
func getAuthOperatorStaticInputResources() *libraryinputresources.InputResources {
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
				// oauth-openshift (aka. oauth-server)
				libraryinputresources.ExactResource("route.openshift.io", "v1", "routes", "openshift-authentication", "oauth-openshift"),
				libraryinputresources.ExactResource("", "v1", "services", "openshift-authentication", "oauth-openshift"),
				libraryinputresources.ExactSecret("openshift-authentication", "v4-0-config-system-router-certs"),
				// TODO: fix me (reconcile v4-0-config-system-custom-router-certs)
				//libraryinputresources.ExactSecret("openshift-authentication", "v4-0-config-system-custom-router-certs"),
				libraryinputresources.ExactConfigMap("openshift-authentication", "v4-0-config-system-cliconfig"),
				libraryinputresources.ExactSecret("openshift-authentication", "v4-0-config-system-session"),
				libraryinputresources.ExactSecret("openshift-authentication", "v4-0-config-system-serving-cert"),
				libraryinputresources.ExactConfigMap("openshift-authentication", "v4-0-config-system-service-ca"),
				libraryinputresources.ExactSecret("openshift-authentication", "v4-0-config-system-ocp-branding-template"),
				libraryinputresources.ExactConfigMap("openshift-authentication", "audit"),
			},
		},
	}
}
