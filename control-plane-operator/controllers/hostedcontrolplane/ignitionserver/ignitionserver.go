package ignitionserver

import (
	"context"
	"fmt"
	"net"
	"strings"

	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ingress"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilpointer "k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ReconcileIgnitionServer(ctx context.Context,
	c client.Client,
	createOrUpdate upsert.CreateOrUpdateFN,
	utilitiesImage string,
	hcp *hyperv1.HostedControlPlane,
	defaultIngressDomain string,
	hasHealthzHandler bool,
	registryOverrides map[string]string,
	managementClusterHasCapabilitySecurityContextConstraint bool,
) error {
	log := ctrl.LoggerFrom(ctx)

	controlPlaneNamespace := hcp.Namespace
	serviceStrategy := servicePublishingStrategyByType(hcp, hyperv1.Ignition)
	if serviceStrategy == nil {
		//lint:ignore ST1005 Ignition is proper name
		return fmt.Errorf("Ignition service strategy not specified")
	}
	// Reconcile service
	ignitionServerService := ignitionserver.Service(controlPlaneNamespace)
	if _, err := createOrUpdate(ctx, c, ignitionServerService, func() error {
		return reconcileIgnitionServerService(ignitionServerService, serviceStrategy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile ignition service: %w", err)
	}
	var ignitionServerAddress string
	switch serviceStrategy.Type {
	case hyperv1.Route:
		// Reconcile route
		ignitionServerRoute := ignitionserver.Route(controlPlaneNamespace)
		if _, err := createOrUpdate(ctx, c, ignitionServerRoute, func() error {
			// The route host is considered immutable, so set it only once upon creation
			// and ignore updates.
			if ignitionServerRoute.CreationTimestamp.IsZero() {
				switch {
				case !util.ConnectsThroughInternetToControlplane(hcp.Spec.Platform):
					ignitionServerRoute.Spec.Host = fmt.Sprintf("%s.apps.%s.hypershift.local", ignitionServerRoute.Name, hcp.Name)
					ingress.AddRouteLabel(ignitionServerRoute)
				case serviceStrategy.Route != nil && serviceStrategy.Route.Hostname != "":
					ignitionServerRoute.Spec.Host = serviceStrategy.Route.Hostname
					ingress.AddRouteLabel(ignitionServerRoute)
				default:
					ignitionServerRoute.Spec.Host = util.ShortenRouteHostnameIfNeeded(ignitionServerRoute.Name, ignitionServerRoute.Namespace, defaultIngressDomain)
				}
			}

			if ignitionServerRoute.Annotations == nil {
				ignitionServerRoute.Annotations = map[string]string{}
			}
			if serviceStrategy.Route != nil && serviceStrategy.Route.Hostname != "" {
				ignitionServerRoute.ObjectMeta.Annotations[hyperv1.ExternalDNSHostnameAnnotation] = serviceStrategy.Route.Hostname
			}
			ignitionServerRoute.Spec.TLS = &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationPassthrough,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
			}
			ignitionServerRoute.Spec.To = routev1.RouteTargetReference{
				Kind:   "Service",
				Name:   ignitionserver.ResourceName,
				Weight: utilpointer.Int32Ptr(100),
			}
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile ignition route: %w", err)
		}

		// The route must be admitted and assigned a host before we can generate certs
		if len(ignitionServerRoute.Status.Ingress) == 0 || len(ignitionServerRoute.Status.Ingress[0].Host) == 0 {
			log.Info("ignition server reconciliation waiting for ignition server route to be assigned a host value")
			return nil
		}
		ignitionServerAddress = ignitionServerRoute.Status.Ingress[0].Host
	case hyperv1.NodePort:
		if serviceStrategy.NodePort == nil {
			return fmt.Errorf("nodeport metadata not specified for ignition service")
		}
		ignitionServerAddress = serviceStrategy.NodePort.Address
	default:
		return fmt.Errorf("unknown service strategy type for ignition service: %s", serviceStrategy.Type)
	}

	// Reconcile a root CA for ignition serving certificates. We only create this
	// and don't update it for now.
	caCertSecret := ignitionserver.IgnitionCACertSecret(controlPlaneNamespace)
	if result, err := createOrUpdate(ctx, c, caCertSecret, func() error {
		caCertSecret.Type = corev1.SecretTypeTLS
		return certs.ReconcileSelfSignedCA(caCertSecret, "ignition-root-ca", "openshift", func(o *certs.CAOpts) {
			o.CASignerCertMapKey = corev1.TLSCertKey
			o.CASignerKeyMapKey = corev1.TLSPrivateKeyKey
		})
	}); err != nil {
		return fmt.Errorf("failed to reconcile ignition ca cert: %w", err)
	} else {
		log.Info("reconciled ignition CA cert secret", "result", result)
	}

	// Reconcile a ignition serving certificate issued by the generated root CA. We
	// only create this and don't update it for now.
	servingCertSecret := ignitionserver.IgnitionServingCertSecret(controlPlaneNamespace)
	if result, err := createOrUpdate(ctx, c, servingCertSecret, func() error {
		servingCertSecret.Type = corev1.SecretTypeTLS

		var dnsNames, ipAddresses []string
		numericIP := net.ParseIP(ignitionServerAddress)
		if numericIP == nil {
			dnsNames = []string{ignitionServerAddress}
		} else {
			ipAddresses = []string{ignitionServerAddress}
		}

		return certs.ReconcileSignedCert(
			servingCertSecret,
			caCertSecret,
			"ignition-server",
			[]string{"openshift"},
			nil,
			corev1.TLSCertKey,
			corev1.TLSPrivateKeyKey,
			"",
			dnsNames,
			ipAddresses,
			func(o *certs.CAOpts) {
				o.CASignerCertMapKey = corev1.TLSCertKey
				o.CASignerKeyMapKey = corev1.TLSPrivateKeyKey
			})
	}); err != nil {
		return fmt.Errorf("failed to reconcile ignition serving cert: %w", err)
	} else {
		log.Info("reconciled ignition serving cert secret", "result", result)
	}

	role := ignitionserver.Role(controlPlaneNamespace)
	if result, err := createOrUpdate(ctx, c, role, func() error {
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{
					"events",
					// This is needed by the tokeSecret controller to watch secrets.
					"secrets",
					// This is needed by the MCS ignitionProvider to lookup the release image and create the MCS.
					"pods/log",
					"serviceaccounts",
					"pods",
					// This is needed by the MCS ignitionProvider to create an ephemeral ConfigMap
					// with the machine config to mount it into the MCS Pod that generates the final payload.
					"configmaps",
				},
				Verbs: []string{"*"},
			},
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile ignition role: %w", err)
	} else {
		log.Info("Reconciled ignition role", "result", result)
	}

	sa := ignitionserver.ServiceAccount(controlPlaneNamespace)
	if result, err := createOrUpdate(ctx, c, sa, func() error {
		util.EnsurePullSecret(sa, controlplaneoperator.PullSecret("").Name)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator service account: %w", err)
	} else {
		log.Info("Reconciled ignition server service account", "result", result)
	}

	roleBinding := ignitionserver.RoleBinding(controlPlaneNamespace)
	if result, err := createOrUpdate(ctx, c, roleBinding, func() error {
		roleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     role.Name,
		}

		roleBinding.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      sa.Name,
				Namespace: sa.Namespace,
			},
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile ignition RoleBinding: %w", err)
	} else {
		log.Info("Reconciled ignition server rolebinding", "result", result)
	}

	var probeHandler corev1.ProbeHandler
	if hasHealthzHandler {
		probeHandler.HTTPGet = &corev1.HTTPGetAction{
			Path:   "/healthz",
			Port:   intstr.FromInt(9090),
			Scheme: corev1.URISchemeHTTPS,
		}
	} else {
		probeHandler.TCPSocket = &corev1.TCPSocketAction{
			Port: intstr.FromInt(9090),
		}
	}

	// Reconcile deployment
	ignitionServerDeployment := ignitionserver.Deployment(controlPlaneNamespace)
	if result, err := createOrUpdate(ctx, c, ignitionServerDeployment, func() error {
		if ignitionServerDeployment.Annotations == nil {
			ignitionServerDeployment.Annotations = map[string]string{}
		}
		ignitionServerLabels := map[string]string{
			"app":                         ignitionserver.ResourceName,
			hyperv1.ControlPlaneComponent: ignitionserver.ResourceName,
		}
		ignitionServerDeployment.Spec = appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: ignitionServerLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ignitionServerLabels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:            sa.Name,
					TerminationGracePeriodSeconds: utilpointer.Int64Ptr(10),
					Tolerations: []corev1.Toleration{
						{
							Key:    "node-role.kubernetes.io/master",
							Effect: corev1.TaintEffectNoSchedule,
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "serving-cert",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: servingCertSecret.Name,
								},
							},
						},
						{
							Name: "payloads",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:            ignitionserver.ResourceName,
							Image:           utilitiesImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env: []corev1.EnvVar{
								{
									Name: "MY_NAMESPACE",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.namespace",
										},
									},
								},
							},
							Command: []string{
								"/usr/bin/control-plane-operator",
								"ignition-server",
								"--cert-file", "/var/run/secrets/ignition/serving-cert/tls.crt",
								"--key-file", "/var/run/secrets/ignition/serving-cert/tls.key",
								"--registry-overrides", convertRegistryOverridesToCommandLineFlag(registryOverrides),
								"--platform", string(hcp.Spec.Platform.Type),
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler:        probeHandler,
								InitialDelaySeconds: 120,
								TimeoutSeconds:      5,
								PeriodSeconds:       60,
								FailureThreshold:    6,
								SuccessThreshold:    1,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler:        probeHandler,
								InitialDelaySeconds: 5,
								TimeoutSeconds:      5,
								PeriodSeconds:       60,
								FailureThreshold:    3,
								SuccessThreshold:    1,
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "https",
									ContainerPort: 9090,
								},
								{
									Name:          "metrics",
									ContainerPort: 8080,
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("40Mi"),
									corev1.ResourceCPU:    resource.MustParse("10m"),
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "serving-cert",
									MountPath: "/var/run/secrets/ignition/serving-cert",
								},
								{
									Name:      "payloads",
									MountPath: "/payloads",
								},
							},
						},
					},
				},
			},
		}
		proxy.SetEnvVars(&ignitionServerDeployment.Spec.Template.Spec.Containers[0].Env)

		if hcp.Spec.AdditionalTrustBundle != nil {
			// Add trusted-ca mount with optional configmap
			util.DeploymentAddTrustBundleVolume(hcp.Spec.AdditionalTrustBundle, ignitionServerDeployment)
		}

		// set security context
		if !managementClusterHasCapabilitySecurityContextConstraint {
			ignitionServerDeployment.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
				RunAsUser: utilpointer.Int64Ptr(config.DefaultSecurityContextUser),
			}
		}

		deploymentConfig := config.DeploymentConfig{}
		deploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
		deploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
		deploymentConfig.SetDefaults(hcp, ignitionServerLabels, nil)
		deploymentConfig.ApplyTo(ignitionServerDeployment)

		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile ignition deployment: %w", err)
	} else {
		log.Info("Reconciled ignition server deployment", "result", result)
	}

	// Reconcile PodMonitor
	podMonitor := ignitionserver.PodMonitor(controlPlaneNamespace)
	if result, err := createOrUpdate(ctx, c, podMonitor, func() error {
		podMonitor.Spec.Selector = *ignitionServerDeployment.Spec.Selector
		podMonitor.Spec.PodMetricsEndpoints = []prometheusoperatorv1.PodMetricsEndpoint{{
			Interval: "15s",
			Port:     "metrics",
		}}
		podMonitor.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{MatchNames: []string{controlPlaneNamespace}}
		podMonitor.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion: hyperv1.GroupVersion.String(),
			Kind:       "HostedControlPlane",
			Name:       hcp.Name,
			UID:        hcp.UID,
		}})
		if podMonitor.Annotations == nil {
			podMonitor.Annotations = map[string]string{}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile ignition server pod monitor: %w", err)
	} else {
		log.Info("Reconciled ignition server podmonitor", "result", result)
	}

	return nil
}

