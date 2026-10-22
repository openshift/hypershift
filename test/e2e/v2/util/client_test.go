//go:build e2ev2

package util

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	autoscalingv1 "github.com/openshift/cluster-autoscaler-operator/pkg/apis/autoscaling/v1"
	autoscalingv1beta1 "github.com/openshift/cluster-autoscaler-operator/pkg/apis/autoscaling/v1beta1"

	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

func managementTestKubeconfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubeconfig")
	config := clientcmdapi.Config{
		Clusters:       map[string]*clientcmdapi.Cluster{"management": {Server: "https://127.0.0.1:6443"}},
		Contexts:       map[string]*clientcmdapi.Context{"management": {Cluster: "management"}},
		CurrentContext: "management",
	}
	if err := clientcmd.WriteToFile(config, path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGetConfig(t *testing.T) {
	t.Run("When kubeconfig is valid, it should retain the endpoint and bound requests without client throttling", func(t *testing.T) {
		t.Setenv("KUBECONFIG", managementTestKubeconfig(t))
		config, err := GetConfig()
		if err != nil {
			t.Fatal(err)
		}
		if config.Host != "https://127.0.0.1:6443" || config.QPS != -1 || config.Burst != -1 || config.Timeout != 5*time.Minute {
			t.Fatal("management config did not preserve endpoint, rate limit or timeout settings")
		}
	})
	t.Run("When kubeconfig is malformed, it should return the loading error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "kubeconfig")
		if err := os.WriteFile(path, []byte("clusters: ["), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("KUBECONFIG", path)
		if config, err := GetConfig(); err == nil || config != nil {
			t.Fatal("expected malformed kubeconfig to fail")
		}
	})
}

func TestGetClient(t *testing.T) {
	t.Run("When kubeconfig is valid, it should register all additional test resource kinds", func(t *testing.T) {
		t.Setenv("KUBECONFIG", managementTestKubeconfig(t))
		client, err := GetClient()
		if err != nil {
			t.Fatal(err)
		}
		for _, object := range []runtime.Object{
			&awskarpenterv1.EC2NodeClass{}, &awskarpenterv1.EC2NodeClassList{},
			&operatorsv1.OperatorGroup{}, &operatorsv1alpha1.Subscription{},
			&autoscalingv1.ClusterAutoscaler{}, &autoscalingv1beta1.MachineAutoscaler{},
		} {
			if _, _, err := client.Scheme().ObjectKinds(object); err != nil {
				t.Errorf("missing resource %T: %v", object, err)
			}
		}
	})
	t.Run("When configuration loading fails, it should not construct a client", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "kubeconfig")
		if err := os.WriteFile(path, []byte("clusters: ["), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("KUBECONFIG", path)
		if client, err := GetClient(); err == nil || client != nil {
			t.Fatal("expected configuration loading failure")
		}
	})
}
