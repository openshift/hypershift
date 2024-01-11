package framework

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// LoadKubeConfig loads a kubeconfig from the file and uses the default context
func LoadKubeConfig(path string) (clientcmd.ClientConfig, error) {
	loader := clientcmd.NewDefaultClientConfigLoadingRules()
	loader.ExplicitPath = path
	cfg, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("could not load kubeconfig: %w", err)
	}
	return clientcmd.NewDefaultClientConfig(*cfg, &clientcmd.ConfigOverrides{}), nil
}

type Cleanup func() error

// CleanupSentinel is a helper for returning a no-op cleanup function.
func CleanupSentinel() error {
	return nil
}

// SkippedCleanupSteps parses $SKIP_CLEANUP as a comma-delimited list of cleanup steps to skip.
func SkippedCleanupSteps() sets.Set[string] {
	skip := os.Getenv("SKIP_CLEANUP")
	if skip == "" {
		return sets.New[string]()
	}
	parts := strings.Split(skip, ",")
	return sets.New[string](parts...)
}

type InjectKubeconfigMode string

const (
	InjectKubeconfigFlag InjectKubeconfigMode = "flag"
	InjectKubeconfigEnv  InjectKubeconfigMode = "env"
)

// EmulateDeployment runs an executable locally that attempts to emulate what the component would have been doing
// on the cluster had it been running in a Pod as part of a Deployment.
// We emulate in-cluster configuration by setting --kubeconfig or $KUBECONFIG - we can't properly emulate in-cluster
// configuration since we are running local processes and not containers where we can mess with the filesystem. We
// grab a token for the ServiceAccount that the Deployment is configured to run with.
//
// A closure is returned that knows how to clean this emulated process up.
func EmulateDeployment(ctx context.Context, logger logr.Logger, opts *Options, waitDuration time.Duration, injectMode InjectKubeconfigMode, namespace, name, containerName, executable string, additionalArgs ...string) (Cleanup, error) {
	logger.Info("emulating deployment", "deployment.namespace", namespace, "deployment.name", name)
	cmdcfg, err := LoadKubeConfig(opts.Kubeconfig)
	cfg, err := cmdcfg.ClientConfig()
	if err != nil {
		return CleanupSentinel, err
	}
	cfg.QPS = -1
	cfg.Burst = -1

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return CleanupSentinel, fmt.Errorf("couldn't create kubernetes client: %w", err)
	}

	var deployment *appsv1.Deployment
	if err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, waitDuration, true, func(ctx context.Context) (done bool, err error) {
		d, err := kubeClient.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			// this is ok, we just need to wait longer
			return false, nil
		}
		if err != nil {
			return true, fmt.Errorf("couldn't get deployment %s/%s: %w", namespace, name, err)
		}
		deployment = d
		return true, nil
	}); err != nil {
		return CleanupSentinel, fmt.Errorf("failed to find the emulated deployment %s/%s: %w", namespace, name, err)
	}

	var ourContainer *corev1.Container
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name == containerName {
			ourContainer = &container
			break
		}
	}
	if ourContainer == nil {
		return CleanupSentinel, fmt.Errorf("deployment %s/%s had no container %s", namespace, name, containerName)
	}

	env := map[string]string{}
	for i, item := range ourContainer.Env {
		if item.ValueFrom != nil {
			if item.ValueFrom.FieldRef != nil {
				switch item.ValueFrom.FieldRef.FieldPath {
				case "metadata.name":
					env[item.Name] = deployment.ObjectMeta.Name
				case "metadata.namespace":
					env[item.Name] = deployment.ObjectMeta.Namespace
				default:
					return CleanupSentinel, fmt.Errorf("deployment %s/%s spec.template.spec.containers[?(@.name==%q)].env[%d].valueFrom.fieldRef.fieldPath unknown: %s", namespace, name, containerName, i, item.ValueFrom.FieldRef.FieldPath)
				}
			} else {
				return CleanupSentinel, fmt.Errorf("deployment %s/%s spec.template.spec.containers[?(@.name==%q)].env[%d].valueFrom unknown: %#v", namespace, name, containerName, i, item.ValueFrom)
			}
		} else {
			env[item.Name] = item.Value
		}
	}

	var envReplacements []string
	for key, value := range env {
		envReplacements = append(envReplacements, fmt.Sprintf("$(%s)", key), value)
	}
	envReplacer := strings.NewReplacer(envReplacements...)

	rawArgs := ourContainer.Args
	if len(ourContainer.Command) != 1 {
		if len(ourContainer.Args) != 0 {
			return CleanupSentinel, fmt.Errorf("deployment %s/%s assumptions invalid: spec.template.spec.containers[?(@.name==%q)].command had more than one entry and non-empty args: %v", namespace, name, containerName, ourContainer.Command)
		}
		// we have many in .command and none in .args, assume .command[1:] are args
		rawArgs = ourContainer.Command[1:]
	}

	var args []string
	for _, arg := range rawArgs {
		args = append(args, envReplacer.Replace(arg))
	}

	rawCfg, err := cmdcfg.RawConfig()
	if err != nil {
		return CleanupSentinel, fmt.Errorf("couldn't fetch raw config: %w", err)
	}
	saNamespace, saName := deployment.ObjectMeta.Namespace, deployment.Spec.Template.Spec.ServiceAccountName
	kubeconfig, err := writeServiceAccountKubeconfig(ctx, logger, opts, rawCfg, saNamespace, saName, executable)
	if err != nil {
		return CleanupSentinel, fmt.Errorf("couldn't create kubeconfig for service account %s/%s: %w", saNamespace, saName, err)
	}
	switch injectMode {
	case InjectKubeconfigFlag:
		args = append(args, "--kubeconfig", kubeconfig)
	case InjectKubeconfigEnv:
		env["KUBECONFIG"] = kubeconfig
	}

	args = append(args, additionalArgs...)

	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, executable, args...)
	var formattedEnv []string
	for key, value := range env {
		formattedEnv = append(formattedEnv, fmt.Sprintf("%s=%s", key, value))
	}
	cmd.Env = formattedEnv
	logPath := fmt.Sprintf("%s.log", filepath.Base(executable))
	return func() error {
		cancel()
		return nil
	}, StartCommand(logger, opts, logPath, cmd)
}

