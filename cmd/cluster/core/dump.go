package core

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	scheduling "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"
	hyperapi "github.com/openshift/hypershift/support/api"
	supportutil "github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	securityv1 "github.com/openshift/api/security/v1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"

	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	kubeclient "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiopenstackv1alpha1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha1"
	capiopenstackv1beta1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"

	"github.com/go-logr/logr"
	orcv1alpha1 "github.com/k-orc/openstack-resource-controller/api/v1alpha1"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	"github.com/spf13/cobra"
	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

const (
	hypershiftNamespace = "hypershift"
	kubevirtNamespace   = "openshift-cnv"
)

var (
	coreResources = []client.Object{
		&appsv1.DaemonSet{},
		&appsv1.Deployment{},
		&appsv1.ReplicaSet{},
		&appsv1.StatefulSet{},
		&batchv1.Job{},
		&corev1.ConfigMap{},
		&corev1.Endpoints{},
		&corev1.Event{},
		&corev1.PersistentVolumeClaim{},
		&corev1.Pod{},
		&corev1.ReplicationController{},
		&corev1.Service{},
	}

	ocpResources = []client.Object{
		&routev1.Route{},
		&imagev1.ImageStream{},
	}

	kubevirtResources = []client.Object{
		&cdiv1beta1.DataVolume{},
		&kubevirtv1.VirtualMachine{},
		&kubevirtv1.VirtualMachineInstance{},
	}
)

type DumpOptions struct {
	Namespace   string
	Name        string
	ArtifactDir string
	ArchiveDump bool
	// LogCheckers is a list of functions that will
	// get run over all raw logs if set.
	LogCheckers []LogChecker
	// AgentNamespace is the namespace where Agents
	// are located, when using the agent platform.
	AgentNamespace string

	DumpGuestCluster bool
	ImpersonateAs    string

	Log logr.Logger
}

func NewDumpCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "cluster",
		Short:        "Dumps hostedcluster diagnostic info",
		SilenceUsage: true,
	}

	opts := &DumpOptions{
		Namespace:      "clusters",
		Name:           "example",
		ArtifactDir:    "",
		ArchiveDump:    true,
		AgentNamespace: "",
		Log:            log.Log,
	}

	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The namespace of the hostedcluster to dump")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "The name of the hostedcluster to dump")
	cmd.Flags().StringVar(&opts.ImpersonateAs, "as", opts.ImpersonateAs, "The user or service account to impersonate to and used to execute the cluster dump command")
	cmd.Flags().StringVar(&opts.ArtifactDir, "artifact-dir", opts.ArtifactDir, "Destination directory for dump files")
	cmd.Flags().BoolVar(&opts.ArchiveDump, "archive-dump", opts.ArchiveDump, "Create a tar archive of the artifact directory")
	cmd.Flags().StringVar(&opts.AgentNamespace, "agent-namespace", opts.AgentNamespace, "For agent platform, the namespace where the agents are located")
	cmd.Flags().BoolVar(&opts.DumpGuestCluster, "dump-guest-cluster", opts.DumpGuestCluster, "If the guest cluster contents should also be dumped")

	_ = cmd.MarkFlagRequired("artifact-dir")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		rand.New(rand.NewSource(time.Now().UnixNano()))
		if err := DumpCluster(cmd.Context(), opts); err != nil {
			opts.Log.Error(err, "Error")
			return err
		}
		return nil
	}
	return cmd
}

