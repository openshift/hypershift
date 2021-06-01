package cluster

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperapi "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	capiv1 "github.com/openshift/hypershift/thirdparty/clusterapi/api/v1alpha4"
	capiaws "github.com/openshift/hypershift/thirdparty/clusterapiprovideraws/v1alpha4"
)

type DumpOptions struct {
	Namespace   string
	Name        string
	ArtifactDir string
}

func NewDumpCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "cluster",
		Short:        "Creates basic functional HostedCluster resources",
		SilenceUsage: true,
	}

	opts := &DumpOptions{
		Namespace:   "clusters",
		Name:        "example",
		ArtifactDir: "",
	}

	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "A namespace to contain the generated resources")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "A name for the cluster")
	cmd.Flags().StringVar(&opts.ArtifactDir, "artifact-dir", opts.ArtifactDir, "Destination directory for dump files")

	cmd.MarkFlagRequired("artifact-dir")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		if err := DumpCluster(ctx, opts); err != nil {
			log.Error(err, "Error")
		}
		os.Exit(1)
	}
	return cmd
}

func DumpCluster(ctx context.Context, opts *DumpOptions) error {
	ocCommand, err := exec.LookPath("oc")
	if err != nil || len(ocCommand) == 0 {
		return fmt.Errorf("cannot find oc command")
	}
	cfg := util.GetConfigOrDie()
	c := util.GetClientOrDie()
	allNodePools := &hyperv1.NodePoolList{}
	if err = c.List(ctx, allNodePools, client.InNamespace(opts.Namespace)); err != nil {
		log.Error(err, "Cannot list nodepools")
	}
	nodePools := []*hyperv1.NodePool{}
	for i := range allNodePools.Items {
		if allNodePools.Items[i].Spec.ClusterName == opts.Name {
			nodePools = append(nodePools, &allNodePools.Items[i])
		}
	}
	cmd := OCAdmInspect{
		oc:          ocCommand,
		artifactDir: opts.ArtifactDir,
	}
	objectNames := make([]string, 0, len(nodePools)+1)
	objectNames = append(objectNames, typedName(&hyperv1.HostedCluster{}, opts.Name))
	for _, nodePool := range nodePools {
		objectNames = append(objectNames, typedName(&hyperv1.NodePool{}, nodePool.Name))
	}
	cmd.WithNamespace(opts.Namespace).Run(objectNames...)

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
		&capiaws.AWSMachine{},
		&capiaws.AWSMachineTemplate{},
		&capiaws.AWSCluster{},
		&hyperv1.HostedControlPlane{},
	}
	resourceList := strings.Join(resourceTypes(resources), ",")
	cmd.WithNamespace(controlPlaneNamespace).Run(resourceList)
	cmd.WithNamespace(opts.Namespace).Run(resourceList)

	podList := &corev1.PodList{}
	if err = c.List(ctx, podList, client.InNamespace(controlPlaneNamespace)); err != nil {
		log.Error(err, "Cannot list pods in controlplane namespace", "namespace", controlPlaneNamespace)
	}
	kubeClient := kubeclient.NewForConfigOrDie(cfg)
	outputLogs(ctx, kubeClient, opts.ArtifactDir, podList)
	return nil
}

type OCAdmInspect struct {
	oc          string
	artifactDir string
	namespace   string
}

func (i *OCAdmInspect) WithNamespace(namespace string) *OCAdmInspect {
	withNS := *i
	withNS.namespace = namespace
	return &withNS
}

func (i *OCAdmInspect) Run(cmdArgs ...string) {
	allArgs := []string{"adm", "inspect", "--dest-dir", i.artifactDir}
	if len(i.namespace) > 0 {
		allArgs = append(allArgs, "-n", i.namespace)
	}
	allArgs = append(allArgs, cmdArgs...)
	cmd := exec.Command(i.oc, allArgs...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	err := cmd.Run()
	if err != nil {
		log.Error(err, "error running inspect command")
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

func outputLogs(ctx context.Context, c kubeclient.Interface, artifactDir string, podList *corev1.PodList) {
	for _, pod := range podList.Items {
		dir := filepath.Join(artifactDir, "namespaces", pod.Namespace, "core", "pods", "logs")
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Error(err, "Cannot create directory", "directory", dir)
			continue
		}
		for _, container := range pod.Spec.InitContainers {
			outputLog(ctx, filepath.Join(dir, fmt.Sprintf("%s-%s.log", pod.Name, container.Name)),
				c.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name}), false)
			outputLog(ctx, filepath.Join(dir, fmt.Sprintf("%s-%s-previous.log", pod.Name, container.Name)),
				c.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name, Previous: true}), true)
		}
		for _, container := range pod.Spec.Containers {
			outputLog(ctx, filepath.Join(dir, fmt.Sprintf("%s-%s.log", pod.Name, container.Name)),
				c.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name}), false)
			outputLog(ctx, filepath.Join(dir, fmt.Sprintf("%s-%s-previous.log", pod.Name, container.Name)),
				c.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name, Previous: true}), true)
		}
	}
}

func outputLog(ctx context.Context, fileName string, req *restclient.Request, skipLogErr bool) {
	b, err := req.DoRaw(ctx)
	if err != nil {
		if !skipLogErr {
			log.Error(err, "Failed to get pod log", "req", req.URL().String())
		}
		return
	}
	if err := ioutil.WriteFile(fileName, b, 0644); err != nil {
		log.Error(err, "Failed to write file", "file", fileName)
	}
}
