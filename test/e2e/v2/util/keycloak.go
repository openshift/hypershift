//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	routev1 "github.com/openshift/api/route/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	keycloakNamespace      = "keycloak"
	keycloakStatefulSet    = "keycloak"
	keycloakServiceName    = "keycloak"
	keycloakRouteName      = "keycloak"
	keycloakSetupCMName    = "keycloak-setup"
	keycloakCredsSecret    = "keycloak-creds"
	keycloakImage          = "quay.io/keycloak/keycloak:26.0"
	keycloakContainerPort  = 8080
	defaultCLIClientID     = "oc-cli-test"
	defaultConsoleClientID = "console-test"
	defaultTestGroup       = "keycloak-testgroup-1"
	defaultTestUser        = "keycloak-testuser-1"
	defaultRealm           = "master"
)

// KeycloakConfig holds the OIDC configuration produced by DeployKeycloak.
type KeycloakConfig struct {
	// IssuerURL is the OIDC issuer URL (e.g. https://keycloak.apps.example.com/realms/master).
	IssuerURL string
	// CLIClientID is the public OIDC client for CLI authentication.
	CLIClientID string
	// ConsoleClientID is the confidential OIDC client for console authentication.
	ConsoleClientID string
	// ConsoleClientSecret is the secret for the console OIDC client.
	ConsoleClientSecret string
	// TestUsers is a colon-separated string of test user credentials (e.g. "user1:pass1").
	TestUsers string
	// CABundle is the PEM-encoded CA certificate bundle for TLS verification of the issuer.
	CABundle []byte
}

// DeployKeycloak deploys a Keycloak instance on the management cluster for External OIDC testing.
// It creates all required resources (namespace, ConfigMap, Service, StatefulSet, Route),
// waits for the instance to become ready, and returns the OIDC configuration.
func DeployKeycloak(ctx context.Context, client crclient.Client, consoleRedirectURI string) (*KeycloakConfig, error) {
	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("getting kubeconfig for pod exec: %w", err)
	}

	adminUser := "admin"
	adminPass := generateRandomString(16)
	consoleSecret := generateRandomString(32)
	testUsers := fmt.Sprintf("%s:%s", defaultTestUser, generateRandomString(12))

	// Create keycloak namespace
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: keycloakNamespace,
		},
	}
	if err := CreateOrUpdate(ctx, client, ns); err != nil {
		return nil, fmt.Errorf("failed to create keycloak namespace: %w", err)
	}

	// Create setup ConfigMap (scripts and client config)
	cm := buildSetupConfigMap(consoleSecret, consoleRedirectURI)
	if err := CreateOrUpdate(ctx, client, cm); err != nil {
		return nil, fmt.Errorf("failed to create keycloak setup configmap: %w", err)
	}

	// Create credentials Secret (test user passwords)
	credSecret := buildCredentialsSecret(testUsers)
	if err := CreateOrUpdate(ctx, client, credSecret); err != nil {
		return nil, fmt.Errorf("failed to create keycloak credentials secret: %w", err)
	}

	// Create headless Service
	svc := buildKeycloakService()
	if err := CreateOrUpdate(ctx, client, svc); err != nil {
		return nil, fmt.Errorf("failed to create keycloak service: %w", err)
	}

	// Create StatefulSet
	sts := buildKeycloakStatefulSet(adminUser, adminPass)
	if err := CreateOrUpdate(ctx, client, sts); err != nil {
		return nil, fmt.Errorf("failed to create keycloak statefulset: %w", err)
	}

	// Create Route
	if err := createKeycloakRoute(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to create keycloak route: %w", err)
	}

	// Wait for StatefulSet to be ready
	if err := waitForKeycloakReady(ctx, client, restConfig); err != nil {
		return nil, fmt.Errorf("keycloak did not become ready: %w", err)
	}

	// Get issuer URL from Route
	issuerURL, err := getKeycloakIssuerURL(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to get keycloak issuer URL: %w", err)
	}

	// Extract router CA
	caBundle, err := extractRouterCA(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to extract router CA: %w", err)
	}

	// Verify OIDC discovery endpoint
	if err := verifyOIDCEndpoint(ctx, issuerURL, caBundle); err != nil {
		return nil, fmt.Errorf("OIDC discovery endpoint verification failed: %w", err)
	}

	return &KeycloakConfig{
		IssuerURL:           issuerURL,
		CLIClientID:         defaultCLIClientID,
		ConsoleClientID:     defaultConsoleClientID,
		ConsoleClientSecret: consoleSecret,
		TestUsers:           testUsers,
		CABundle:            caBundle,
	}, nil
}

