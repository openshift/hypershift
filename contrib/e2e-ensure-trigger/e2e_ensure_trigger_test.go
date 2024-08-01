package e2etrigger

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	//. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"go.uber.org/zap/zapcore"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	crzap "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type options struct {
	TestName       string
	KubeconfigPath string
	HCNamespace    string
	HCName         string
}

var (
	globalOpts = &options{
		TestName:       "",
		KubeconfigPath: filepath.Join(homedir.HomeDir(), ".kube", "config"),
		HCNamespace:    "",
		HCName:         "",
	}

	log = crzap.New(crzap.UseDevMode(true), crzap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	}))
	ensureFuncs = map[string]e2eutil.EnsureFunc{}
)

func init() {
	var (
		EnsureFuncNoCrashingPods e2eutil.EnsureFunc = e2eutil.EnsureNoCrashingPods
	)

	ensureFuncs["EnsureNoCrashingPods"] = EnsureFuncNoCrashingPods

	//ToDo (jparrill): functions to migrate to ensureFuncs
	//EnsureNoPodsWithTooHighPriority(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster)
	//EnsureOAPIMountsTrustBundle(t *testing.T, ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster)
	//EnsureAllContainersHavePullPolicyIfNotPresent(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster)
	//EnsureNodeCountMatchesNodePoolReplicas(t *testing.T, ctx context.Context, hostClient, guestClient crclient.Client, platform hyperv1.PlatformType, nodePoolNamespace string)
	//EnsureMachineDeploymentGeneration(t *testing.T, ctx context.Context, hostClient crclient.Client, hostedCluster *hyperv1.HostedCluster, expectedGeneration int64)
	//EnsurePSANotPrivileged(t *testing.T, ctx context.Context, guestClient crclient.Client)
	//EnsureAllRoutesUseHCPRouter(t *testing.T, ctx context.Context, hostClient crclient.Client, hostedCluster *hyperv1.HostedCluster)
	//EnsureNetworkPolicies(t *testing.T, ctx context.Context, c crclient.Client, hostedCluster *hyperv1.HostedCluster)
	//EnsureHCPContainersHaveResourceRequests(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster)
	//EnsureSecretEncryptedUsingKMS(t *testing.T, ctx context.Context, hostedCluster *hyperv1.HostedCluster, guestClient crclient.Client)
	//EnsurePodsWithEmptyDirPVsHaveSafeToEvictAnnotations(t *testing.T, ctx context.Context, hostClient crclient.Client, hcpNs string)
	//EnsureGuestWebhooksValidated(t *testing.T, ctx context.Context, guestClient crclient.Client)
	//EnsureHCPPodsAffinitiesAndTolerations(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster)
	//EnsureOnlyRequestServingPodsOnRequestServingNodes(t *testing.T, ctx context.Context, client crclient.Client)
	//EnsureAllReqServingPodsLandOnReqServingNodes(t *testing.T, ctx context.Context, client crclient.Client)
	//EnsureNoHCPPodsLandOnDefaultNode(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster)
	//EnsureSATokenNotMountedUnlessNecessary(t *testing.T, ctx context.Context, c crclient.Client, hostedCluster *hyperv1.HostedCluster)
	//EnsureOAuthWithIdentityProvider(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster)
	//EnsureNodeCommunication(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster)
	//EnsureNodesLabelsAndTaints(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node)
	//EnsurePullSecret(serviceAccount *corev1.ServiceAccount, secretName string)
}

func TestMain(m *testing.M) {
	ctrl.SetLogger(log)

	flag.StringVar(&globalOpts.TestName, "name", "", "Function name to trigger (E.G EnsureNoCrashingPods)")
	flag.StringVar(&globalOpts.HCNamespace, "hcns", "", "HostedCluster namespace")
	flag.StringVar(&globalOpts.HCName, "hc", "", "HostedCluster name")
	flag.Parse()
	if err := len(globalOpts.TestName); err <= 0 {
		fmt.Println("Test name is required")
		return
	}
	if err := len(globalOpts.HCName); err <= 0 {
		fmt.Println("HostedCluster name is required")
		return
	}
	if err := len(globalOpts.HCNamespace); err <= 0 {
		fmt.Println("HostedCluster namepace is required")
		return
	}

	os.Exit(m.Run())

}

func getKubeconfig(kubeconfigPath string) (crclient.Client, discovery.DiscoveryClient, error) {
	var (
		discoveryClient *discovery.DiscoveryClient
		k8sClient       crclient.Client
		err             error
	)

	if value := os.Getenv("KUBECONFIG"); value != "" {
		kubeconfigPath = value
	}

	restConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		panic(fmt.Errorf("failed to get kubeconfig: %w", err))
	}

	discoveryClient, err = discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		panic(fmt.Errorf("failed to construct discovery client: %w", err))
	}

	k8sClient, err = crclient.New(restConfig, crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		fmt.Printf("Error creating Kubernetes client: %s\n", err.Error())
		os.Exit(1)
	}

	return k8sClient, *discoveryClient, nil
}

func TestTriggerFunction(t *testing.T) {
	ctx := context.TODO()
	mgmtClient, _, err := getKubeconfig(globalOpts.KubeconfigPath)
	if err != nil {
		t.Logf("Error getting kubeconfig: %s\n", err.Error())
		t.FailNow()
	}

	hostedCluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      globalOpts.HCName,
			Namespace: globalOpts.HCNamespace,
		},
	}

	err = mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	if err != nil {
		t.Logf("Error getting HostedCluster: %s\n", err.Error())
		t.FailNow()
	}

	err = callFunctionByName(globalOpts.TestName, t, ctx, &e2eutil.TestParams{
		HostedCluster: hostedCluster,
		MgmtClient:    mgmtClient,
		HcpNS:         fmt.Sprintf("%s-%s", hostedCluster.Namespace, hostedCluster.Name),
	})
	if err != nil {
		t.Logf("Error calling function %s: %s\n", globalOpts.TestName, err.Error())
		os.Exit(1)
	}
}

func callFunctionByName(testName string, t *testing.T, ctx context.Context, params *e2eutil.TestParams) error {
	if e2eFunc, ok := ensureFuncs[testName]; ok {
		// Check if function is of the correct type
		funcType := reflect.TypeOf(e2eFunc)
		if funcType.Kind() == reflect.Func {
			// Call the function
			results := reflect.ValueOf(e2eFunc).Call([]reflect.Value{
				reflect.ValueOf(t),
				reflect.ValueOf(ctx),
				reflect.ValueOf(params),
			})
			if len(results) == 1 && results[0].Interface() != nil {
				return results[0].Interface().(error)
			}
			return nil
		}
	}

	return fmt.Errorf("function %s not found", testName)
}
