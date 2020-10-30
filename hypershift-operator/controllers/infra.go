package controllers

import (
	"context"
	"fmt"

	routev1 "github.com/openshift/api/route/v1"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"

	operatorv1 "github.com/openshift/api/operator/v1"
	securityv1 "github.com/openshift/api/security/v1"

	hyperv1 "openshift.io/hypershift/api/v1alpha1"
)

const (
	kubeAPIServerServiceName = "kube-apiserver"
	oauthServiceName         = "oauth-openshift"
	vpnServiceName           = "openvpn-server"
	ingressOperatorNamespace = "openshift-ingress-operator"
	hypershiftRouteLabel     = "hypershift.openshift.io/cluster"
	vpnServiceAccountName    = "vpn"
)

type InfrastructureStatus struct {
	APIAddress              string
	OAuthAddress            string
	VPNAddress              string
	OpenShiftAPIAddress     string
	IgnitionProviderAddress string
}

func (s InfrastructureStatus) IsReady() bool {
	return len(s.APIAddress) > 0 &&
		len(s.OAuthAddress) > 0 &&
		len(s.VPNAddress) > 0 &&
		len(s.IgnitionProviderAddress) > 0
}

func (r *OpenShiftClusterReconciler) ensureInfrastructure(ctx context.Context, cluster *hyperv1.OpenShiftCluster) (InfrastructureStatus, error) {
	status := InfrastructureStatus{}

	name := cluster.Name
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	// Start creating resources on management cluster
	err := r.Create(ctx, ns)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return status, fmt.Errorf("failed to create target namespace %q: %w", ns.Name, err)
	}

	// Ensure that we can run privileged pods
	if err := ensureVPNSCC(r, name); err != nil {
		return status, fmt.Errorf("failed to ensure privileged SCC for the new namespace: %w", err)
	}

	// Create pull secret
	r.Log.Info("Creating pull secret")
	if _, err := createPullSecret(r, name, cluster.Spec.PullSecret); err != nil {
		return status, fmt.Errorf("failed to create pull secret: %w", err)
	}

	// Create Kube APIServer service
	r.Log.Info("Creating Kube API service")
	apiService, err := createKubeAPIServerService(r, name)
	if err != nil {
		return status, fmt.Errorf("failed to create Kube API service: %w", err)
	}
	r.Log.Info("Created Kube API service")

	r.Log.Info("Creating VPN service")
	vpnService, err := createVPNServerService(r, name)
	if err != nil {
		return status, fmt.Errorf("failed to create vpn server service: %w", err)
	}
	r.Log.Info("Created VPN service")

	r.Log.Info("Creating Openshift API service")
	openshiftAPIService, err := createOpenshiftService(r, name)
	if err != nil {
		return status, fmt.Errorf("failed to create openshift server service: %w", err)
	}
	r.Log.Info("Created Openshift API service")

	r.Log.Info("Creating OAuth service")
	oauthService, err := createOauthService(r, name)
	if err != nil {
		return status, fmt.Errorf("error creating service for oauth: %w", err)
	}

	r.Log.Info("Creating router shard")
	if err := createIngressController(r, name, cluster.Spec.BaseDomain); err != nil {
		return status, fmt.Errorf("cannot create router shard: %w", err)
	}

	r.Log.Info("Creating ignition provider route")
	ignitionRoute := createIgnitionServerRoute(r, ctx, name)
	if err := r.Create(ctx, ignitionRoute); err != nil && !apierrors.IsAlreadyExists(err) {
		return status, fmt.Errorf("failed to create ignition route: %w", err)
	}

	apiAddress, err := getLoadBalancerServiceAddress(r, ctx, ctrl.ObjectKeyFromObject(apiService))
	if err != nil {
		return status, fmt.Errorf("failed to get service: %w", err)
	}
	status.APIAddress = apiAddress

	oauthAddress, err := getLoadBalancerServiceAddress(r, ctx, ctrl.ObjectKeyFromObject(oauthService))
	if err != nil {
		return status, fmt.Errorf("failed to get service: %w", err)
	}
	status.OAuthAddress = oauthAddress

	vpnAddress, err := getLoadBalancerServiceAddress(r, ctx, ctrl.ObjectKeyFromObject(vpnService))
	if err != nil {
		return status, fmt.Errorf("failed to get service: %w", err)
	}
	status.VPNAddress = vpnAddress

	ignitionAddress, err := getRouteAddress(r, ctx, ctrl.ObjectKeyFromObject(ignitionRoute))
	if err != nil {
		return status, fmt.Errorf("failed get get route address: %w", err)
	}
	status.IgnitionProviderAddress = ignitionAddress

	status.OpenShiftAPIAddress = openshiftAPIService.Spec.ClusterIP

	return status, nil
}