func dumpGuestCluster(ctx context.Context, opts *DumpOptions) error {
	start := time.Now()
	var c client.Client
	var err error

	if len(opts.ImpersonateAs) > 0 {
		c, err = util.GetImpersonatedClient(opts.ImpersonateAs)
	} else {
		c, err = util.GetClient()
	}

	if err != nil {
		return err
	}
	hostedCluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: opts.Namespace, Name: opts.Name}}
	if err := c.Get(ctx, client.ObjectKeyFromObject(hostedCluster), hostedCluster); err != nil {
		return fmt.Errorf("failed to get hosted cluster %s/%s: %w", opts.Namespace, opts.Name, err)
	}
	cpNamespace := manifests.HostedControlPlaneNamespace(opts.Namespace, opts.Name)
	localPort := rand.Intn(45000-32767) + 32767
	kubeconfigFileName, err := createGuestKubeconfig(ctx, c, cpNamespace, localPort, opts.Log)
	if err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(kubeconfigFileName); err != nil {
			opts.Log.Error(err, "Failed to cleanup temporary kubeconfig")
		}
	}()

	target := opts.ArtifactDir + "/hostedcluster-" + opts.Name

	kubeAPIServerPodList := &corev1.PodList{}
	if err := c.List(ctx, kubeAPIServerPodList, client.InNamespace(cpNamespace), client.MatchingLabels{"app": "kube-apiserver", hyperv1.ControlPlaneComponentLabel: "kube-apiserver"}); err != nil {
		return fmt.Errorf("failed to list kube-apiserver pods in control plane namespace: %w", err)
	}
	var podToForward *corev1.Pod
	for i := range kubeAPIServerPodList.Items {
		pod := &kubeAPIServerPodList.Items[i]
		if pod.Status.Phase == corev1.PodRunning {
			podToForward = pod
			break
		}
	}
	if podToForward == nil {
		return fmt.Errorf("did not find running kube-apiserver pod for guest cluster")
	}
	restConfig, err := util.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get a config for management cluster: %w", err)
	}

	if len(opts.ImpersonateAs) > 0 {
		restConfig.Impersonate = restclient.ImpersonationConfig{
			UserName: opts.ImpersonateAs,
		}
	}

	kubeClient, err := kubeclient.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to get a kubernetes client: %w", err)
	}
	forwarderOutput := &bytes.Buffer{}
	forwarder := portForwarder{
		Namespace: podToForward.Namespace,
		PodName:   podToForward.Name,
		Config:    restConfig,
		Client:    kubeClient,
		Out:       forwarderOutput,
		ErrOut:    forwarderOutput,
	}
	podPort := supportutil.KASPodPortFromHostedCluster(hostedCluster)
	forwarderStop := make(chan struct{})
	if err := forwarder.ForwardPorts([]string{fmt.Sprintf("%d:%d", localPort, podPort)}, forwarderStop); err != nil {
		return fmt.Errorf("cannot forward kube apiserver port: %w, output: %s", err, forwarderOutput.String())
	}
	defer close(forwarderStop)

	opts.Log.Info("Dumping guestcluster", "target", target)
	if err := DumpGuestCluster(ctx, opts.Log, kubeconfigFileName, target); err != nil {
		return fmt.Errorf("failed to dump guest cluster: %w", err)
	}
	opts.Log.Info("Successfully dumped guest cluster", "duration", time.Since(start).String())

	return nil
}

func createGuestKubeconfig(ctx context.Context, c client.Client, cpNamespace string, localPort int, log logr.Logger) (string, error) {

	localhostKubeconfigSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "localhost-kubeconfig",
			Namespace: cpNamespace,
		},
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(localhostKubeconfigSecret), localhostKubeconfigSecret); err != nil {
		return "", fmt.Errorf("failed to get hostedcluster localhost kubeconfig: %w", err)
	}
	kubeconfigFile, err := os.CreateTemp(os.TempDir(), "kubeconfig-")
	if err != nil {
		return "", fmt.Errorf("failed to create tempfile for kubeconfig: %w", err)
	}
	defer func() {
		if err := kubeconfigFile.Sync(); err != nil {
			log.Error(err, "Failed to sync temporary kubeconfig file")
		}
		if err := kubeconfigFile.Close(); err != nil {
			log.Error(err, "Failed to close temporary kubeconfig file")
		}
	}()
	localhostKubeconfig, err := clientcmd.Load(localhostKubeconfigSecret.Data["kubeconfig"])
	if err != nil {
		return "", fmt.Errorf("failed to parse localhost kubeconfig: %w", err)
	}
	if len(localhostKubeconfig.Clusters) == 0 {
		return "", fmt.Errorf("no clusters found in localhost kubeconfig")
	}

	for k := range localhostKubeconfig.Clusters {
		localhostKubeconfig.Clusters[k].Server = fmt.Sprintf("https://localhost:%d", localPort)
	}
	localhostKubeconfigYaml, err := clientcmd.Write(*localhostKubeconfig)
	if err != nil {
		return "", fmt.Errorf("failed to serialize localhost kubeconfig: %w", err)
	}
	if _, err := kubeconfigFile.Write(localhostKubeconfigYaml); err != nil {
		return "", fmt.Errorf("failed to write kubeconfig data: %w", err)
	}
	return kubeconfigFile.Name(), nil
}

