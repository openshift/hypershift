package core

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1alpha1"
	"github.com/spf13/cobra"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperapi "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
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
	}

	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The namespace of the hostedcluster to dump")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "The name of the hostedcluster to dump")
	cmd.Flags().StringVar(&opts.ArtifactDir, "artifact-dir", opts.ArtifactDir, "Destination directory for dump files")
	cmd.Flags().StringVar(&opts.AgentNamespace, "agent-namespace", opts.AgentNamespace, "For agent platform, the namespace where the agents are located")
	cmd.Flags().BoolVar(&opts.DumpGuestCluster, "dump-guest-cluster", opts.DumpGuestCluster, "If the guest cluster contents should also be dumped")

	cmd.MarkFlagRequired("artifact-dir")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := DumpCluster(cmd.Context(), opts); err != nil {
			log.Log.Error(err, "Error")
			return err
		}
		return nil
	}
	return cmd
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
		log.Log.Error(err, "Cannot list nodepools")
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
		log.Log.Error(err, "Cannot list pods in controlplane namespace", "namespace", controlPlaneNamespace)
	}
	hypershiftNSPodList := &corev1.PodList{}
	if err := c.List(ctx, hypershiftNSPodList, client.InNamespace("hypershift")); err != nil {
		log.Log.Error(err, "Failed to list pods in hypershift namespace")
	}
	podList.Items = append(podList.Items, hypershiftNSPodList.Items...)
	kubeClient := kubeclient.NewForConfigOrDie(cfg)
	outputLogs(ctx, kubeClient, opts.ArtifactDir, podList, opts.LogCheckers...)

	if opts.DumpGuestCluster {
		start := time.Now()
		hcluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
			Namespace: opts.Namespace,
			Name:      opts.Name,
		}}
		if err := c.Get(ctx, client.ObjectKeyFromObject(hcluster), hcluster); err != nil {
			return fmt.Errorf("failed to get hostedcluster %s/%s: %w", opts.Namespace, opts.Name, err)
		}
		if hcluster.Status.KubeConfig == nil {
			log.Log.Info("Hostedcluster has no kubeconfig published, skipping guest cluster duming", "namespace", opts.Namespace, "name", opts.Name)
			return nil
		}
		kubeconfigSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Namespace: hcluster.Namespace,
			Name:      hcluster.Status.KubeConfig.Name,
		}}
		if err := c.Get(ctx, client.ObjectKeyFromObject(kubeconfigSecret), kubeconfigSecret); err != nil {
			return fmt.Errorf("failed to get guest cluster kubeconfig secret: %w", err)
		}
		kubeconfigFile, err := ioutil.TempFile(os.TempDir(), "kubeconfig-")
		if err != nil {
			return fmt.Errorf("failed to create tempfile for kubeconfig: %w", err)
		}
		defer func() {
			if err := kubeconfigFile.Close(); err != nil {
				log.Log.Error(err, "Failed to close kubeconfig file")
			}
			if err := os.Remove(kubeconfigFile.Name()); err != nil {
				log.Log.Error(err, "Failed to cleanup temporary kubeconfig")
			}
		}()
		if _, err := kubeconfigFile.Write(kubeconfigSecret.Data["kubeconfig"]); err != nil {
			return fmt.Errorf("failed to write kubeconfig data: %w", err)
		}
		target := opts.ArtifactDir + "/hostedcluster-" + opts.Name
		log.Log.Info("Dumping guestcluster", "target", target)
		if err := DumpGuestCluster(ctx, kubeconfigFile.Name(), target); err != nil {
			return fmt.Errorf("failed to dump guest cluster: %w", err)
		}
		log.Log.Info("Successfully dumped guest cluster", "duration", time.Since(start).String())
	}
	return nil
}

// DumpGuestCluster dumps resources from a hosted cluster using its apiserver
// indicated by the provided kubeconfig. This function assumes that pods aren't
// able to be scheduled and so can only gather information directly accessible
// through the api server.
func DumpGuestCluster(ctx context.Context, kubeconfig string, destDir string) error {
	ocCommand, err := exec.LookPath("oc")
	if err != nil || len(ocCommand) == 0 {
		return fmt.Errorf("cannot find oc command")
	}
	cmd := OCAdmInspect{
		oc:          ocCommand,
		artifactDir: destDir,
		kubeconfig:  kubeconfig,
	}
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
		&configv1.ClusterOperator{},
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
		log.Log.Info("oc adm inspect returned an error", "args", allArgs, "error", err.Error(), "output", string(out))
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

func outputLogs(ctx context.Context, c kubeclient.Interface, artifactDir string, podList *corev1.PodList, checker ...LogChecker) {
	for _, pod := range podList.Items {
		dir := filepath.Join(artifactDir, "namespaces", pod.Namespace, "core", "pods", "logs")
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Log.Error(err, "Cannot create directory", "directory", dir)
			continue
		}
		for _, container := range pod.Spec.InitContainers {
			outputLog(ctx, filepath.Join(dir, fmt.Sprintf("%s-%s.log", pod.Name, container.Name)),
				c.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name}), false, checker...)
			outputLog(ctx, filepath.Join(dir, fmt.Sprintf("%s-%s-previous.log", pod.Name, container.Name)),
				c.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name, Previous: true}), true, checker...)
		}
		for _, container := range pod.Spec.Containers {
			outputLog(ctx, filepath.Join(dir, fmt.Sprintf("%s-%s.log", pod.Name, container.Name)),
				c.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name}), false, checker...)
			outputLog(ctx, filepath.Join(dir, fmt.Sprintf("%s-%s-previous.log", pod.Name, container.Name)),
				c.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name, Previous: true}), true, checker...)
		}
	}
}

func outputLog(ctx context.Context, fileName string, req *restclient.Request, skipLogErr bool, checker ...LogChecker) {
	b, err := req.DoRaw(ctx)
	if err != nil {
		if !skipLogErr {
			log.Log.Info("Failed to get pod log", "req", req.URL().String(), "error", err.Error())
		}
		return
	}
	for _, c := range checker {
		c(fileName, b)
	}
	if err := ioutil.WriteFile(fileName, b, 0644); err != nil {
		log.Log.Error(err, "Failed to write file", "file", fileName)
	}
}
