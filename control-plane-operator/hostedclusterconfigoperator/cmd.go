package hostedclusterconfigoperator

/*
The hosted-cluster-config-operator is responsible for reconciling resources
that live inside the hosted cluster. It is also responsible for updating
configuration that lives in the control plane based on the state of hosted
cluster configuration resources.

The main controller that accomplishes this is the resources controller. This
is where new reconciliation code should go, unless there is good reason to
create a separate controller.
*/

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/configmetrics"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/cmca"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/drainer"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/hcpstatus"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/inplaceupgrader"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/machine"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/node"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/nodecount"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/labelenforcingclient"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/supportedversion"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	"k8s.io/client-go/rest"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
)

const (
	defaultReleaseVersion    = "0.0.1-snapshot"
	defaultKubernetesVersion = "0.0.1-snapshot-kubernetes"
)

func NewCommand() *cobra.Command {
	return newHostedClusterConfigOperatorCommand()
}

var controllerFuncs = map[string]operator.ControllerSetupFunc{
	"controller-manager-ca":  cmca.Setup,
	resources.ControllerName: resources.Setup,
	"inplaceupgrader":        inplaceupgrader.Setup,
	"node":                   node.Setup,
	nodecount.ControllerName: nodecount.Setup,
	"machine":                machine.Setup,
	"drainer":                drainer.Setup,
	hcpstatus.ControllerName: hcpstatus.Setup,
}

type HostedClusterConfigOperator struct {
	// Namespace is the namespace on the management cluster where the control plane components run.
	Namespace string

	// HostedControlPlaneName is the name of the hosted control plane that owns this operator instance.
	HostedControlPlaneName string

	// TargetKubeconfig is a kubeconfig to access the target cluster.
	TargetKubeconfig string

	// KubevirtInfraKubeconfig is a kubeconfig to access the infra cluster.
	KubevirtInfraKubeconfig string

	// InitialCAFile is a file containing the initial contents of the Kube controller manager CA.
	InitialCAFile string

	// Controllers is the list of controllers that the operator should start
	Controllers []string

	// ClusterSignerCAFile is a file containing the cluster signer CA cert
	ClusterSignerCAFile string

	// ReleaseVersion is the OpenShift version for the release
	ReleaseVersion string

	// KubernetesVersion is the kubernetes version included in the release
	KubernetesVersion string

	// KonnectivityAddress is the external address of the konnectivity server
	KonnectivityAddress string

	// KonnectivityPort is the external port of the konnectivity server
	KonnectivityPort int32

	// OAuthAddress is the external address of the oauth server
	OAuthAddress string

	// OAuthPort is the external port of the oauth server
	OAuthPort int32

	initialCA []byte

	platformType string

	enableCIDebugOutput bool

	clusterSignerCA []byte

	registryOverrides map[string]string
}

