package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	configv1 "github.com/openshift/api/config/v1"
	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	securityv1 "github.com/openshift/api/security/v1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1alpha1"
	"github.com/spf13/cobra"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	kubeclient "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperapi "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
)

type DumpOptions struct {
	Namespace   string
	Name        string
	ArtifactDir string
	// LogCheckers is a list of functions that will
	// get run over all raw logs if set.
	LogCheckers []LogChecker
	// AgentNamespace is the namespace where Agents
	// are located, when using the agent platform.
	AgentNamespace string

	DumpGuestCluster bool

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
		AgentNamespace: "",
		Log:            log.Log,
	}

	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The namespace of the hostedcluster to dump")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "The name of the hostedcluster to dump")
	cmd.Flags().StringVar(&opts.ArtifactDir, "artifact-dir", opts.ArtifactDir, "Destination directory for dump files")
	cmd.Flags().StringVar(&opts.AgentNamespace, "agent-namespace", opts.AgentNamespace, "For agent platform, the namespace where the agents are located")
	cmd.Flags().BoolVar(&opts.DumpGuestCluster, "dump-guest-cluster", opts.DumpGuestCluster, "If the guest cluster contents should also be dumped")

	cmd.MarkFlagRequired("artifact-dir")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
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
	c, err := util.GetClient()
	if err != nil {
		return err
	}
	hcluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
		Namespace: opts.Namespace,
		Name:      opts.Name,
	}}
	if err := c.Get(ctx, client.ObjectKeyFromObject(hcluster), hcluster); err != nil {
		return fmt.Errorf("failed to get hostedcluster %s/%s: %w", opts.Namespace, opts.Name, err)
	}
	if hcluster.Status.KubeConfig == nil {
		opts.Log.Info("Hostedcluster has no kubeconfig published, skipping guest cluster duming", "namespace", opts.Namespace, "name", opts.Name)
		return nil
	}
	kubeconfigSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: hcluster.Namespace,
		Name:      hcluster.Status.KubeConfig.Name,
	}}
	if err := c.Get(ctx, client.ObjectKeyFromObject(kubeconfigSecret), kubeconfigSecret); err != nil {
		return fmt.Errorf("failed to get guest cluster kubeconfig secret: %w", err)
	}
	kubeconfigFile, err := os.CreateTemp(os.TempDir(), "kubeconfig-")
	if err != nil {
		return fmt.Errorf("failed to create tempfile for kubeconfig: %w", err)
	}
	defer func() {
		if err := kubeconfigFile.Close(); err != nil {
			opts.Log.Error(err, "Failed to close kubeconfig file")
		}
		if err := os.Remove(kubeconfigFile.Name()); err != nil {
			opts.Log.Error(err, "Failed to cleanup temporary kubeconfig")
		}
	}()
	if _, err := kubeconfigFile.Write(kubeconfigSecret.Data["kubeconfig"]); err != nil {
		return fmt.Errorf("failed to write kubeconfig data: %w", err)
	}
	target := opts.ArtifactDir + "/hostedcluster-" + opts.Name
	opts.Log.Info("Dumping guestcluster", "target", target)
	if err := DumpGuestCluster(ctx, opts.Log, kubeconfigFile.Name(), target); err != nil {
		return fmt.Errorf("failed to dump guest cluster: %w", err)
	}
	opts.Log.Info("Successfully dumped guest cluster", "duration", time.Since(start).String())

	return nil
}