// UpdateKeycloakConsoleClient updates the console client's redirect URI in the
// running Keycloak instance via the Keycloak Admin CLI. The redirect URI
// depends on the hosted cluster's apps domain, which is not known until
// after the cluster is created.
func UpdateKeycloakConsoleClient(ctx context.Context, consoleRedirectURI string) error {
	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("getting kubeconfig for pod exec: %w", err)
	}

	consoleWebOrigin := strings.TrimSuffix(consoleRedirectURI, "/auth/callback")
	kcadmCfg := "/tmp/.keycloak-kcadm.config"
	script := fmt.Sprintf(`
KCADM="/opt/keycloak/bin/kcadm.sh"
KCADM_CONFIG="%s"
${KCADM} config credentials --server http://localhost:%d --realm %s --user ${KEYCLOAK_ADMIN} --password ${KEYCLOAK_ADMIN_PASSWORD} --config=${KCADM_CONFIG}
CLIENT_UUID=$(${KCADM} get clients -r %s -q clientId=%s --fields id --format csv --noquotes --config=${KCADM_CONFIG} | tail -1)
if [ -z "${CLIENT_UUID}" ]; then
  echo "ERROR: console client %s not found"
  exit 1
fi
${KCADM} update clients/${CLIENT_UUID} -r %s -s 'redirectUris=["%s"]' -s 'webOrigins=["%s"]' --config=${KCADM_CONFIG}
echo "Updated console client redirect URI to %s"
`, kcadmCfg, keycloakContainerPort, defaultRealm,
		defaultRealm, defaultConsoleClientID,
		defaultConsoleClientID,
		defaultRealm, consoleRedirectURI, consoleWebOrigin,
		consoleRedirectURI)

	output, err := execInKeycloakPod(ctx, restConfig, script)
	if err != nil {
		return fmt.Errorf("updating console client redirect URI: %w (output: %s)", err, output)
	}
	log.Printf("Updated Keycloak console client: %s", output)
	return nil
}

// CleanupKeycloak removes all Keycloak resources by deleting the keycloak namespace.
func CleanupKeycloak(ctx context.Context, client crclient.Client) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: keycloakNamespace,
		},
	}
	if err := client.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete keycloak namespace: %w", err)
	}
	return nil
}