func newHostedClusterConfigOperatorCommand() *cobra.Command {
	cpo := newHostedClusterConfigOperator()
	cmd := &cobra.Command{
		Use:   "hosted-cluster-config-operator",
		Short: "The Hosted Control Plane Config Operator contains a set of controllers that manage an OpenShift hosted control plane.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cpo.Validate(); err != nil {
				return err
			}
			if err := cpo.Complete(); err != nil {
				return err
			}
			return cpo.Run(ctrl.SetupSignalHandler())
		},
	}
	flags := cmd.Flags()
	flags.AddGoFlagSet(flag.CommandLine)
	flags.StringVar(&cpo.Namespace, "namespace", cpo.Namespace, "Namespace for control plane components on management cluster")
	flags.StringVar(&cpo.TargetKubeconfig, "target-kubeconfig", cpo.TargetKubeconfig, "Kubeconfig for target cluster")
	flags.StringVar(&cpo.KubevirtInfraKubeconfig, "kubevirt-infra-kubeconfig", cpo.KubevirtInfraKubeconfig, "Kubeconfig for infra cluster (kubevirt provider)")
	flags.StringVar(&cpo.InitialCAFile, "initial-ca-file", cpo.InitialCAFile, "Path to controller manager initial CA file")
	flags.StringVar(&cpo.ClusterSignerCAFile, "cluster-signer-ca-file", cpo.ClusterSignerCAFile, "Path to the cluster signer CA cert bundle")
	flags.StringSliceVar(&cpo.Controllers, "controllers", cpo.Controllers, "Controllers to run with this operator")
	flags.StringVar(&cpo.platformType, "platform-type", "", "The platform of the cluster")
	flags.BoolVar(&cpo.enableCIDebugOutput, "enable-ci-debug-output", false, "If extra CI debug output should be enabled")
	flags.StringVar(&cpo.HostedControlPlaneName, "hosted-control-plane", cpo.HostedControlPlaneName, "Name of the hosted control plane that owns this operator")
	flags.StringVar(&cpo.KonnectivityAddress, "konnectivity-address", cpo.KonnectivityAddress, "Address of external konnectivity endpoint")
	flags.Int32Var(&cpo.KonnectivityPort, "konnectivity-port", cpo.KonnectivityPort, "Port of external konnectivity endpoint")
	flags.StringVar(&cpo.OAuthAddress, "oauth-address", cpo.KonnectivityAddress, "Address of external oauth endpoint")
	flags.Int32Var(&cpo.OAuthPort, "oauth-port", cpo.KonnectivityPort, "Port of external oauth endpoint")
	flags.StringToStringVar(&cpo.registryOverrides, "registry-overrides", map[string]string{}, "registry-overrides contains the source registry string as a key and the destination registry string as value. Images before being applied are scanned for the source registry string and if found the string is replaced with the destination registry string. Format is: sr1=dr1,sr2=dr2")
	return cmd
}

func newHostedClusterConfigOperator() *HostedClusterConfigOperator {
	return &HostedClusterConfigOperator{
		Controllers: allControllers(),
	}
}

func allControllers() []string {
	controllers := make([]string, 0, len(controllerFuncs))
	for name := range controllerFuncs {
		if name == nodecount.ControllerName && os.Getenv("ENABLE_SIZE_TAGGING") != "1" {
			continue
		}
		controllers = append(controllers, name)
	}
	return controllers
}

func (o *HostedClusterConfigOperator) Validate() error {
	if len(o.Controllers) == 0 {
		return fmt.Errorf("at least one controller is required")
	}
	if len(o.Namespace) == 0 {
		return fmt.Errorf("the namespace for control plane components is required")
	}
	return nil
}

func (o *HostedClusterConfigOperator) Complete() error {
	var err error
	if len(o.InitialCAFile) > 0 {
		o.initialCA, err = os.ReadFile(o.InitialCAFile)
		if err != nil {
			return err
		}
	}
	if o.ClusterSignerCAFile != "" {
		o.clusterSignerCA, err = os.ReadFile(o.ClusterSignerCAFile)
		if err != nil {
			return err
		}
	}
	o.ReleaseVersion = os.Getenv("OPENSHIFT_RELEASE_VERSION")
	if o.ReleaseVersion == "" {
		o.ReleaseVersion = defaultReleaseVersion
	}
	o.KubernetesVersion = os.Getenv("KUBERNETES_VERSION")
	if o.KubernetesVersion == "" {
		o.KubernetesVersion = defaultKubernetesVersion
	}
	if o.platformType == "" {
		return errors.New("--platform-type is required")
	}
	return nil
}