func DumpCluster(ctx context.Context, opts *DumpOptions) error {
	ocCommand, err := exec.LookPath("oc")
	if err != nil || len(ocCommand) == 0 {
		return fmt.Errorf("cannot find oc command")
	}
	cfg, err := util.GetConfig()
	if err != nil {
		return err
	}
	c, err := util.GetClient()
	if err != nil {
		return err
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
	objectNames := make([]string, 0, len(nodePools)+1)
	objectNames = append(objectNames, typedName(&hyperv1.HostedCluster{}, opts.Name))
	for _, nodePool := range nodePools {
		objectNames = append(objectNames, typedName(&hyperv1.NodePool{}, nodePool.Name))
	}
	cmd.WithNamespace(opts.Namespace).Run(ctx, objectNames...)

	cmd.Run(ctx, objectType(&corev1.Node{}))

	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(opts.Namespace, opts.Name).Name

	resources := []client.Object{
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
		&capiv1.Cluster{},
		&capiv1.MachineDeployment{},
		&capiv1.Machine{},
		&capiv1.MachineSet{},
		&hyperv1.HostedControlPlane{},
		&capiaws.AWSMachine{},
		&capiaws.AWSMachineTemplate{},
		&capiaws.AWSCluster{},
		&hyperv1.AWSEndpointService{},
		&agentv1.AgentMachine{},
		&agentv1.AgentMachineTemplate{},
		&agentv1.AgentCluster{},
		&capikubevirt.KubevirtMachine{},
		&capikubevirt.KubevirtMachineTemplate{},
		&capikubevirt.KubevirtCluster{},
		&routev1.Route{},
		&imagev1.ImageStream{},
		&networkingv1.NetworkPolicy{},
	}
	resourceList := strings.Join(resourceTypes(resources), ",")
	if opts.AgentNamespace != "" {
		// Additional Agent platform resources
		resourceList += ",clusterdeployment.hive.openshift.io,agentclusterinstall.extensions.hive.openshift.io"
	}
	cmd.WithNamespace(controlPlaneNamespace).Run(ctx, resourceList)
	cmd.WithNamespace(opts.Namespace).Run(ctx, resourceList)
	cmd.WithNamespace("hypershift").Run(ctx, resourceList)
	if opts.AgentNamespace != "" {
		cmd.WithNamespace(opts.AgentNamespace).Run(ctx, "agent.agent-install.openshift.io,infraenv.agent-install.openshift.io")
	}

	podList := &corev1.PodList{}
	if err = c.List(ctx, podList, client.InNamespace(controlPlaneNamespace)); err != nil {
		opts.Log.Error(err, "Cannot list pods in controlplane namespace", "namespace", controlPlaneNamespace)
	}
	hypershiftNSPodList := &corev1.PodList{}
	if err := c.List(ctx, hypershiftNSPodList, client.InNamespace("hypershift")); err != nil {
		opts.Log.Error(err, "Failed to list pods in hypershift namespace")
	}
	podList.Items = append(podList.Items, hypershiftNSPodList.Items...)
	kubeClient := kubeclient.NewForConfigOrDie(cfg)
	outputLogs(ctx, opts.Log, kubeClient, opts.ArtifactDir, podList, opts.LogCheckers...)
	gatherNetworkLogs(ocCommand, controlPlaneNamespace, opts.ArtifactDir, ctx, c, opts.Log)

	if opts.DumpGuestCluster {
		if err = dumpGuestCluster(ctx, opts); err != nil {
			opts.Log.Error(err, "Failed to dump guest cluster")
		}
	}

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
	opts.Log.Info("Successfully archied dump", "duration", time.Since(startArchivingDump).String())

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
	resources := []client.Object{
		&apiextensionsv1.CustomResourceDefinition{},
		&appsv1.ControllerRevision{},
		&appsv1.DaemonSet{},
		&appsv1.Deployment{},
		&appsv1.ReplicaSet{},
		&appsv1.StatefulSet{},
		&batchv1.Job{},
		&configv1.ClusterOperator{},
		&corev1.ConfigMap{},
		&corev1.Endpoints{},
		&corev1.Event{},
		&corev1.Namespace{},
		&corev1.Node{},
		&corev1.PersistentVolume{},
		&corev1.PersistentVolumeClaim{},
		&corev1.Pod{},
		&corev1.ReplicationController{},
		&corev1.Service{},
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
	}
	resourceList := strings.Join(resourceTypes(resources), ",")
	cmd.Run(ctx, resourceList)
	return nil
}

type OCAdmInspect struct {
	oc             string
	artifactDir    string
	namespace      string
	kubeconfig     string
	agentNamespace string
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
	allArgs = append(allArgs, cmdArgs...)
	cmd := exec.CommandContext(ctx, i.oc, allArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		i.log.Info("oc adm inspect returned an error", "args", allArgs, "error", err.Error(), "output", string(out))
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

func outputLogs(ctx context.Context, l logr.Logger, c kubeclient.Interface, artifactDir string, podList *corev1.PodList, checker ...LogChecker) {
	for _, pod := range podList.Items {
		dir := filepath.Join(artifactDir, "namespaces", pod.Namespace, "core", "pods", "logs")
		if err := os.MkdirAll(dir, 0755); err != nil {
			l.Error(err, "Cannot create directory", "directory", dir)
			continue
		}
		for _, container := range pod.Spec.InitContainers {
			outputLog(ctx, l, filepath.Join(dir, fmt.Sprintf("%s-%s.log", pod.Name, container.Name)),
				c.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name}), false, checker...)
			outputLog(ctx, l, filepath.Join(dir, fmt.Sprintf("%s-%s-previous.log", pod.Name, container.Name)),
				c.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name, Previous: true}), true, checker...)
		}
		for _, container := range pod.Spec.Containers {
			outputLog(ctx, l, filepath.Join(dir, fmt.Sprintf("%s-%s.log", pod.Name, container.Name)),
				c.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name}), false, checker...)
			outputLog(ctx, l, filepath.Join(dir, fmt.Sprintf("%s-%s-previous.log", pod.Name, container.Name)),
				c.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name, Previous: true}), true, checker...)
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