func DumpCluster(ctx context.Context, opts *DumpOptions) error {
	var c client.Client
	var err error

	ocCommand, err := exec.LookPath("oc")
	if err != nil || len(ocCommand) == 0 {
		return fmt.Errorf("cannot find oc command")
	}
	cfg, err := util.GetConfig()
	if err != nil {
		return err
	}

	if len(opts.ImpersonateAs) > 0 {
		cfg.Impersonate = restclient.ImpersonationConfig{
			UserName: opts.ImpersonateAs,
		}

		c, err = util.GetImpersonatedClient(opts.ImpersonateAs)
		if err != nil {
			return err
		}
	} else {
		c, err = util.GetClient()
		if err != nil {
			return err
		}
	}
	allNodePools := &hyperv1.NodePoolList{}
	if err = c.List(ctx, allNodePools, client.InNamespace(opts.Namespace)); err != nil {
		opts.Log.Error(err, "Cannot list nodepools")
	}
	nodePools := []*hyperv1.NodePool{}
	for i := range allNodePools.Items {
		if allNodePools.Items[i].Spec.ClusterName == opts.Name {
			nodePools = append(nodePools, &allNodePools.Items[i])
		}
	}
	cmd := OCAdmInspect{
		oc:             ocCommand,
		artifactDir:    opts.ArtifactDir,
		agentNamespace: opts.AgentNamespace,
		log:            opts.Log,
	}

	if len(opts.ImpersonateAs) > 0 {
		cmd.impersonate = opts.ImpersonateAs
	}

	objectNames := make([]string, 0, len(nodePools)+1)
	objectNames = append(objectNames, typedName(&hyperv1.HostedCluster{}, opts.Name))
	for _, nodePool := range nodePools {
		objectNames = append(objectNames, typedName(&hyperv1.NodePool{}, nodePool.Name))
	}
	cmd.WithNamespace(opts.Namespace).Run(ctx, objectNames...)

	cmd.Run(ctx, objectType(&corev1.Node{}))

	cmd.Run(ctx, objectType(&scheduling.ClusterSizingConfiguration{}))

	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(opts.Namespace, opts.Name)

	kubevirtExternalInfraClusters, localKubevirtInUse := shouldDumpKubevirt(nodePools)

	resources := append(coreResources,
		&capiv1.Cluster{},
		&capiv1.MachineDeployment{},
		&capiv1.Machine{},
		&capiv1.MachineSet{},
		&hyperv1.HostedControlPlane{},
		&capiaws.AWSMachine{},
		&capiaws.AWSMachineTemplate{},
		&capiaws.AWSCluster{},
		&hyperv1.AWSEndpointService{},
		&capiazure.AzureCluster{},
		&capiazure.AzureMachine{},
		&capiazure.AzureMachineTemplate{},
		&capiopenstackv1alpha1.OpenStackServer{},
		&capiopenstackv1beta1.OpenStackCluster{},
		&capiopenstackv1beta1.OpenStackMachine{},
		&capiopenstackv1beta1.OpenStackMachineTemplate{},
		&orcv1alpha1.Image{},
		&agentv1.AgentMachine{},
		&agentv1.AgentMachineTemplate{},
		&agentv1.AgentCluster{},
		&capikubevirt.KubevirtMachine{},
		&capikubevirt.KubevirtMachineTemplate{},
		&capikubevirt.KubevirtCluster{},
		&policyv1.PodDisruptionBudget{},
		&networkingv1.NetworkPolicy{},
	)

	// These resources are not required to exist since they
	// are live behind a feature gate. Therefore, we'll
	// check whether they are registered in the management
	// cluster before dumping them.
	featureGatedResources := []client.Object{
		&hyperv1.ControlPlaneComponent{},
		&secretsstorev1.SecretProviderClass{},
	}

	// The management cluster may not be an OpenShift cluster.
	// Only dump registered OpenShift GVKs to avoid errors.
	kubeClient := kubeclient.NewForConfigOrDie(cfg)
	kubeDiscoveryClient := kubeClient.Discovery()
	optionalResources := append(featureGatedResources, ocpResources...)
	for _, resource := range optionalResources {
		gvk, err := c.GroupVersionKindFor(resource)
		if err != nil {
			return err
		}
		resourceRegistered, err := isResourceRegistered(kubeDiscoveryClient, gvk)
		if err != nil {
			return err
		}
		if resourceRegistered {
			resources = append(resources, resource)
		}
	}

	if localKubevirtInUse {
		resources = append(resources, kubevirtResources...)
	}

	resourceList := strings.Join(resourceTypes(resources), ",")
	if opts.AgentNamespace != "" {
		// Additional Agent platform resources
		resourceList += ",clusterdeployment.hive.openshift.io,agentclusterinstall.extensions.hive.openshift.io"
	}

	namespaces := []string{controlPlaneNamespace, opts.Namespace, hypershiftNamespace}
	if localKubevirtInUse {
		namespaces = append(namespaces, kubevirtNamespace)
	}
	if sharedingress.UseSharedIngress() {
		namespaces = append(namespaces, sharedingress.RouterNamespace)
	}

	for _, ns := range namespaces {
		cmd.WithNamespace(ns).Run(ctx, resourceList)
	}

	outputLogs(ctx, opts.Log, kubeClient, opts.ArtifactDir, controlPlaneNamespace, opts)
	outputLogs(ctx, opts.Log, kubeClient, opts.ArtifactDir, hypershiftNamespace, opts)
	if sharedingress.UseSharedIngress() {
		outputLogs(ctx, opts.Log, kubeClient, opts.ArtifactDir, sharedingress.RouterNamespace, opts)
	}

	if opts.AgentNamespace != "" {
		cmd.WithNamespace(opts.AgentNamespace).Run(ctx, "agent.agent-install.openshift.io,infraenv.agent-install.openshift.io")
	}

	gatherNetworkLogs(ocCommand, controlPlaneNamespace, opts.ArtifactDir, ctx, c, opts.Log)

	if opts.DumpGuestCluster {
		if err = dumpGuestCluster(ctx, opts); err != nil {
			opts.Log.Error(err, "Failed to dump guest cluster")
		}
	}

	for _, infra := range kubevirtExternalInfraClusters {
		destDit := path.Join(opts.ArtifactDir, "external-infra-clusters", "nodepool-"+infra.uid)
		opts.Log.Info("dumping external infra cluster into " + destDit)

		if err = dumpKubevirtExternalCluster(ctx, c, infra.creds, infra.nodePoolNS, destDit, opts); err != nil {
			opts.Log.Error(err, "Failed to dump infra cluster")
		}
	}

	if opts.ArchiveDump {
		if err = CreateArchive(ctx, opts); err != nil {
			return err
		}
	}

	return nil
}