func buildSetupConfigMap(consoleSecret, consoleRedirectURI string) *corev1.ConfigMap {
	cliClientJSON := `{
  "clientId": "` + defaultCLIClientID + `",
  "enabled": true,
  "publicClient": true,
  "standardFlowEnabled": true,
  "directAccessGrantsEnabled": true,
  "frontchannelLogout": true,
  "redirectUris": ["http://localhost:8080"],
  "webOrigins": ["http://localhost:8080"],
  "defaultClientScopes": ["openid", "profile", "email"],
  "attributes": {
    "access.token.lifespan": "150",
    "client.session.idle.timeout": "7200"
  }
}`

	consoleWebOrigin := strings.TrimSuffix(consoleRedirectURI, "/auth/callback")
	consoleClientJSON := `{
  "clientId": "` + defaultConsoleClientID + `",
  "enabled": true,
  "publicClient": false,
  "secret": "` + consoleSecret + `",
  "standardFlowEnabled": true,
  "directAccessGrantsEnabled": true,
  "frontchannelLogout": true,
  "redirectUris": ["` + consoleRedirectURI + `"],
  "webOrigins": ["` + consoleWebOrigin + `"],
  "defaultClientScopes": ["openid", "profile", "email"],
  "attributes": {
    "access.token.lifespan": "150",
    "client.session.idle.timeout": "7200"
  }
}`

	groupMapperJSON := `{
  "name": "groups",
  "protocol": "openid-connect",
  "protocolMapper": "oidc-group-membership-mapper",
  "config": {
    "full.path": "false",
    "id.token.claim": "true",
    "access.token.claim": "true",
    "claim.name": "groups",
    "userinfo.token.claim": "true"
  }
}`

	cliAudienceMapperJSON := `{
  "name": "cli-audience",
  "protocol": "openid-connect",
  "protocolMapper": "oidc-audience-mapper",
  "config": {
    "included.client.audience": "` + defaultCLIClientID + `",
    "id.token.claim": "true",
    "access.token.claim": "true"
  }
}`

	consoleAudienceMapperJSON := `{
  "name": "console-audience",
  "protocol": "openid-connect",
  "protocolMapper": "oidc-audience-mapper",
  "config": {
    "included.client.audience": "` + defaultConsoleClientID + `",
    "id.token.claim": "true",
    "access.token.claim": "true"
  }
}`

	kcadmConfig := "/tmp/.keycloak-kcadm.config"
	setupScript := `#!/bin/bash
set -euo pipefail

KCADM="/opt/keycloak/bin/kcadm.sh"
KCADM_CONFIG="` + kcadmConfig + `"
REALM="` + defaultRealm + `"

echo "Authenticating to Keycloak..."
${KCADM} config credentials --server http://localhost:` + fmt.Sprintf("%d", keycloakContainerPort) + ` --realm ${REALM} --user ${KEYCLOAK_ADMIN} --password ${KEYCLOAK_ADMIN_PASSWORD} --config=${KCADM_CONFIG}
echo "Keycloak is ready"

# Update session timeout
${KCADM} update realms/${REALM} -s ssoSessionIdleTimeout=7200 --config=${KCADM_CONFIG}

# Create CLI client
${KCADM} create clients -r ${REALM} -f /tmp/.keycloak/cli-client.json --config=${KCADM_CONFIG}
echo "Created CLI client: ` + defaultCLIClientID + `"

# Create console client
${KCADM} create clients -r ${REALM} -f /tmp/.keycloak/console-client.json --config=${KCADM_CONFIG}
echo "Created console client: ` + defaultConsoleClientID + `"

# Add group mapper to CLI client
CLI_CLIENT_UUID=$(${KCADM} get clients -r ${REALM} -q clientId=` + defaultCLIClientID + ` --fields id --format csv --noquotes --config=${KCADM_CONFIG} | tail -1)
${KCADM} create clients/${CLI_CLIENT_UUID}/protocol-mappers/models -r ${REALM} -f /tmp/.keycloak/group-mapper.json --config=${KCADM_CONFIG}
echo "Added group mapper to CLI client"

# Add audience mapper to CLI client so aud claim includes the client ID
${KCADM} create clients/${CLI_CLIENT_UUID}/protocol-mappers/models -r ${REALM} -f /tmp/.keycloak/cli-audience-mapper.json --config=${KCADM_CONFIG}
echo "Added audience mapper to CLI client"

# Add group mapper to console client
CONSOLE_CLIENT_UUID=$(${KCADM} get clients -r ${REALM} -q clientId=` + defaultConsoleClientID + ` --fields id --format csv --noquotes --config=${KCADM_CONFIG} | tail -1)
${KCADM} create clients/${CONSOLE_CLIENT_UUID}/protocol-mappers/models -r ${REALM} -f /tmp/.keycloak/group-mapper.json --config=${KCADM_CONFIG}
echo "Added group mapper to console client"

# Add audience mapper to console client so aud claim includes the client ID
${KCADM} create clients/${CONSOLE_CLIENT_UUID}/protocol-mappers/models -r ${REALM} -f /tmp/.keycloak/console-audience-mapper.json --config=${KCADM_CONFIG}
echo "Added audience mapper to console client"

# Create test group
${KCADM} create groups -r ${REALM} -s name=` + defaultTestGroup + ` --config=${KCADM_CONFIG}
GROUP_ID=$(${KCADM} get groups -r ${REALM} -q search=` + defaultTestGroup + ` --fields id --format csv --noquotes --config=${KCADM_CONFIG} | tail -1)
echo "Created group: ` + defaultTestGroup + `"

# Create test users from testusers file (use || to handle missing trailing newline)
while IFS=: read -r USERNAME PASSWORD || [ -n "${USERNAME}" ]; do
  [ -z "${USERNAME}" ] && continue
  ${KCADM} create users -r ${REALM} -s username=${USERNAME} -s enabled=true -s email="${USERNAME}@example.com" -s emailVerified=true --config=${KCADM_CONFIG}
  ${KCADM} set-password -r ${REALM} --username ${USERNAME} --new-password ${PASSWORD} --config=${KCADM_CONFIG}
  USER_ID=$(${KCADM} get users -r ${REALM} -q username=${USERNAME} --fields id --format csv --noquotes --config=${KCADM_CONFIG} | tail -1)
  ${KCADM} update users/${USER_ID}/groups/${GROUP_ID} -r ${REALM} -s realm=${REALM} -s userId=${USER_ID} -s groupId=${GROUP_ID} -n --config=${KCADM_CONFIG}
  echo "Created user: ${USERNAME} in group ` + defaultTestGroup + `"
done < /tmp/.keycloak-creds/testusers

# Verify at least one user was created
USER_COUNT=$(${KCADM} get users -r ${REALM} --fields username --format csv --noquotes --config=${KCADM_CONFIG} | grep -c "` + defaultTestUser + `" || true)
if [ "${USER_COUNT}" -eq 0 ]; then
  echo "ERROR: No test users were created in Keycloak"
  exit 1
fi

echo "Keycloak setup complete"
`

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      keycloakSetupCMName,
			Namespace: keycloakNamespace,
		},
		Data: map[string]string{
			"cli-client.json":              cliClientJSON,
			"console-client.json":          consoleClientJSON,
			"group-mapper.json":            groupMapperJSON,
			"cli-audience-mapper.json":     cliAudienceMapperJSON,
			"console-audience-mapper.json": consoleAudienceMapperJSON,
			"setup.sh":                     setupScript,
		},
	}
}