func writeServiceAccountKubeconfig(ctx context.Context, logger logr.Logger, opts *Options, rawCfg clientcmdapi.Config, namespace, serviceAccountName, executable string) (string, error) {
	token := bytes.Buffer{}
	tokenCmd := exec.CommandContext(ctx, opts.OCPath, "create", "token", serviceAccountName, "--namespace", namespace, "--kubeconfig", opts.Kubeconfig)
	tokenCmd.Stdout = &token
	if err := RunCommand(logger, opts, fmt.Sprintf("%s.token.log", filepath.Base(executable)), tokenCmd); err != nil {
		return "", err
	}

	rawCfg.Contexts[rawCfg.CurrentContext].Namespace = namespace
	rawCfg.Contexts[rawCfg.CurrentContext].AuthInfo = "serviceaccount"
	rawCfg.AuthInfos = map[string]*clientcmdapi.AuthInfo{
		"serviceaccount": {
			Token: token.String(),
		},
	}
	saKubeConfig, err := clientcmd.Write(rawCfg)
	if err != nil {
		return "", fmt.Errorf("couldn't encode kubeconfig: %w", err)
	}

	kubeconfigPath := fmt.Sprintf("%s.kubeconfig", filepath.Base(executable))
	kubeconfig, err := Artifact(opts, kubeconfigPath)
	if err != nil {
		return "", fmt.Errorf("couldn't open kubeconfig: %w", err)
	}
	if _, err := kubeconfig.Write(saKubeConfig); err != nil {
		return "", fmt.Errorf("couldn't write kubeconfig: %w", err)
	}
	if err := kubeconfig.Close(); err != nil {
		return "", fmt.Errorf("couldn't close kubeconfig: %w", err)
	}

	return filepath.Join(opts.ArtifactDir, kubeconfigPath), nil
}