// DumpGuestCluster dumps resources from a hosted cluster using its apiserver
// indicated by the provided kubeconfig. This function assumes that pods aren't
// able to be scheduled and so can only gather information directly accessible
// through the api server.
func DumpGuestCluster(ctx context.Context, log logr.Logger, kubeconfig string, destDir string) error {
	ocCommand, err := exec.LookPath("oc")
	if err != nil || len(ocCommand) == 0 {
		return fmt.Errorf("cannot find oc command")
	}
	cmd := OCAdmInspect{
		oc:          ocCommand,
		artifactDir: destDir,
		kubeconfig:  kubeconfig,
		log:         log,
	}

	resources := append(coreResources,
		&apiextensionsv1.CustomResourceDefinition{},
		&appsv1.ControllerRevision{},
		&configv1.ClusterOperator{},
		&corev1.Namespace{},
		&corev1.Node{},
		&corev1.PersistentVolume{},
		&rbacv1.ClusterRole{},
		&rbacv1.ClusterRoleBinding{},
		&rbacv1.Role{},
		&rbacv1.RoleBinding{},
		&securityv1.SecurityContextConstraints{},
		&storagev1.CSIDriver{},
		&storagev1.CSINode{},
		&storagev1.StorageClass{},
		&storagev1.VolumeAttachment{},
		// TODO: Filter out when HostedCluster support capabilities && CSISnapshot capability is disabled in the guest cluster.
		// https://github.com/openshift/api/blob/2bde012f248a5172dcde2f7104caf0726cf6d93a/config/v1/types_cluster_version.go#L266-L270
		&snapshotv1.VolumeSnapshotClass{},
		&snapshotv1.VolumeSnapshotContent{},
		&admissionregistrationv1beta1.ValidatingAdmissionPolicy{},
		&admissionregistrationv1beta1.ValidatingAdmissionPolicyBinding{},
	)

	resourceList := strings.Join(resourceTypes(resources), ",")
	cmd.Run(ctx, resourceList)
	dumpWorkerNodeLogsCmd := OCAdmNodeLogs{
		oc:          ocCommand,
		artifactDir: destDir,
		kubeconfig:  kubeconfig,
		role:        "worker",
		log:         log,
	}
	dumpWorkerNodeLogsCmd.Run(ctx)
	return nil
}

