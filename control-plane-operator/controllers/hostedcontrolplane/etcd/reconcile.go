package etcd

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
	etcdv1 "github.com/openshift/hypershift/thirdparty/etcd/v1beta2"
)

func (p *EtcdParams) ReconcileOperatorServiceAccount(sa *corev1.ServiceAccount) error {
	util.EnsureOwnerRef(sa, p.OwnerReference)
	return nil
}

func (p *EtcdParams) ReconcileOperatorRole(role *rbacv1.Role) error {
	util.EnsureOwnerRef(role, p.OwnerReference)
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{
				etcdv1.SchemeGroupVersion.Group,
			},
			Resources: []string{
				"etcdclusters",
				"etcdbackups",
				"etcdrestores",
			},
			Verbs: []string{
				"*",
			},
		},
		{
			APIGroups: []string{
				corev1.SchemeGroupVersion.Group,
			},
			Resources: []string{
				"pods",
				"services",
				"endpoints",
				"persistentvolumeclaims",
				"events",
			},
			Verbs: []string{
				"*",
			},
		},
		{
			APIGroups: []string{
				appsv1.SchemeGroupVersion.Group,
			},
			Resources: []string{
				"deployments",
			},
			Verbs: []string{
				"*",
			},
		},
		{
			APIGroups: []string{
				corev1.SchemeGroupVersion.Group,
			},
			Resources: []string{
				"secrets",
			},
			Verbs: []string{
				"get",
			},
		},
	}
	return nil
}

func (p *EtcdParams) ReconcileOperatorRoleBinding(roleBinding *rbacv1.RoleBinding) error {
	util.EnsureOwnerRef(roleBinding, p.OwnerReference)
	serviceAccount := manifests.EtcdOperatorServiceAccount(roleBinding.Namespace)
	roleBinding.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "Role",
		Name:     "etcd-operator",
	}
	roleBinding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			APIGroup:  corev1.SchemeGroupVersion.Group,
			Namespace: serviceAccount.Namespace,
			Name:      serviceAccount.Name,
		},
	}
	return nil
}

var etcdOperatorDeploymentLabels = map[string]string{
	"name": "etcd-operator",
}

func etcdOperatorContainer() *corev1.Container {
	return &corev1.Container{
		Name: "etcd-operator",
	}
}

// etcdContainer is not a container that we build directly but is used
// to assign scheduling/resources to it
func etcdContainer() *corev1.Container {
	return &corev1.Container{
		Name: "etcd",
	}
}

func (p *EtcdParams) buildEtcdOperatorContainer(c *corev1.Container) {
	c.Image = p.EtcdOperatorImage
	c.Command = []string{"etcd-operator"}
	c.Args = []string{"-create-crd=false"}
	c.Env = []corev1.EnvVar{
		{
			Name: "MY_POD_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.namespace",
				},
			},
		},
		{
			Name: "MY_POD_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
	}
}

func (p *EtcdParams) ReconcileOperatorDeployment(deployment *appsv1.Deployment) error {
	util.EnsureOwnerRef(deployment, p.OwnerReference)
	serviceAccount := manifests.EtcdOperatorServiceAccount(deployment.Namespace)
	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: pointer.Int32Ptr(1),
		Selector: &metav1.LabelSelector{
			MatchLabels: etcdOperatorDeploymentLabels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: etcdOperatorDeploymentLabels,
			},
			Spec: corev1.PodSpec{
				ServiceAccountName: serviceAccount.Name,
				Containers: []corev1.Container{
					util.BuildContainer(etcdOperatorContainer(), p.buildEtcdOperatorContainer),
				},
			},
		},
	}
	p.Resources.ApplyTo(&deployment.Spec.Template.Spec)
	p.OperatorScheduling.ApplyTo(&deployment.Spec.Template.Spec)
	p.SecurityContexts.ApplyTo(&deployment.Spec.Template.Spec)
	p.ReadinessProbes.ApplyTo(&deployment.Spec.Template.Spec)
	p.LivenessProbes.ApplyTo(&deployment.Spec.Template.Spec)
	p.AdditionalLabels.ApplyTo(&deployment.Spec.Template.ObjectMeta)
	return nil
}

func (p *EtcdParams) ReconcileCluster(cluster *etcdv1.EtcdCluster) error {
	util.EnsureOwnerRef(cluster, p.OwnerReference)
	peerSecret := manifests.EtcdPeerSecret(cluster.Namespace)
	serverSecret := manifests.EtcdServerSecret(cluster.Namespace)
	clientSecret := manifests.EtcdClientSecret(cluster.Namespace)

	podPolicy := &etcdv1.PodPolicy{}

	if resources, ok := p.Resources[etcdContainer().Name]; ok {
		podPolicy.Resources = resources
	}
	podPolicy.Affinity = p.EtcdScheduling.Affinity
	podPolicy.Tolerations = p.EtcdScheduling.Tolerations
	// TODO: Figure out how to set priority class on etcd pods
	// podPolicy.PriorityClass = p.EtcdScheduling.PriorityClass
	if sc, ok := p.SecurityContexts[etcdContainer().Name]; ok {
		podPolicy.SecurityContext = &corev1.PodSecurityContext{
			SELinuxOptions: sc.SELinuxOptions,
			WindowsOptions: sc.WindowsOptions,
			RunAsUser:      sc.RunAsUser,
			RunAsGroup:     sc.RunAsGroup,
			RunAsNonRoot:   sc.RunAsNonRoot,
			SeccompProfile: sc.SeccompProfile,
		}
	}
	podPolicy.PersistentVolumeClaimSpec = p.PVCClaim
	podPolicy.Labels = p.AdditionalLabels

	cluster.Spec = etcdv1.ClusterSpec{
		Size:    p.ClusterSize,
		Version: p.ClusterVersion,
		TLS: &etcdv1.TLSPolicy{
			Static: &etcdv1.StaticTLS{
				Member: &etcdv1.MemberSecret{
					PeerSecret:   peerSecret.Name,
					ServerSecret: serverSecret.Name,
				},
				OperatorSecret: clientSecret.Name,
			},
		},
		Pod: podPolicy,
	}
	return nil
}