func createKubeAPIServerService(client ctrl.Client, namespace string) (*corev1.Service, error) {
	svc := &corev1.Service{}
	svc.Namespace = namespace
	svc.Name = kubeAPIServerServiceName
	svc.Spec.Selector = map[string]string{"app": "kube-apiserver"}
	svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Port:       6443,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(6443),
		},
	}
	if err := client.Create(context.TODO(), svc); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("failed to create api server service: %w", err)
		}
	}
	return svc, nil
}

func createVPNServerService(client ctrl.Client, namespace string) (*corev1.Service, error) {
	svc := &corev1.Service{}
	svc.Namespace = namespace
	svc.Name = vpnServiceName
	svc.Spec.Selector = map[string]string{"app": "openvpn-server"}
	svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Port:       1194,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(1194),
		},
	}
	if err := client.Create(context.TODO(), svc); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("failed to create vpn server service: %w", err)
		}
	}
	return svc, nil
}

func createOpenshiftService(client ctrl.Client, namespace string) (*corev1.Service, error) {
	svc := &corev1.Service{}
	svc.Namespace = namespace
	svc.Name = "openshift-apiserver"
	svc.Spec.Selector = map[string]string{"app": "openshift-apiserver"}
	svc.Spec.Type = corev1.ServiceTypeClusterIP
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Name:       "https",
			Port:       443,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(8443),
		},
	}
	if err := client.Create(context.TODO(), svc); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("failed to create openshift service: %w", err)
		}
	}
	return svc, nil
}

func createOauthService(client ctrl.Client, namespace string) (*corev1.Service, error) {
	svc := &corev1.Service{}
	svc.Namespace = namespace
	svc.Name = oauthServiceName
	svc.Spec.Selector = map[string]string{"app": "oauth-openshift"}
	svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Name:       "https",
			Port:       8443,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(6443),
		},
	}
	err := client.Create(context.TODO(), svc)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("failed to create oauth service: %w", err)
	}
	return svc, nil
}

func createPullSecret(client ctrl.Client, namespace, data string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	secret.Namespace = namespace
	secret.Name = "pull-secret"
	secret.Data = map[string][]byte{".dockerconfigjson": []byte(data)}
	secret.Type = corev1.SecretTypeDockerConfigJson
	if err := client.Create(context.TODO(), secret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("failed to create pull secret: %w", err)
		}
	}
	return secret, retry.RetryOnConflict(retry.DefaultRetry, func() error {
		sa := &corev1.ServiceAccount{}
		if err := client.Get(context.TODO(), ctrl.ObjectKey{Namespace: namespace, Name: "default"}, sa); err != nil {
			return err
		}
		sa.ImagePullSecrets = append(sa.ImagePullSecrets, corev1.LocalObjectReference{Name: "pull-secret"})
		if err := client.Update(context.TODO(), sa); err != nil {
			return err
		}
		return nil
	})
}