type OCAdmInspect struct {
	oc             string
	artifactDir    string
	namespace      string
	kubeconfig     string
	agentNamespace string
	impersonate    string
	log            logr.Logger
}

func (i *OCAdmInspect) WithNamespace(namespace string) *OCAdmInspect {
	withNS := *i
	withNS.namespace = namespace
	return &withNS
}

func (i *OCAdmInspect) Run(ctx context.Context, cmdArgs ...string) {
	allArgs := []string{"adm", "inspect", "--dest-dir", i.artifactDir}
	if len(i.namespace) > 0 {
		allArgs = append(allArgs, "-n", i.namespace)
	}
	if len(i.kubeconfig) > 0 {
		allArgs = append(allArgs, "--kubeconfig", i.kubeconfig)
	}
	if len(i.impersonate) > 0 {
		allArgs = append(allArgs, "--as", i.impersonate)
	}
	allArgs = append(allArgs, cmdArgs...)
	cmd := exec.CommandContext(ctx, i.oc, allArgs...)
	// oc adm inspect command always returns an error so ignore
	_, _ = cmd.CombinedOutput()
}

type OCAdmNodeLogs struct {
	oc          string
	artifactDir string
	kubeconfig  string
	log         logr.Logger
	role        string
}

func (i *OCAdmNodeLogs) Run(ctx context.Context, cmdArgs ...string) {
	allArgs := []string{"adm", "node-logs"}
	if len(i.kubeconfig) > 0 {
		allArgs = append(allArgs, "--kubeconfig", i.kubeconfig)
	}
	nodeLogsFileName := "nodes.log"
	if len(i.role) > 0 {
		allArgs = append(allArgs, "--role", i.role)
		nodeLogsFileName = fmt.Sprintf("%s.%s", i.role, nodeLogsFileName)
	}
	allArgs = append(allArgs, cmdArgs...)
	nodeLogsFile, err := os.Create(filepath.Join(i.artifactDir, nodeLogsFileName))
	if err != nil {
		i.log.Info(fmt.Sprintf("failed creating file to dump node-logs: %v", err))
		return
	}
	defer func(nodeLogsFile *os.File) {
		err := nodeLogsFile.Close()
		if err != nil {
			i.log.Info(fmt.Sprintf("failed closing file to dump node-logs: %v", err))
		}
	}(nodeLogsFile)
	cmd := exec.CommandContext(ctx, i.oc, allArgs...)
	var errb bytes.Buffer
	cmd.Stdout = nodeLogsFile
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		i.log.Info(fmt.Sprintf("failed running command oc %s: %v, %s", strings.Join(allArgs, " "), err, errb.String()))
	}
}

func objectType(obj client.Object) string {
	var kind, group string
	gvks, _, err := hyperapi.Scheme.ObjectKinds(obj)
	if err != nil || len(gvks) == 0 {
		kind = "unknown"
		group = "unknown"
	} else {
		kind = strings.ToLower(gvks[0].Kind)
		group = gvks[0].Group
	}
	if len(group) > 0 {
		return fmt.Sprintf("%s.%s", kind, group)
	} else {
		return kind
	}
}

func resourceTypes(objs []client.Object) []string {
	result := make([]string, 0, len(objs))
	for _, obj := range objs {
		result = append(result, objectType(obj))
	}
	return result
}

func typedName(obj client.Object, name string) string {
	return fmt.Sprintf("%s/%s", objectType(obj), name)
}

type LogChecker func(filename string, content []byte)

