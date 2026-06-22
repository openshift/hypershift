package framework

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hcpmanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	homanifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests"

	v1 "k8s.io/api/authentication/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/go-logr/logr"
)

// WaitForGuestRestConfig returns the raw content of a KubeConfig for the guest cluster's API server.
// In order to get connectivity to the server, we start a process that port-forwards to the Service.
func WaitForGuestRestConfig(ctx context.Context, logger logr.Logger, opts *Options, t *testing.T, hc *hypershiftv1beta1.HostedCluster) (*rest.Config, error) {
	forwardedLocalPort, err := GetFreePort(ctx, logger, opts, t)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch a free port: %w", err)
	}

	mgmtCfg, err := LoadKubeConfig(opts.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("couldn't load kubeconfig: %w", err)
	}
	mgmtKubeClient, err := kubernetes.NewForConfig(mgmtCfg)
	if err != nil {
		return nil, fmt.Errorf("couldn't create mgmt kube client: %w", err)
	}

	hostedControlPlaneNamespace := homanifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
	localKubeConfig := hcpmanifests.KASLocalhostKubeconfigSecret(hostedControlPlaneNamespace)
	if err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 2*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		s, err := mgmtKubeClient.CoreV1().Secrets(localKubeConfig.Namespace).Get(ctx, localKubeConfig.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			// this is ok, we just need to wait longer
			return false, nil
		}
		if err != nil {
			return true, fmt.Errorf("couldn't get local kubeconfig %s/%s: %w", localKubeConfig.Namespace, localKubeConfig.Name, err)
		}
		localKubeConfig = s
		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to find the local kubeconfig %s/%s: %w", localKubeConfig.Namespace, localKubeConfig.Name, err)
	}

	guestClientCfg, err := clientcmd.NewClientConfigFromBytes(localKubeConfig.Data["kubeconfig"])
	if err != nil {
		return nil, fmt.Errorf("couldn't load guest cluster kubeconfig: %w", err)
	}
	guestKubeconfig, err := guestClientCfg.RawConfig()
	if err != nil {
		return nil, fmt.Errorf("couldn't load guest cluster kubeconfig: %w", err)
	}
	currentCtx, ok := guestKubeconfig.Contexts[guestKubeconfig.CurrentContext]
	if !ok || currentCtx == nil {
		return nil, fmt.Errorf("malformed guest cluster kubeconfig: current context %q missing or empty: %w", guestKubeconfig.CurrentContext, err)
	}
	currentCluster, ok := guestKubeconfig.Clusters[currentCtx.Cluster]
	if !ok || currentCluster == nil {
		return nil, fmt.Errorf("malformed guest cluster kubeconfig: current cluster %q missing or empty: %w", currentCtx.Cluster, err)
	}
	serverURL, err := url.Parse(currentCluster.Server)
	if err != nil {
		return nil, fmt.Errorf("malformed guest cluster kubeconfig: current cluster %q url invalid: %w", currentCtx.Cluster, err)
	}
	host, _, err := net.SplitHostPort(serverURL.Host)
	if err != nil {
		return nil, fmt.Errorf("malformed guest cluster kubeconfig: current cluster %q host invalid: %w", currentCtx.Cluster, err)
	}
	serverURL.Host = net.JoinHostPort(host, forwardedLocalPort)
	guestKubeconfig.Clusters[currentCtx.Cluster].Server = serverURL.String()

	rawGuestKubeconfig, err := clientcmd.Write(guestKubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to write guest kubeconfig: %w", err)
	}

	guestKubeconfigFile, err := Artifact(opts, "guest.kubeconfig")
	if err != nil {
		return nil, fmt.Errorf("failed to open guest kubeconfig file: %w", err)
	}
	if _, err := guestKubeconfigFile.Write(rawGuestKubeconfig); err != nil {
		return nil, fmt.Errorf("failed to write guest kubeconfig file: %w", err)
	}
	if err := guestKubeconfigFile.Close(); err != nil {
		return nil, fmt.Errorf("failed to close guest kubeconfig file: %w", err)
	}

	guestCfg, err := clientcmd.RESTConfigFromKubeConfig(rawGuestKubeconfig)
	if err != nil {
		t.Fatalf("couldn't load guest cluster kubeconfig: %v", err)
	}
	guestCfg.QPS = -1
	guestCfg.Burst = -1

	go func() {
		portForwardCtx := context.Background() // we need this during cleanup, possible to do better but hard
		logPath := "apiserver-port-forward.log"
		cmd := exec.CommandContext(portForwardCtx, opts.OCPath,
			"port-forward", "service/kube-apiserver", "--namespace", hostedControlPlaneNamespace,
			fmt.Sprintf("%s:6443", forwardedLocalPort),
			"--kubeconfig", opts.Kubeconfig,
		)
		if err := StartCommand(logger, opts, logPath, cmd); err != nil {
			logger.Error(err, "failed to start port-forwarding")
		}
	}()

	guestClient, err := kubernetes.NewForConfig(guestCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create guest client: %w", err)
	}
	if err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 2*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		_, err = guestClient.AuthenticationV1().SelfSubjectReviews().Create(ctx, &v1.SelfSubjectReview{}, metav1.CreateOptions{})
		return err == nil, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to find the local kubeconfig %s/%s: %w", localKubeConfig.Namespace, localKubeConfig.Name, err)
	}

	return guestCfg, nil
}

// GetFreePort asks the kernel for a free open port that is ready to use.
func GetFreePort(ctx context.Context, logger logr.Logger, opts *Options, t *testing.T) (string, error) {
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
		if err != nil {
			t.Fatalf("could not resolve free port: %v", err)
		}

		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			t.Fatalf("could not listen on free port: %v", err)
		}
		defer func(c io.Closer) {
			if err := c.Close(); err != nil {
				t.Errorf("could not close listener: %v", err)
			}
		}(l)
		port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
		// Tests run in -parallel will run in separate processes, so we must use the file-system
		// for sharing state and locking across them to coordinate who gets which port. Without
		// some mechanism for sharing state, the following race is possible:
		// - process A calls net.ListenTCP() to resolve a new port
		// - process A calls l.Close() to close the listener to allow accessory to use it
		// - process B calls net.ListenTCP() and resolves the same port
		// - process A attempts to use the port, fails as it is in use
		// Therefore, holding the listener open is our kernel-based lock for this process, and while
		// we hold it open we must record our intent to disk.
		lockDir := filepath.Join(os.TempDir(), "integration-ports")
		if err := os.MkdirAll(lockDir, 0777); err != nil {
			t.Fatalf("could not create port lockfile dir: %v", err)
		}
		lockFile := filepath.Join(lockDir, port)
		if _, err := os.Stat(lockFile); os.IsNotExist(err) {
			// we've never seen this before, we can use it
			f, err := os.Create(lockFile)
			if err != nil {
				t.Fatalf("could not record port lockfile: %v", err)
			}
			if err := f.Close(); err != nil {
				t.Errorf("could not close port lockfile: %v", err)
			}
			// the lifecycle of an accessory (and thereby its ports) is the test lifecycle
			t.Cleanup(func() {
				if err := os.Remove(lockFile); err != nil {
					t.Errorf("failed to remove port lockfile: %v", err)
				}
			})
			t.Logf("found a never-before-seen port, returning: %s", port)
			return port, nil
		} else if err != nil {
			t.Fatalf("could not access port lockfile: %v", err)
		}
		t.Logf("found a previously-seen port, retrying: %s", port)
	}
}