func ensureVPNSCC(client ctrl.Client, namespace string) error {
	scc := &securityv1.SecurityContextConstraints{}
	if err := client.Get(context.TODO(), ctrl.ObjectKey{Name: "privileged"}, scc); err != nil {
		return fmt.Errorf("failed to get privileged scc: %w", err)
	}
	userSet := sets.NewString(scc.Users...)
	svcAccount := fmt.Sprintf("system:serviceaccount:%s:%s", namespace, vpnServiceAccountName)
	if userSet.Has(svcAccount) {
		return nil
	}
	userSet.Insert(svcAccount)
	scc.Users = userSet.List()
	if err := client.Update(context.TODO(), scc); err != nil {
		return fmt.Errorf("failed to update privileged scc: %w", err)
	}
	return nil
}

func createIngressController(client ctrl.Client, name string, parentDomain string) error {
	// First ensure that the default ingress controller doesn't use routes generated for hypershift clusters
	err := ensureDefaultIngressControllerSelector(client)
	if err != nil {
		return err
	}
	ic := &operatorv1.IngressController{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ingressOperatorNamespace,
		},
		Spec: operatorv1.IngressControllerSpec{
			Domain: fmt.Sprintf("apps.%s", parentDomain),
			RouteSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					hypershiftRouteLabel: name,
				},
			},
		},
	}
	if err := client.Create(context.TODO(), ic); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create ingress controller for %s: %w", name, err)
	}
	return nil
}

func ensureDefaultIngressControllerSelector(client ctrl.Client) error {
	defaultIC := &operatorv1.IngressController{}
	if err := client.Get(context.TODO(), ctrl.ObjectKey{Namespace: ingressOperatorNamespace, Name: "default"}, defaultIC); err != nil {
		return fmt.Errorf("failed to fetch default ingress controller: %w", err)
	}
	routeSelector := defaultIC.Spec.RouteSelector
	if routeSelector == nil {
		routeSelector = &metav1.LabelSelector{}
	}
	found := false
	for _, exp := range routeSelector.MatchExpressions {
		if exp.Key == hypershiftRouteLabel && exp.Operator == metav1.LabelSelectorOpDoesNotExist {
			found = true
			break
		}
	}
	if !found {
		routeSelector.MatchExpressions = append(routeSelector.MatchExpressions, metav1.LabelSelectorRequirement{
			Key:      hypershiftRouteLabel,
			Operator: metav1.LabelSelectorOpDoesNotExist,
		})
		defaultIC.Spec.RouteSelector = routeSelector
		if err := client.Update(context.TODO(), defaultIC); err != nil {
			return fmt.Errorf("failed to update default ingress controller: %w", err)
		}
	}
	return nil
}

func createIgnitionServerRoute(client ctrl.Client, ctx context.Context, namespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      "ignition-provider",
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: "machine-config-server",
			},
		},
	}
}

func getLoadBalancerServiceAddress(client ctrl.Client, ctx context.Context, key ctrl.ObjectKey) (string, error) {
	svc := &corev1.Service{}
	if err := client.Get(ctx, key, svc); err != nil {
		return "", fmt.Errorf("failed to get service: %w", err)
	}
	var addr string
	if len(svc.Status.LoadBalancer.Ingress) > 0 {
		switch {
		case svc.Status.LoadBalancer.Ingress[0].Hostname != "":
			addr = svc.Status.LoadBalancer.Ingress[0].Hostname
		case svc.Status.LoadBalancer.Ingress[0].IP != "":
			addr = svc.Status.LoadBalancer.Ingress[0].IP
		}
	}
	return addr, nil
}

func getRouteAddress(client ctrl.Client, ctx context.Context, key ctrl.ObjectKey) (string, error) {
	route := &routev1.Route{}
	if err := client.Get(ctx, key, route); err != nil {
		return "", fmt.Errorf("failed to get route: %w", err)
	}
	var addr string
	if len(route.Status.Ingress) > 0 {
		if len(route.Status.Ingress[0].Host) > 0 {
			addr = route.Status.Ingress[0].Host
		}
	}
	return addr, nil
}