func buildCredentialsSecret(testUsers string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      keycloakCredsSecret,
			Namespace: keycloakNamespace,
		},
		StringData: map[string]string{
			"testusers": testUsers,
		},
	}
}

func buildKeycloakService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      keycloakServiceName,
			Namespace: keycloakNamespace,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone,
			Selector: map[string]string{
				"app": "keycloak",
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       keycloakContainerPort,
					TargetPort: intstr.FromInt32(keycloakContainerPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

func buildKeycloakStatefulSet(adminUser, adminPass string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      keycloakStatefulSet,
			Namespace: keycloakNamespace,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    ptr.To[int32](1),
			ServiceName: keycloakServiceName,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "keycloak",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "keycloak",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "keycloak",
							Image: keycloakImage,
							Args:  []string{"start-dev"},
							Env: []corev1.EnvVar{
								{
									Name:  "KEYCLOAK_ADMIN",
									Value: adminUser,
								},
								{
									Name:  "KEYCLOAK_ADMIN_PASSWORD",
									Value: adminPass,
								},
								{
									Name:  "KC_PROXY_HEADERS",
									Value: "xforwarded",
								},
								{
									Name:  "KC_HOSTNAME_STRICT",
									Value: "false",
								},
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: keycloakContainerPort,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/realms/master",
										Port: intstr.FromInt32(keycloakContainerPort),
									},
								},
								InitialDelaySeconds: 30,
								PeriodSeconds:       10,
								TimeoutSeconds:      5,
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "keycloak-setup",
									MountPath: "/tmp/.keycloak",
								},
								{
									Name:      "keycloak-creds",
									MountPath: "/tmp/.keycloak-creds",
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "keycloak-setup",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: keycloakSetupCMName,
									},
									DefaultMode: ptr.To[int32](0755),
								},
							},
						},
						{
							Name: "keycloak-creds",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: keycloakCredsSecret,
								},
							},
						},
					},
				},
			},
		},
	}
}

func createKeycloakRoute(ctx context.Context, client crclient.Client) error {
	route := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      keycloakRouteName,
			Namespace: keycloakNamespace,
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: keycloakServiceName,
			},
			Port: &routev1.RoutePort{
				TargetPort: intstr.FromInt32(keycloakContainerPort),
			},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationEdge,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect,
			},
		},
	}
	return CreateOrUpdate(ctx, client, route)
}

func waitForKeycloakReady(ctx context.Context, client crclient.Client, restConfig *rest.Config) error {
	if err := wait.PollUntilContextTimeout(ctx, 15*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		sts := &appsv1.StatefulSet{}
		if err := client.Get(ctx, crclient.ObjectKey{
			Namespace: keycloakNamespace,
			Name:      keycloakStatefulSet,
		}, sts); err != nil {
			return false, nil //nolint:nilerr // retry until statefulset is available
		}
		if sts.Status.ReadyReplicas < 1 {
			return false, nil
		}
		if sts.Status.UpdateRevision != "" && sts.Status.CurrentRevision != sts.Status.UpdateRevision {
			return false, nil
		}
		return true, nil
	}); err != nil {
		return err
	}

	log.Println("Keycloak pod is ready, running setup script via exec...")
	output, err := execInKeycloakPod(ctx, restConfig, "bash /tmp/.keycloak/setup.sh")
	if err != nil {
		return fmt.Errorf("keycloak setup script failed: %w (output: %s)", err, output)
	}
	log.Printf("Keycloak setup completed: %s", output)
	return nil
}

