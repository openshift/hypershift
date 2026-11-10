package integration

import (
	"slices"

	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/internal/testtrace"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	hccomanifests "github.com/openshift/hypershift/pkg/manifests/hcco"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const testNamespace = "hcp-namespace"

type projectionFixture struct {
	controlPlane   []client.Object
	hosted         []client.Object
	cpGetFailures  map[string]error
	upsertFailures map[string]error
}

type projectionRun struct {
	hosted       HostedCluster
	controlPlane ControlPlane
	hostedClient client.WithWatch
	cpClient     client.WithWatch
	trace        *testtrace.Recorder
}

func newProjectionRun(fixture projectionFixture) projectionRun {
	trace := &testtrace.Recorder{}
	hostedClient := testtrace.NewClient(trace, "hosted", testtrace.ClientOptions{Objects: fixture.hosted})
	cpClient := testtrace.NewClient(trace, "control-plane", testtrace.ClientOptions{Objects: fixture.controlPlane, GetFailures: fixture.cpGetFailures})
	upsert := testtrace.FailingUpserter{Failures: fixture.upsertFailures}.CreateOrUpdate
	return projectionRun{
		hosted:       NewHostedCluster(hostedClient, upsert),
		controlPlane: NewControlPlane(cpClient, upsert),
		hostedClient: hostedClient,
		cpClient:     cpClient,
		trace:        trace,
	}
}

func rootCASecret() *corev1.Secret {
	secret := cpomanifests.RootCASecret(testNamespace)
	secret.Data = map[string][]byte{"ca.crt": []byte("root-ca-bundle"), "ca.key": []byte("root-ca-key")}
	return secret
}

func controlPlaneService(service *corev1.Service, clusterIP string) *corev1.Service {
	service.Spec.ClusterIP = clusterIP
	return service
}

func openshiftAPIServerControlPlaneService(clusterIP string) *corev1.Service {
	return controlPlaneService(manifests.OpenShiftAPIServerService(testNamespace), clusterIP)
}

func oauthAPIServerControlPlaneService(clusterIP string) *corev1.Service {
	return controlPlaneService(manifests.OpenShiftOAuthAPIServerService(testNamespace), clusterIP)
}

func kubeadminPasswordSecret() *corev1.Secret {
	secret := hccomanifests.KubeadminPasswordSecret(testNamespace)
	secret.Data = map[string][]byte{"password": []byte("adminpass")}
	return secret
}

func oauthMasterCABundle() *corev1.ConfigMap {
	configMap := cpomanifests.OpenShiftOAuthMasterCABundle(testNamespace)
	configMap.Data = map[string]string{"ca.crt": "oauth-master-ca"}
	return configMap
}

// controlPlaneInputs returns every control-plane object the projections read, with distinct ClusterIPs.
func controlPlaneInputs() []client.Object {
	return []client.Object{
		rootCASecret(),
		openshiftAPIServerControlPlaneService("172.30.0.10"),
		oauthAPIServerControlPlaneService("172.30.0.11"),
		kubeadminPasswordSecret(),
		oauthMasterCABundle(),
	}
}

func without(objects []client.Object, removed ...client.Object) []client.Object {
	var kept []client.Object
	for _, object := range objects {
		if !slices.ContainsFunc(removed, func(candidate client.Object) bool {
			return testtrace.ObjectKey(candidate) == testtrace.ObjectKey(object)
		}) {
			kept = append(kept, object)
		}
	}
	return kept
}

func replacing(objects []client.Object, replacements ...client.Object) []client.Object {
	replaced := make([]client.Object, 0, len(objects))
	for _, object := range objects {
		if index := slices.IndexFunc(replacements, func(candidate client.Object) bool {
			return testtrace.ObjectKey(candidate) == testtrace.ObjectKey(object)
		}); index >= 0 {
			object = replacements[index]
		}
		replaced = append(replaced, object)
	}
	return replaced
}

func enabledParams() Params {
	return Params{Namespace: testNamespace, OAuthEnabled: true, OAuthAddress: "oauth.example.com", OAuthPort: 443}
}

func disabledParams() Params {
	params := enabledParams()
	params.OAuthEnabled = false
	return params
}

func errorMessages(errs []error) []string {
	messages := make([]string, 0, len(errs))
	for _, err := range errs {
		messages = append(messages, err.Error())
	}
	return messages
}