func outputLogs(ctx context.Context, l logr.Logger, c kubeclient.Interface, artifactDir string, namespace string, opts *DumpOptions) {
	podList, err := c.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		opts.Log.Error(err, fmt.Sprintf("Cannot list pods in %s namespace", namespace))
		return
	}
	if len(podList.Items) == 0 {
		opts.Log.Info(fmt.Sprintf("No pods in %s namespace", namespace))
		return
	}

	dir := filepath.Join(artifactDir, "namespaces", namespace, "core", "pods", "logs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		l.Error(err, "Cannot create directory", "directory", dir)
		return
	}

	opts.Log.Info("dumping container logs", "namespace", namespace)
	for _, pod := range podList.Items {
		for _, container := range pod.Spec.InitContainers {
			outputLog(ctx, l, filepath.Join(dir, fmt.Sprintf("%s-%s.log", pod.Name, container.Name)),
				c.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name}), false, opts.LogCheckers...)
			outputLog(ctx, l, filepath.Join(dir, fmt.Sprintf("%s-%s-previous.log", pod.Name, container.Name)),
				c.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name, Previous: true}), true, opts.LogCheckers...)
		}
		for _, container := range pod.Spec.Containers {
			outputLog(ctx, l, filepath.Join(dir, fmt.Sprintf("%s-%s.log", pod.Name, container.Name)),
				c.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name}), false, opts.LogCheckers...)
			outputLog(ctx, l, filepath.Join(dir, fmt.Sprintf("%s-%s-previous.log", pod.Name, container.Name)),
				c.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name, Previous: true}), true, opts.LogCheckers...)
		}
	}
}

func outputLog(ctx context.Context, l logr.Logger, fileName string, req *restclient.Request, skipLogErr bool, checker ...LogChecker) {
	b, err := req.DoRaw(ctx)
	if err != nil {
		if !skipLogErr {
			l.Info("Failed to get pod log", "req", req.URL().String(), "error", err.Error())
		}
		return
	}
	for _, c := range checker {
		c(fileName, b)
	}
	if err := os.WriteFile(fileName, b, 0644); err != nil {
		l.Error(err, "Failed to write file", "file", fileName)
	}
}

func gatherNetworkLogs(ocCommand, controlPlaneNamespace, artifactDir string, ctx context.Context, c client.Client, l logr.Logger) {
	// copy ovn dbs and save db cluster status for all ovnkube-master pods
	dir := filepath.Join(artifactDir, "network_logs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		l.Error(err, "Cannot create directory", "directory", dir)
		return
	}
	podList := &corev1.PodList{}
	if err := c.List(ctx, podList, &client.ListOptions{
		Namespace:     controlPlaneNamespace,
		LabelSelector: labels.SelectorFromValidatedSet(labels.Set{"app": "ovnkube-master"}),
	}); err != nil {
		l.Error(err, "Cannot list ovnkube pods in controlplane namespace", "namespace", controlPlaneNamespace)
	}
	for _, pod := range podList.Items {
		for _, dbType := range []string{"n", "s"} {
			allArgs := []string{"cp", fmt.Sprintf("%s/%s:/etc/ovn/ovn%sb_db.db", controlPlaneNamespace, pod.Name, dbType),
				"-c", fmt.Sprintf("%sbdb", dbType), filepath.Join(dir, fmt.Sprintf("%s_ovn%sb_db.db", pod.Name, dbType))}
			cmd := exec.CommandContext(ctx, ocCommand, allArgs...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				l.Info("Copy ovn dbs command returned an error", "args", allArgs, "error", err.Error(), "output", string(out))
			}
			var dbName string
			if dbType == "n" {
				dbName = "OVN_Northbound"
			} else {
				dbName = "OVN_Southbound"
			}
			allArgs = []string{"exec", "-n", controlPlaneNamespace, pod.Name, "-c", fmt.Sprintf("%sbdb", dbType),
				"--", "bash", "-c", fmt.Sprintf("ovn-appctl -t /var/run/ovn/ovn%sb_db.ctl cluster/status %s", dbType, dbName)}
			cmd = exec.CommandContext(ctx, ocCommand, allArgs...)
			out, err = cmd.CombinedOutput()
			if err != nil {
				l.Info("Get ovn db status command returned an error", "args", allArgs, "error", err.Error(), "output", string(out))
			}
			fileName := filepath.Join(dir, fmt.Sprintf("%s_%s_status", pod.Name, dbName))
			if err := os.WriteFile(fileName, out, 0644); err != nil {
				l.Error(err, "Failed to write file", "file", fileName)
			}
		}
	}
}

func CreateArchive(ctx context.Context, opts *DumpOptions) error {
	files, err := os.ReadDir(opts.ArtifactDir)
	if err != nil {
		return fmt.Errorf("failed to list artifactDir %s: %w", opts.ArtifactDir, err)
	}
	args := []string{"-cvzf", "hypershift-dump.tar.gz"}
	for _, file := range files {
		args = append(args, file.Name())
	}

	tarCMD := exec.CommandContext(ctx, "tar", args...)
	tarCMD.Dir = opts.ArtifactDir

	opts.Log.Info("Archiving dump", "command", "tar", "args", args)
	startArchivingDump := time.Now()
	if out, err := tarCMD.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to run tar with %v args: got err %w and out \n%s", args, err, string(out))
	}
	opts.Log.Info("Successfully archived dump", "duration", time.Since(startArchivingDump).String())

	return nil
}