func getKeycloakIssuerURL(ctx context.Context, client crclient.Client) (string, error) {
	var host string
	if err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		route := &routev1.Route{}
		if err := client.Get(ctx, crclient.ObjectKey{
			Namespace: keycloakNamespace,
			Name:      keycloakRouteName,
		}, route); err != nil {
			return false, err
		}
		host = route.Spec.Host
		if host == "" {
			for _, ingress := range route.Status.Ingress {
				if ingress.Host != "" {
					host = ingress.Host
					break
				}
			}
		}
		return host != "", nil
	}); err != nil {
		return "", fmt.Errorf("waiting for keycloak route host: %w", err)
	}

	issuer := &url.URL{
		Scheme: "https",
		Host:   host,
		Path:   fmt.Sprintf("/realms/%s", defaultRealm),
	}
	return issuer.String(), nil
}

func extractRouterCA(ctx context.Context, client crclient.Client) ([]byte, error) {
	cm := &corev1.ConfigMap{}
	if err := client.Get(ctx, crclient.ObjectKey{
		Namespace: "openshift-config-managed",
		Name:      "default-ingress-cert",
	}, cm); err != nil {
		return nil, fmt.Errorf("failed to get default-ingress-cert configmap: %w", err)
	}

	caData, ok := cm.Data["ca-bundle.crt"]
	if !ok {
		return nil, fmt.Errorf("ca-bundle.crt not found in default-ingress-cert configmap")
	}

	return []byte(caData), nil
}

func verifyOIDCEndpoint(ctx context.Context, issuerURL string, caBundle []byte) error {
	discoveryURL := issuerURL + "/.well-known/openid-configuration"

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caBundle) {
		return fmt.Errorf("failed to parse CA bundle for OIDC endpoint verification")
	}
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    certPool,
				MinVersion: tls.VersionTLS12,
			},
		},
		Timeout: 30 * time.Second,
	}

	return wait.PollUntilContextTimeout(ctx, 10*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
		if err != nil {
			return false, fmt.Errorf("failed to create request: %w", err)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			return false, nil //nolint:nilerr // retry until endpoint is reachable
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return true, nil
		}
		return false, nil
	})
}

// execInKeycloakPod executes a bash script inside the Keycloak pod and returns
// the combined stdout/stderr output.
func execInKeycloakPod(ctx context.Context, restConfig *rest.Config, script string) (string, error) {
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return "", fmt.Errorf("creating clientset: %w", err)
	}

	podName := keycloakStatefulSet + "-0"
	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(keycloakNamespace).
		SubResource("exec").
		Param("container", "keycloak").
		Param("command", "/bin/bash").
		Param("command", "-c").
		Param("command", script).
		Param("stdout", "true").
		Param("stderr", "true")

	executor, err := remotecommand.NewSPDYExecutor(restConfig, "POST", req.URL())
	if err != nil {
		return "", fmt.Errorf("creating executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	if err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	}); err != nil {
		return stdout.String() + stderr.String(), fmt.Errorf("exec failed: %w", err)
	}
	return strings.TrimSpace(stdout.String() + stderr.String()), nil
}

// CreateOrUpdate creates a resource or updates it if it already exists.
func CreateOrUpdate(ctx context.Context, client crclient.Client, obj crclient.Object) error {
	if err := client.Create(ctx, obj); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		existing := obj.DeepCopyObject().(crclient.Object)
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(obj), existing); err != nil {
			return fmt.Errorf("failed to get existing resource for update: %w", err)
		}
		obj.SetResourceVersion(existing.GetResourceVersion())
		if err := client.Update(ctx, obj); err != nil {
			return fmt.Errorf("failed to update existing resource: %w", err)
		}
	}
	return nil
}

// generateRandomString returns a random alphanumeric string of length n using crypto/rand.
func generateRandomString(n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, n)
	for i := range result {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			// crypto/rand should never fail; if it does, fall back to a simple pattern
			result[i] = charset[i%len(charset)]
			continue
		}
		result[i] = charset[idx.Int64()]
	}
	return string(result)
}