func (o *HostedClusterConfigOperator) Run(ctx context.Context) error {
	ctrl.SetLogger(zap.New(zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))
	versions := map[string]string{
		"release":    o.ReleaseVersion,
		"kubernetes": o.KubernetesVersion,
	}
	cfg := operator.CfgFromFile(o.TargetKubeconfig)
	cpConfig := ctrl.GetConfigOrDie()
	mgr := operator.Mgr(cfg, cpConfig, o.Namespace)
	mgr.GetLogger().Info("Starting hosted-cluster-config-operator", "version", supportedversion.String())
	cpCluster, err := cluster.New(cpConfig, func(opt *cluster.Options) {
		opt.Cache = cache.Options{
			DefaultNamespaces: map[string]cache.Config{o.Namespace: {}},
		}
		opt.Scheme = api.Scheme
	})
	if err != nil {
		return fmt.Errorf("cannot create control plane cluster: %v", err)
	}
	if err := mgr.Add(cpCluster); err != nil {
		return fmt.Errorf("cannot add CPCluster to manager: %v", err)
	}
	var kubevirtInfraConfig *rest.Config
	if o.KubevirtInfraKubeconfig != "" {
		kubevirtInfraConfig = operator.CfgFromFile(o.KubevirtInfraKubeconfig)
	} else {
		// in case infra kubeconfig hasn't been provided, default the kubevirtInfraCluster to cpConfig
		kubevirtInfraConfig = cpConfig
	}

	imageRegistryOverrides := map[string][]string{}
	openShiftImgOverrides, ok := os.LookupEnv("OPENSHIFT_IMG_OVERRIDES")
	if ok {
		imageRegistryOverrides = util.ConvertImageRegistryOverrideStringToMap(openShiftImgOverrides)
	}
	if len(o.registryOverrides) > 0 {
		if imageRegistryOverrides == nil {
			imageRegistryOverrides = map[string][]string{}
		}
		for registry, override := range o.registryOverrides {
			if _, exists := imageRegistryOverrides[registry]; !exists {
				imageRegistryOverrides[registry] = []string{}
			}
			imageRegistryOverrides[registry] = append(imageRegistryOverrides[registry], override)
		}
	}

	releaseProvider := &releaseinfo.ProviderWithOpenShiftImageRegistryOverridesDecorator{
		Delegate: &releaseinfo.RegistryMirrorProviderDecorator{
			Delegate: &releaseinfo.StaticProviderDecorator{
				Delegate: &releaseinfo.CachedProvider{
					Inner: &releaseinfo.RegistryClientProvider{},
					Cache: map[string]*releaseinfo.ReleaseImage{},
				},
			},
			RegistryOverrides: nil,
		},
		OpenShiftImageRegistryOverrides: imageRegistryOverrides,
	}

	imageMetaDataProvider := &util.RegistryClientImageMetadataProvider{
		OpenShiftImageRegistryOverrides: imageRegistryOverrides,
	}

	apiReadingClient, err := client.New(mgr.GetConfig(), client.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return fmt.Errorf("failed to construct api reading client: %w", err)
	}

	controllersToRun := map[string]operator.ControllerSetupFunc{}
	for _, controllerName := range o.Controllers {
		if setup, registered := controllerFuncs[controllerName]; !registered {
			return fmt.Errorf("requested to run unknown controller %q", controllerName)
		} else {
			controllersToRun[controllerName] = setup
		}
	}

	operatorConfig := &operator.HostedClusterConfigOperatorConfig{
		TargetCreateOrUpdateProvider: &labelenforcingclient.LabelEnforcingUpsertProvider{
			Upstream:  upsert.New(o.enableCIDebugOutput),
			APIClient: apiReadingClient,
		},

		Config:                cpConfig,
		TargetConfig:          cfg,
		KubevirtInfraConfig:   kubevirtInfraConfig,
		Manager:               mgr,
		Namespace:             o.Namespace,
		HCPName:               o.HostedControlPlaneName,
		InitialCA:             string(o.initialCA),
		ClusterSignerCA:       string(o.clusterSignerCA),
		ControllerFuncs:       controllersToRun,
		Versions:              versions,
		PlatformType:          hyperv1.PlatformType(o.platformType),
		CPCluster:             cpCluster,
		Logger:                ctrl.Log.WithName("hypershift-operator"),
		ReleaseProvider:       releaseProvider,
		KonnectivityAddress:   o.KonnectivityAddress,
		KonnectivityPort:      o.KonnectivityPort,
		OAuthAddress:          o.OAuthAddress,
		OAuthPort:             o.OAuthPort,
		OperateOnReleaseImage: os.Getenv("OPERATE_ON_RELEASE_IMAGE"),
		EnableCIDebugOutput:   o.enableCIDebugOutput,
		ImageMetaDataProvider: imageMetaDataProvider,
	}
	configmetrics.Register(mgr.GetCache())
	return operatorConfig.Start(ctx)
}