var (
	kvExtResources     = append(coreResources, kubevirtResources...)
	kvExtResourcesList = strings.Join(resourceTypes(kvExtResources), ",")
)

func dumpKubevirtExternalCluster(ctx context.Context, mngmtCl client.Client, creds *hyperv1.KubevirtPlatformCredentials, npNS string, destDir string, opts *DumpOptions) error {
	kubeconfig, err := kvinfra.GetKubeConfig(ctx, mngmtCl, npNS, creds.InfraKubeConfigSecret.Name)
	if err != nil {
		return fmt.Errorf("failed to get KubeVirt external infra-cluster:  %w", err)
	}

	kubeconfigFile, err := writeKubeconfigToFile(kubeconfig)
	if err != nil {
		return err
	}

	defer func() {
		_ = os.Remove(kubeconfigFile)
	}()

	ocCommand, err := exec.LookPath("oc")
	if err != nil || len(ocCommand) == 0 {
		return fmt.Errorf("cannot find oc command")
	}
	cmd := OCAdmInspect{
		oc:          ocCommand,
		artifactDir: destDir,
		kubeconfig:  kubeconfigFile,
		log:         opts.Log,
	}

	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return fmt.Errorf("can't get infra cluster rest config; %w", err)
	}

	kubeClient := kubeclient.NewForConfigOrDie(cfg)

	opts.Log.Info("dumping external infra cluster", "host", cfg.Host)
	namespaces := []string{creds.InfraNamespace, kubevirtNamespace}
	for _, ns := range namespaces {
		cmd.WithNamespace(ns).Run(ctx, kvExtResourcesList)
		outputLogs(ctx, opts.Log, kubeClient, destDir, ns, opts)
	}

	return nil
}

func writeKubeconfigToFile(data []byte) (string, error) {
	kubeconfigFile, err := os.CreateTemp(os.TempDir(), "kubeconfig-")
	if err != nil {
		return "", fmt.Errorf("failed to get create kubeconfig file for KubeVirt external infra-cluster:  %w", err)
	}
	_, err = kubeconfigFile.Write(data)
	if err != nil {
		return "", fmt.Errorf("failed to get create kubeconfig file for KubeVirt external infra-cluster:  %w", err)
	}
	err = kubeconfigFile.Close()
	if err != nil {
		return "", fmt.Errorf("failed to get create kubeconfig file for KubeVirt external infra-cluster:  %w", err)
	}

	return kubeconfigFile.Name(), err
}

type kubevirtExtCluster struct {
	uid        string
	nodePoolNS string
	creds      *hyperv1.KubevirtPlatformCredentials
}

func shouldDumpKubevirt(nodePools []*hyperv1.NodePool) ([]kubevirtExtCluster, bool) {
	var (
		localKubevirtInUse       = false
		kubevirtInExternalInfras []kubevirtExtCluster
	)

	for _, np := range nodePools {
		if np.Spec.Platform.Type == hyperv1.KubevirtPlatform {
			if np.Status.Platform != nil &&
				np.Status.Platform.KubeVirt != nil &&
				np.Status.Platform.KubeVirt.Credentials != nil &&
				np.Status.Platform.KubeVirt.Credentials.InfraKubeConfigSecret != nil {

				extInfra := kubevirtExtCluster{
					uid:        string(np.UID),
					nodePoolNS: np.Namespace,
					creds:      np.Status.Platform.KubeVirt.Credentials,
				}

				kubevirtInExternalInfras = append(kubevirtInExternalInfras, extInfra)
			} else {
				localKubevirtInUse = true
			}
		}
	}

	return kubevirtInExternalInfras, localKubevirtInUse
}

func isResourceRegistered(discoveryClient discovery.DiscoveryInterface, gvk schema.GroupVersionKind) (bool, error) {
	apiResourceLists, err := discoveryClient.ServerResourcesForGroupVersion(gvk.GroupVersion().String())
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	for _, apiResource := range apiResourceLists.APIResources {
		if apiResource.Kind == gvk.Kind {
			return true, nil
		}
	}
	return false, nil
}