func servicePublishingStrategyByType(hcp *hyperv1.HostedControlPlane, svcType hyperv1.ServiceType) *hyperv1.ServicePublishingStrategy {
	for _, mapping := range hcp.Spec.Services {
		if mapping.Service == svcType {
			return &mapping.ServicePublishingStrategy
		}
	}
	return nil
}

func reconcileIgnitionServerService(svc *corev1.Service, strategy *hyperv1.ServicePublishingStrategy) error {
	svc.Spec.Selector = map[string]string{
		"app": ignitionserver.ResourceName,
	}
	var portSpec corev1.ServicePort
	if len(svc.Spec.Ports) > 0 {
		portSpec = svc.Spec.Ports[0]
	} else {
		svc.Spec.Ports = []corev1.ServicePort{portSpec}
	}
	portSpec.Port = int32(443)
	portSpec.Name = "https"
	portSpec.Protocol = corev1.ProtocolTCP
	portSpec.TargetPort = intstr.FromInt(9090)
	switch strategy.Type {
	case hyperv1.NodePort:
		svc.Spec.Type = corev1.ServiceTypeNodePort
		if portSpec.NodePort == 0 && strategy.NodePort != nil {
			portSpec.NodePort = strategy.NodePort.Port
		}
	case hyperv1.Route:
		svc.Spec.Type = corev1.ServiceTypeClusterIP
	default:
		return fmt.Errorf("invalid publishing strategy for Ignition service: %s", strategy.Type)
	}
	svc.Spec.Ports[0] = portSpec
	return nil
}

func convertRegistryOverridesToCommandLineFlag(registryOverrides map[string]string) string {
	commandLineFlagArray := []string{}
	for registrySource, registryReplacement := range registryOverrides {
		commandLineFlagArray = append(commandLineFlagArray, fmt.Sprintf("%s=%s", registrySource, registryReplacement))
	}
	if len(commandLineFlagArray) > 0 {
		return strings.Join(commandLineFlagArray, ",")
	}
	// this is the equivalent of null on a StringToString command line variable.
	return "="
}
