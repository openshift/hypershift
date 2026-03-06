package hostedcontrolplane

import (
	"context"
	_ "embed"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/infra"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	etcdv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/etcd"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/fg"
	ignitionserverv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/ignitionserver"
	ignitionproxyv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/ignitionserver_proxy"
	kasv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/azureutil"
	fakecapabilities "github.com/openshift/hypershift/support/capabilities/fake"
	"github.com/openshift/hypershift/support/certs"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/releaseinfo"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/releaseinfo/testutils"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/image/docker10"
	routev1 "github.com/openshift/api/route/v1"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	"github.com/go-logr/zapr"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap/zaptest"
)

type fakeEC2Client struct {
	ec2iface.EC2API
}

func (*fakeEC2Client) DescribeVpcEndpointsWithContext(aws.Context, *ec2.DescribeVpcEndpointsInput, ...request.Option) (*ec2.DescribeVpcEndpointsOutput, error) {
	return &ec2.DescribeVpcEndpointsOutput{}, fmt.Errorf("not ready")
}

func TestReconcileKubeadminPassword(t *testing.T) {
	targetNamespace := "test"

	testsCases := []struct {
		name                 string
		hcp                  *hyperv1.HostedControlPlane
		expectedOutputSecret *corev1.Secret
	}{
		{
			name: "When OAuth config specified results in no kubeadmin secret",
			hcp: &hyperv1.HostedControlPlane{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: targetNamespace,
					Name:      "cluster1",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						OAuth: &configv1.OAuthSpec{
							IdentityProviders: []configv1.IdentityProvider{
								{
									IdentityProviderConfig: configv1.IdentityProviderConfig{
										Type: configv1.IdentityProviderTypeOpenID,
										OpenID: &configv1.OpenIDIdentityProvider{
											ClientID: "clientid1",
											ClientSecret: configv1.SecretNameReference{
												Name: "clientid1-secret-name",
											},
											Issuer: "https://example.com/identity",
											Claims: configv1.OpenIDClaims{
												Email:             []string{"email"},
												Name:              []string{"clientid1-secret-name"},
												PreferredUsername: []string{"preferred_username"},
											},
										},
									},
									Name:          "IAM",
									MappingMethod: "lookup",
								},
							},
						},
					},
				},
			},
			expectedOutputSecret: nil,
		},
		{
			name: "When Oauth config not specified results in default kubeadmin secret",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: targetNamespace,
					Name:      "cluster1",
				},
			},
			expectedOutputSecret: common.KubeadminPasswordSecret(targetNamespace),
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			fakeClient := fake.NewClientBuilder().Build()
			r := &HostedControlPlaneReconciler{
				Client: fakeClient,
				Log:    ctrl.LoggerFrom(t.Context()),
			}
			err := r.reconcileKubeadminPassword(t.Context(), tc.hcp, tc.hcp.Spec.Configuration != nil && tc.hcp.Spec.Configuration.OAuth != nil, controllerutil.CreateOrUpdate)
			g.Expect(err).NotTo(HaveOccurred())

			actualSecret := common.KubeadminPasswordSecret(targetNamespace)
			err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(actualSecret), actualSecret)
			if tc.expectedOutputSecret != nil {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(actualSecret.Data).To(HaveKey("password"))
				g.Expect(actualSecret.Data["password"]).ToNot(BeEmpty())
			} else {
				if !apierrors.IsNotFound(err) {
					g.Expect(err).NotTo(HaveOccurred())
				}
			}
		})
	}
}

func TestReconcileIgnitionServer(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockedProviderWithOpenshiftImageRegistryOverrides := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(mockCtrl)
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		Lookup(gomock.Any(), gomock.Any(), gomock.Any()).Return(testutils.InitReleaseImageOrDie("4.20.0"), nil).AnyTimes()
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		GetRegistryOverrides().Return(map[string]string{"registry": "override"}).AnyTimes()
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		GetOpenShiftImageRegistryOverrides().Return(map[string][]string{"registry": {"override"}}).AnyTimes()
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().GetMirroredReleaseImage().Return("").AnyTimes()

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "hcp-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Configuration: &hyperv1.ClusterConfiguration{
				FeatureGate: &configv1.FeatureGateSpec{
					FeatureGateSelection: configv1.FeatureGateSelection{
						FeatureSet: configv1.Default,
					},
				},
			},
			Services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service: hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
						Type: hyperv1.Route,
					},
				},
			},
			Networking: hyperv1.ClusterNetworking{
				APIServer: &hyperv1.APIServerNetworking{
					Port:             ptr.To[int32](2040),
					AdvertiseAddress: ptr.To("1.2.3.4"),
				},
			},
			Etcd: hyperv1.EtcdSpec{
				ManagementType: hyperv1.Managed,
			},
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.IBMCloudPlatform,
				IBMCloud: &hyperv1.IBMCloudPlatformSpec{
					ProviderType: configv1.IBMCloudProviderTypeVPC,
				},
				GCP: &hyperv1.GCPPlatformSpec{
					Project: "my-project",
					Region:  "us-central1",
					NetworkConfig: hyperv1.GCPNetworkConfig{
						Network: hyperv1.GCPResourceReference{
							Name: "my-network",
						},
					},
				},
			},
		},
	}

	cpContext := controlplanecomponent.ControlPlaneContext{
		Context:                  t.Context(),
		ApplyProvider:            upsert.NewApplyProvider(true),
		ReleaseImageProvider:     testutil.FakeImageProvider(),
		UserReleaseImageProvider: testutil.FakeImageProvider(),
		SkipPredicate:            false,
		SkipCertificateSigning:   false,
		HCP:                      hcp,
	}

	tests := []struct {
		name        string
		annotations map[string]string
		caCert      *corev1.Secret
		servingCert *corev1.Secret
	}{
		{
			name:        "No certs, no extra annotations",
			annotations: map[string]string{},
			caCert:      nil,
			servingCert: nil,
		},
		{
			name: "Premade certs, DisablePKIReconciliation annotation present",
			annotations: map[string]string{
				hyperv1.DisablePKIReconciliationAnnotation: "true",
			},
			caCert: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ignition-server-ca-cert",
					Namespace: "hcp-namespace",
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       []byte("fake"),
					corev1.TLSPrivateKeyKey: []byte("fake"),
				},
			},
			servingCert: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ignition-server-serving-cert",
					Namespace: "hcp-namespace",
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       []byte("fake"),
					corev1.TLSPrivateKeyKey: []byte("fake"),
				},
			},
		},
		{
			name: "No certs, DisablePKIReconciliation annotation present",
			annotations: map[string]string{
				hyperv1.DisablePKIReconciliationAnnotation: "true",
			},
			caCert:      nil,
			servingCert: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hcp.ObjectMeta.Annotations = tt.annotations

			ignitionComponent := ignitionserverv2.NewComponent(mockedProviderWithOpenshiftImageRegistryOverrides, "")
			fakeObjects, err := componentsFakeObjects(hcp.Namespace, configv1.Default)
			if err != nil {
				t.Fatalf("failed to generate fake objects: %v", err)
			}
			fakeDeps := componentsFakeDependencies(ignitionComponent.Name(), hcp.Namespace)

			ignitionCerts := []client.Object{}
			if tt.caCert != nil {
				ignitionCerts = append(ignitionCerts, tt.caCert)
			}
			if tt.servingCert != nil {
				ignitionCerts = append(ignitionCerts, tt.servingCert)
			}

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).
				WithObjects(fakeObjects...).
				WithObjects(fakeDeps...).
				WithObjects(ignitionCerts...).
				Build()
			cpContext.Client = fakeClient

			if err := ignitionComponent.Reconcile(cpContext); err != nil {
				t.Fatalf("failed to reconcile: %v", err)
			}

			gotCACert := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ignition-server-ca-cert",
					Namespace: "hcp-namespace",
				},
			}
			gotServingCert := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ignition-server-serving-cert",
					Namespace: "hcp-namespace",
				},
			}

			_, hasPKIAnnotation := tt.annotations[hyperv1.DisablePKIReconciliationAnnotation]
			expectCACert := !hasPKIAnnotation || tt.caCert != nil
			expectServingCert := !hasPKIAnnotation || tt.servingCert != nil

			err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(gotCACert), gotCACert)
			if err != nil && expectCACert {
				t.Fatalf("ignition-server-ca-cert does not exist")
			} else if err == nil && !expectCACert {
				t.Fatalf("ignition-server-ca-cert exists")
			}

			err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(gotServingCert), gotServingCert)
			if err != nil && expectServingCert {
				t.Fatalf("ignition-server-serving-cert does not exist")
			} else if err == nil && !expectServingCert {
				t.Fatalf("ignition-server-serving-cert exists")
			}

			if tt.caCert != nil {
				wantKey := string(tt.caCert.Data[corev1.TLSCertKey])
				gotKey := string(gotCACert.Data[corev1.TLSCertKey])
				if wantKey != gotKey {
					t.Fatalf("ignition-server-ca-cert data mismatch: want %v, got %v", wantKey, gotKey)
				}
			}
			if tt.servingCert != nil {
				wantKey := string(tt.servingCert.Data[corev1.TLSCertKey])
				gotKey := string(gotServingCert.Data[corev1.TLSCertKey])
				if wantKey != gotKey {
					t.Fatalf("ignition-server-serving-cert data mismatch: want %v, got %v", wantKey, gotKey)
				}
			}
		})
	}
}

func TestEtcdRestoredCondition(t *testing.T) {
	testsCases := []struct {
		name              string
		sts               *appsv1.StatefulSet
		pods              []corev1.Pod
		expectedCondition metav1.Condition
	}{
		{
			name: "single replica, pod ready - condition true",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: "thens",
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](1),
				},
				Status: appsv1.StatefulSetStatus{
					Replicas:      1,
					ReadyReplicas: 1,
				},
			},
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-0",
						Namespace: "thens",
						Labels: map[string]string{
							"app": "etcd",
						},
					},
					Status: corev1.PodStatus{
						InitContainerStatuses: []corev1.ContainerStatus{
							{
								Name:  "etcd-init",
								Ready: true,
							},
						},
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:   string(hyperv1.EtcdSnapshotRestored),
				Status: metav1.ConditionTrue,
				Reason: hyperv1.AsExpectedReason,
			},
		},
		{
			name: "Pod not ready - condition false",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: "thens",
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](1),
				},
				Status: appsv1.StatefulSetStatus{
					Replicas:      1,
					ReadyReplicas: 1,
				},
			},
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-0",
						Namespace: "thens",
						Labels: map[string]string{
							"app": "etcd",
						},
					},
					Status: corev1.PodStatus{
						InitContainerStatuses: []corev1.ContainerStatus{
							{
								Name:  "etcd-init",
								Ready: false,
								LastTerminationState: corev1.ContainerState{
									Terminated: &corev1.ContainerStateTerminated{
										ExitCode: 1,
										Reason:   "somethingfailed",
									},
								},
							},
						},
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:   string(hyperv1.EtcdSnapshotRestored),
				Status: metav1.ConditionFalse,
				Reason: "somethingfailed",
			},
		},
		{
			name: "multiple replica, pods ready - condition true",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: "thens",
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.StatefulSetStatus{
					Replicas:      3,
					ReadyReplicas: 3,
				},
			},
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-0",
						Namespace: "thens",
						Labels: map[string]string{
							"app": "etcd",
						},
					},
					Status: corev1.PodStatus{
						InitContainerStatuses: []corev1.ContainerStatus{
							{
								Name:  "etcd-init",
								Ready: true,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-1",
						Namespace: "thens",
						Labels: map[string]string{
							"app": "etcd",
						},
					},
					Status: corev1.PodStatus{
						InitContainerStatuses: []corev1.ContainerStatus{
							{
								Name:  "etcd-init",
								Ready: true,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-2",
						Namespace: "thens",
						Labels: map[string]string{
							"app": "etcd",
						},
					},
					Status: corev1.PodStatus{
						InitContainerStatuses: []corev1.ContainerStatus{
							{
								Name:  "etcd-init",
								Ready: true,
							},
						},
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:   string(hyperv1.EtcdSnapshotRestored),
				Status: metav1.ConditionTrue,
				Reason: hyperv1.AsExpectedReason,
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			podList := &corev1.PodList{
				Items: tc.pods,
			}
			fakeClient := fake.NewClientBuilder().WithLists(podList).Build()
			r := &HostedControlPlaneReconciler{
				Client: fakeClient,
				Log:    ctrl.LoggerFrom(t.Context()),
			}

			conditionPtr := r.etcdRestoredCondition(t.Context(), tc.sts)
			g.Expect(conditionPtr).ToNot(BeNil())
			g.Expect(*conditionPtr).To(Equal(tc.expectedCondition))
		})
	}
}

func sampleHCP(t *testing.T) *hyperv1.HostedControlPlane {
	t.Helper()
	rawHCP := `apiVersion: hypershift.openshift.io/v1beta1
kind: HostedControlPlane
metadata:
  annotations:
    hypershift.openshift.io/cluster: cewong/cewong-dev
  finalizers:
  - hypershift.openshift.io/finalizer
  generation: 1
  labels:
    cluster.x-k8s.io/cluster-name: cewong-dev-4nvh8
  name: foo
  namespace: bar
  ownerReferences:
  - apiVersion: cluster.x-k8s.io/v1beta1
    blockOwnerDeletion: true
    controller: true
    kind: Cluster
    name: cewong-dev-4nvh8
    uid: 01657128-83b6-4ed8-8814-1d735a374d24
  resourceVersion: "216428710"
  uid: ed1353cb-758d-4c87-b302-233976f93271
spec:
  autoscaling: {}
  clusterID: 5878727a-1200-4fd5-802f-f0218b8af12c
  controllerAvailabilityPolicy: SingleReplica
  dns:
    baseDomain: hypershift.cesarwong.com
    privateZoneID: Z081271024WU1LT4DEEIV
    publicZoneID: Z0676342TNL7FZTLRDUL
  etcd:
    managed:
      storage:
        persistentVolume:
          size: 4Gi
        type: PersistentVolume
    managementType: Managed
  fips: false
  infraID: cewong-dev-4nvh8
  infrastructureAvailabilityPolicy: SingleReplica
  issuerURL: https://hypershift-ci-1-oidc.s3.us-east-1.amazonaws.com/cewong-dev-4nvh8
  machineCIDR: 10.0.0.0/16
  networkType: OVNKubernetes
  networking:
    clusterNetwork:
    - cidr: 10.132.0.0/14
    machineNetwork:
    - cidr: 10.0.0.0/16
    networkType: OVNKubernetes
    serviceNetwork:
    - cidr: 172.29.0.0/16
  olmCatalogPlacement: management
  platform:
    aws:
      cloudProviderConfig:
        subnet:
          id: subnet-099bf416521d0628a
        vpc: vpc-0d5303991a390921f
        zone: us-east-2a
      controlPlaneOperatorCreds: {}
      endpointAccess: Public
      kubeCloudControllerCreds:
        name: cloud-controller-creds
      nodePoolManagementCreds: {}
      region: us-east-2
      resourceTags:
      - key: kubernetes.io/cluster/cewong-dev-4nvh8
        value: owned
      roles:
      - arn: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-openshift-ingress
        name: cloud-credentials
        namespace: openshift-ingress-operator
      - arn: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-openshift-image-registry
        name: installer-cloud-credentials
        namespace: openshift-image-registry
      - arn: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-aws-ebs-csi-driver-controller
        name: ebs-cloud-credentials
        namespace: openshift-cluster-csi-drivers
      - arn: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-cloud-network-config-controller
        name: cloud-credentials
        namespace: openshift-cloud-network-config-controller
      rolesRef:
        controlPlaneOperatorARN: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-control-plane-operator
        imageRegistryARN: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-openshift-image-registry
        ingressARN: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-openshift-ingress
        kubeCloudControllerARN: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-cloud-controller
        networkARN: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-cloud-network-config-controller
        nodePoolManagementARN: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-node-pool
        storageARN: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-aws-ebs-csi-driver-controller
    type: AWS
  pullSecret:
    name: pull-secret
  releaseImage: quay.io/openshift-release-dev/ocp-release:4.11.0-rc.4-x86_64
  secretEncryption:
    aescbc:
      activeKey:
        name: etcd-encryption-key
    type: aescbc
  services:
  - service: APIServer
    servicePublishingStrategy:
      type: LoadBalancer
  - service: OAuthServer
    servicePublishingStrategy:
      type: Route
  - service: Konnectivity
    servicePublishingStrategy:
      type: Route
  - service: Ignition
    servicePublishingStrategy:
      type: Route
  - service: OVNSbDb
    servicePublishingStrategy:
      type: Route`
	hcp := &hyperv1.HostedControlPlane{}
	if err := yaml.Unmarshal([]byte(rawHCP), hcp); err != nil {
		t.Fatal(err)
	}

	return hcp
}

func TestEventHandling(t *testing.T) {
	t.Parallel()

	hcp := sampleHCP(t)
	pullSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "pull-secret"}}
	etcdEncryptionKey := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "etcd-encryption-key"},
		Data:       map[string][]byte{"key": []byte("very-secret")},
	}
	fakeNodeTuningOperator := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "node-tuning-operator",
			Namespace: "bar",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
		},
	}
	fakeNodeTuningOperatorTLS := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "node-tuning-operator-tls"},
		Data:       map[string][]byte{"key": []byte("very-secret")},
	}

	hcpGVK, err := apiutil.GVKForObject(hcp, api.Scheme)
	if err != nil {
		t.Fatalf("failed to determine gvk for %T: %v", hcp, err)
	}
	restMapper := meta.NewDefaultRESTMapper(nil)
	restMapper.Add(hcpGVK, meta.RESTScopeNamespace)
	c := &createTrackingClient{Client: fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(hcp, pullSecret, etcdEncryptionKey, fakeNodeTuningOperator, fakeNodeTuningOperatorTLS).
		WithStatusSubresource(&hyperv1.HostedControlPlane{}).
		WithRESTMapper(restMapper).
		Build(),
	}

	readyInfraStatus := infra.InfrastructureStatus{
		APIHost:          "foo",
		APIPort:          1,
		OAuthHost:        "foo",
		OAuthPort:        1,
		KonnectivityHost: "foo",
		KonnectivityPort: 1,
	}
	// Selftest, so this doesn't rot over time
	if !readyInfraStatus.IsReady() {
		t.Fatal("readyInfraStatus fixture is not actually ready")
	}
	mockCtrl := gomock.NewController(t)
	mockedProviderWithOpenshiftImageRegistryOverrides := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(mockCtrl)
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(testutils.InitReleaseImageOrDie("4.15.0"), nil).AnyTimes()

	r := &HostedControlPlaneReconciler{
		Client:                        c,
		ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
		ReleaseProvider:               mockedProviderWithOpenshiftImageRegistryOverrides,
		UserReleaseProvider:           &fakereleaseprovider.FakeReleaseProvider{},
		reconcileInfrastructureStatus: func(context.Context, *hyperv1.HostedControlPlane) (infra.InfrastructureStatus, error) {
			return readyInfraStatus, nil
		},
		SetDefaultSecurityContext: false,
		ec2Client:                 &fakeEC2Client{},
	}
	r.setup(controllerutil.CreateOrUpdate)

	ctx := ctrl.LoggerInto(t.Context(), zapr.NewLogger(zaptest.NewLogger(t)))

	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(hcp)}); err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}

	eventHandlerList := r.eventHandlers(c.Scheme(), c.RESTMapper())
	eventHandlersByObject := make(map[schema.GroupVersionKind]handler.EventHandler, len(eventHandlerList))
	for _, handler := range eventHandlerList {
		gvk, err := apiutil.GVKForObject(handler.obj, api.Scheme)
		if err != nil {
			t.Errorf("failed to get gvk for %T: %v", handler.obj, err)
		}
		eventHandlersByObject[gvk] = handler.handler
	}

	for _, createdObject := range c.created {
		t.Run(fmt.Sprintf("%T - %s", createdObject, createdObject.GetName()), func(t *testing.T) {
			gvk, err := apiutil.GVKForObject(createdObject, api.Scheme)
			if err != nil {
				t.Fatalf("failed to get gvk for %T: %v", createdObject, err)
			}
			handler, found := eventHandlersByObject[gvk]
			if !found {
				t.Fatalf("reconciler creates %T but has no handler for them", createdObject)
			}

			fakeQueue := &createTrackingWorkqueue{}
			handler.Create(t.Context(), event.CreateEvent{Object: createdObject}, fakeQueue)

			if len(fakeQueue.items) != 1 || fakeQueue.items[0].Namespace != hcp.Namespace || fakeQueue.items[0].Name != hcp.Name {
				t.Errorf("object %+v didn't correctly create event", createdObject)
			}
		})
	}
}

type createTrackingClient struct {
	created []client.Object
	client.Client
}

func (t *createTrackingClient) Create(ctx context.Context, o client.Object, opts ...client.CreateOption) error {
	if err := t.Client.Create(ctx, o, opts...); err != nil {
		return err
	}
	t.created = append(t.created, o)
	return nil
}

type createTrackingWorkqueue struct {
	items []reconcile.Request
	workqueue.TypedRateLimitingInterface[reconcile.Request]
}

func (c *createTrackingWorkqueue) Add(item reconcile.Request) {
	c.items = append(c.items, item)
}

func TestNonReadyInfraTriggersRequeueAfter(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockedProviderWithOpenshiftImageRegistryOverrides := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(mockCtrl)
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(testutils.InitReleaseImageOrDie("4.15.0"), nil).AnyTimes()
	hcp := sampleHCP(t)
	pullSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "pull-secret"}}
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcp, pullSecret).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()
	r := &HostedControlPlaneReconciler{
		Client:                        c,
		ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
		ReleaseProvider:               mockedProviderWithOpenshiftImageRegistryOverrides,
		UserReleaseProvider:           &fakereleaseprovider.FakeReleaseProvider{},
		reconcileInfrastructureStatus: func(context.Context, *hyperv1.HostedControlPlane) (infra.InfrastructureStatus, error) {
			return infra.InfrastructureStatus{}, nil
		},
		ec2Client: &fakeEC2Client{},
	}
	r.setup(controllerutil.CreateOrUpdate)
	ctx := ctrl.LoggerInto(t.Context(), zapr.NewLogger(zaptest.NewLogger(t)))

	result, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(hcp)})
	if err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Errorf("expected requeue after of %s when infrastructure is not ready, got %s", time.Minute, result.RequeueAfter)
	}
}

func TestSetKASCustomKubeconfigStatus(t *testing.T) {
	hcp := sampleHCP(t)
	pullSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "pull-secret"}}
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcp, pullSecret).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()
	ctx := ctrl.LoggerInto(t.Context(), zapr.NewLogger(zaptest.NewLogger(t)))

	tests := []struct {
		name                 string
		KubeAPIServerDNSName string
		expectedStatus       *hyperv1.KubeconfigSecretRef
	}{
		{
			name:                 "KubeAPIServerDNSName is empty",
			KubeAPIServerDNSName: "",
			expectedStatus:       nil,
		},
		{
			name:                 "KubeAPIServerDNSName has a valid value",
			KubeAPIServerDNSName: "testapi.example.com",
			expectedStatus: &hyperv1.KubeconfigSecretRef{
				Name: "custom-admin-kubeconfig",
				Key:  "kubeconfig",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			hcp.Spec.KubeAPIServerDNSName = tc.KubeAPIServerDNSName

			err := setKASCustomKubeconfigStatus(ctx, hcp, c)
			g.Expect(err).To(BeNil(), fmt.Errorf("error setting custom kubeconfig status failed: %v", err))
			g.Expect(hcp.Status.CustomKubeconfig).To(Equal(tc.expectedStatus))
		})
	}
}

func TestIncludeServingCertificates(t *testing.T) {
	ctx := t.Context()
	hcp := sampleHCP(t)
	rootCA := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "root-ca",
			Namespace: hcp.Namespace,
		},
		Data: map[string]string{
			"ca.crt": "root-ca-cert",
		},
	}

	tests := []struct {
		name           string
		servingCerts   *configv1.APIServerServingCerts
		servingSecrets []*corev1.Secret
		expectedCert   string
		expectError    bool
	}{
		{
			name:         "APIServer servingCerts is nil",
			servingCerts: &configv1.APIServerServingCerts{},
			expectedCert: "root-ca-cert",
		},
		{
			name: "APIServer servingCerts configuration with one named certificates",
			servingCerts: &configv1.APIServerServingCerts{
				NamedCertificates: []configv1.APIServerNamedServingCert{
					{
						ServingCertificate: configv1.SecretNameReference{
							Name: "serving-cert-1",
						},
					},
				},
			},
			servingSecrets: []*corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "serving-cert-1",
						Namespace: hcp.Namespace,
					},
					Data: map[string][]byte{
						"tls.crt": []byte("cert-1"),
					},
				},
			},
			expectedCert: "root-ca-cert\ncert-1",
		},
		{
			name: "APIServer servingCerts configuration with multiple named certificates",
			servingCerts: &configv1.APIServerServingCerts{
				NamedCertificates: []configv1.APIServerNamedServingCert{
					{
						ServingCertificate: configv1.SecretNameReference{
							Name: "serving-cert-1",
						},
					},
					{
						ServingCertificate: configv1.SecretNameReference{
							Name: "serving-cert-2",
						},
					},
				},
			},
			servingSecrets: []*corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "serving-cert-1",
						Namespace: hcp.Namespace,
					},
					Data: map[string][]byte{
						"tls.crt": []byte("cert-1"),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "serving-cert-2",
						Namespace: hcp.Namespace,
					},
					Data: map[string][]byte{
						"tls.crt": []byte("cert-2"),
					},
				},
			},
			expectedCert: "root-ca-cert\ncert-1\ncert-2",
		},
		{
			name: "APIServer servingCerts configuration with missing named certificate",
			servingCerts: &configv1.APIServerServingCerts{
				NamedCertificates: []configv1.APIServerNamedServingCert{
					{
						ServingCertificate: configv1.SecretNameReference{
							Name: "missing-cert",
						},
					},
				},
			},
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{
					ServingCerts: *tc.servingCerts,
				},
			}

			fakeClient := fake.NewClientBuilder().WithObjects(rootCA).Build()
			for _, secret := range tc.servingSecrets {
				_ = fakeClient.Create(ctx, secret)
			}

			newRootCA, err := includeServingCertificates(ctx, fakeClient, hcp, rootCA)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(newRootCA.Data["ca.crt"]).To(Equal(tc.expectedCert))
			}
		})
	}
}

// TestControlPlaneComponents is a generic test which generates a fixture for each registered component's deployment/statefulset.
// This is helpful to allow to inspect the final manifest yaml result after all the pre/post-processing is applied.
func TestControlPlaneComponents(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockedProviderWithOpenshiftImageRegistryOverrides := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(mockCtrl)
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		Lookup(gomock.Any(), gomock.Any(), gomock.Any()).Return(testutils.InitReleaseImageOrDie("4.15.0"), nil).AnyTimes()
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		GetRegistryOverrides().Return(map[string]string{"registry": "override"}).AnyTimes()
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		GetOpenShiftImageRegistryOverrides().Return(map[string][]string{"registry": {"override"}}).AnyTimes()
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().GetMirroredReleaseImage().Return("").AnyTimes()

	tests := []struct {
		name           string
		featureSet     configv1.FeatureSet
		platformType   *hyperv1.PlatformType
		hcpAnnotations map[string]string
		mutateHCP      func(hcp *hyperv1.HostedControlPlane)
		setup          func(t *testing.T)
		subDirSuffix   string
	}{
		{
			name:         "Default feature set, default platform type",
			featureSet:   configv1.Default,
			platformType: nil,
		},
		{
			name:         "TechPreviewNoUpgrade feature set, default platform type",
			featureSet:   configv1.TechPreviewNoUpgrade,
			platformType: nil,
		},
		{
			name:         "Default feature set, IBM Cloud platform type",
			featureSet:   configv1.Default,
			platformType: ptr.To(hyperv1.IBMCloudPlatform),
		},
		{
			name:         "TechPreviewNoUpgrade feature set, GCP platform type",
			featureSet:   configv1.TechPreviewNoUpgrade,
			platformType: ptr.To(hyperv1.GCPPlatform),
		},
		{
			name:         "Default feature set, Azure platform with ARO Swift",
			featureSet:   configv1.Default,
			platformType: ptr.To(hyperv1.AzurePlatform),
			hcpAnnotations: map[string]string{
				hyperv1.SwiftPodNetworkInstanceAnnotation: "swift-network-instance",
			},
			mutateHCP: func(hcp *hyperv1.HostedControlPlane) {
				// Configure Azure KMS for ARO-HCP
				hcp.Spec.Platform.Azure.Cloud = "AzurePublicCloud"
				hcp.Spec.SecretEncryption = &hyperv1.SecretEncryptionSpec{
					Type: hyperv1.KMS,
					KMS: &hyperv1.KMSSpec{
						Provider: hyperv1.AZURE,
						Azure: &hyperv1.AzureKMSSpec{
							ActiveKey: hyperv1.AzureKMSKey{
								KeyVaultName: "test-kms-keyvault",
								KeyName:      "test-key",
								KeyVersion:   "1",
							},
							KeyVaultAccess: hyperv1.AzureKeyVaultPrivate,
						},
					},
				}
				// Configure Azure ManagedIdentities for ARO-HCP
				hcp.Spec.Platform.Azure.AzureAuthenticationConfig = hyperv1.AzureAuthenticationConfiguration{
					AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeManagedIdentities,
					ManagedIdentities: &hyperv1.AzureResourceManagedIdentities{
						ControlPlane: hyperv1.ControlPlaneManagedIdentities{
							ManagedIdentitiesKeyVault: hyperv1.ManagedAzureKeyVault{
								Name:     "test-keyvault",
								TenantID: "00000000-0000-0000-0000-000000000000",
							},
							CloudProvider:        hyperv1.ManagedIdentity{ClientID: "00000000-0000-0000-0000-000000000002", CredentialsSecretName: "cloud-provider-creds"},
							NodePoolManagement:   hyperv1.ManagedIdentity{ClientID: "00000000-0000-0000-0000-000000000003", CredentialsSecretName: "nodepool-mgmt-creds"},
							ControlPlaneOperator: hyperv1.ManagedIdentity{ClientID: "00000000-0000-0000-0000-000000000004", CredentialsSecretName: "cpo-creds"},
							ImageRegistry:        hyperv1.ManagedIdentity{ClientID: "00000000-0000-0000-0000-000000000005", CredentialsSecretName: "image-registry-creds"},
							Ingress:              hyperv1.ManagedIdentity{ClientID: "00000000-0000-0000-0000-000000000006", CredentialsSecretName: "ingress-creds"},
							Network:              hyperv1.ManagedIdentity{ClientID: "00000000-0000-0000-0000-000000000007", CredentialsSecretName: "network-creds"},
							Disk:                 hyperv1.ManagedIdentity{ClientID: "00000000-0000-0000-0000-000000000008", CredentialsSecretName: "disk-creds"},
							File:                 hyperv1.ManagedIdentity{ClientID: "00000000-0000-0000-0000-000000000009", CredentialsSecretName: "file-creds"},
						},
						DataPlane: hyperv1.DataPlaneManagedIdentities{
							ImageRegistryMSIClientID: "00000000-0000-0000-0000-00000000000a",
							DiskMSIClientID:          "00000000-0000-0000-0000-00000000000b",
							FileMSIClientID:          "00000000-0000-0000-0000-00000000000c",
						},
					},
				}
			},
			setup:        azureutil.SetAsAroHCPTest,
			subDirSuffix: "AROSwift",
		},
	}

	for _, tt := range tests {
		reconciler := &HostedControlPlaneReconciler{
			ReleaseProvider:               mockedProviderWithOpenshiftImageRegistryOverrides,
			ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
		}
		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hcp",
				Namespace: "hcp-namespace",
				Labels: map[string]string{
					"cluster.x-k8s.io/cluster-name": "cluster_name",
				},
				Annotations: map[string]string{},
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				IssuerURL: "https://test-oidc-bucket.s3.us-east-1.amazonaws.com/test-cluster",
				Configuration: &hyperv1.ClusterConfiguration{
					FeatureGate: &configv1.FeatureGateSpec{},
				},
				Services: []hyperv1.ServicePublishingStrategyMapping{
					{
						Service: hyperv1.Ignition,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.Route,
						},
					},
				},
				Networking: hyperv1.ClusterNetworking{
					ClusterNetwork: []hyperv1.ClusterNetworkEntry{
						{
							CIDR: *ipnet.MustParseCIDR("10.132.0.0/14"),
						},
					},
				},
				Etcd: hyperv1.EtcdSpec{
					ManagementType: hyperv1.Managed,
				},
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AWSPlatform,
					AWS: &hyperv1.AWSPlatformSpec{
						RolesRef: hyperv1.AWSRolesRef{
							NodePoolManagementARN: "arn:aws:iam::123456789012:role/test-node-pool-management-role",
						},
						TerminationHandlerQueueURL: ptr.To("https://sqs.us-east-1.amazonaws.com/123456789012/test-queue"),
					},
					Azure: &hyperv1.AzurePlatformSpec{
						SubnetID:        "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/virtualNetworks/myVnetName/subnets/mySubnetName",
						SecurityGroupID: "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/networkSecurityGroups/myNSGName",
						VnetID:          "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/virtualNetworks/myVnetName",
						AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
							AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeWorkloadIdentities,
							WorkloadIdentities: &hyperv1.AzureWorkloadIdentities{
								CloudProvider: hyperv1.WorkloadIdentity{ClientID: hyperv1.AzureClientID("myClientID")},
							},
						},
					},
					OpenStack: &hyperv1.OpenStackPlatformSpec{
						IdentityRef: hyperv1.OpenStackIdentityReference{
							Name: "fake-cloud-credentials-secret",
						},
					},
					PowerVS: &hyperv1.PowerVSPlatformSpec{
						VPC: &hyperv1.PowerVSVPC{},
					},
					IBMCloud: &hyperv1.IBMCloudPlatformSpec{
						ProviderType: configv1.IBMCloudProviderTypeVPC,
					},
					GCP: &hyperv1.GCPPlatformSpec{
						Project: "my-project",
						Region:  "us-central1",
						NetworkConfig: hyperv1.GCPNetworkConfig{
							Network: hyperv1.GCPResourceReference{
								Name: "my-network",
							},
						},
					},
				},
				ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.16.10-x86_64",
			},
		}
		if tt.platformType != nil {
			hcp.Spec.Platform.Type = *tt.platformType
			if *tt.platformType == hyperv1.IBMCloudPlatform {
				hcp.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{
					Port:             ptr.To[int32](2040),
					AdvertiseAddress: ptr.To("1.2.3.4"),
				}
			}
		}

		// Merge any additional HCP annotations from the test case
		for k, v := range tt.hcpAnnotations {
			hcp.Annotations[k] = v
		}

		// Apply any HCP mutations from the test case
		if tt.mutateHCP != nil {
			tt.mutateHCP(hcp)
		}

		// Run any setup function for the test case
		if tt.setup != nil {
			tt.setup(t)
		}

		reconciler.registerComponents(hcp)

		cpContext := controlplanecomponent.ControlPlaneContext{
			Context:                  t.Context(),
			ReleaseImageProvider:     testutil.FakeImageProvider(),
			UserReleaseImageProvider: testutil.FakeImageProvider(),
			ImageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
				Result: &dockerv1client.DockerImageConfig{
					Config: &docker10.DockerConfig{
						Labels: map[string]string{
							"io.openshift.release": "4.16.10",
						},
					},
				},
				Manifest: fakeimagemetadataprovider.FakeManifest{},
			},
			HCP:                    hcp,
			SkipPredicate:          true,
			SkipCertificateSigning: true,
		}

		cpContext.HCP.Spec.Configuration.FeatureGate.FeatureGateSelection.FeatureSet = tt.featureSet
		// This needs to be defined here, to avoid loopDetector reporting a no-op update, as changing the featureset will actually cause an update.
		cpContext.ApplyProvider = upsert.NewApplyProvider(true)

		for _, component := range reconciler.components {
			fakeObjects, err := componentsFakeObjects(hcp.Namespace, tt.featureSet)
			if err != nil {
				t.Fatalf("failed to generate fake objects: %v", err)
			}

			createdObjects := []client.Object{}
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).
				WithObjects(fakeObjects...).
				WithObjects(componentsFakeDependencies(component.Name(), hcp.Namespace)...).
				WithInterceptorFuncs(interceptor.Funcs{
					Create: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						createdObjects = append(createdObjects, obj)
						return client.Create(ctx, obj, opts...)
					},
				}).
				Build()
			cpContext.Client = fakeClient

			// Reconcile multiple times to make sure multiple runs don't produce different results,
			// and to check if resources are making a no-op update calls.
			for range 2 {
				if err := component.Reconcile(cpContext); err != nil {
					t.Fatalf("failed to reconcile component %s: %v", component.Name(), err)
				}
			}

			for _, obj := range createdObjects {
				if obj.GetNamespace() != hcp.Namespace {
					t.Fatalf("expected object %s to be in namespace %s, got %s", obj.GetName(), hcp.Namespace, obj.GetNamespace())
				}

				switch typedObj := obj.(type) {
				case *hyperv1.ControlPlaneComponent:
					// this is needed to ensure the fixtures match, otherwise LastTransitionTime will have a different value for each execution.
					for i := range typedObj.Status.Conditions {
						typedObj.Status.Conditions[i].LastTransitionTime = metav1.Time{}
					}
				}

				yaml, err := util.SerializeResource(obj, api.Scheme)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				kind := obj.GetObjectKind().GroupVersionKind().Kind
				if kind == "" {
					t.Fatalf("object %s has no kind set", obj.GetName())
				}

				suffix := fmt.Sprintf("_%s_%s", obj.GetName(), strings.ToLower(kind))
				subDir := component.Name()
				if tt.featureSet != configv1.Default {
					subDir = fmt.Sprintf("%s/%s", component.Name(), tt.featureSet)
				}
				if tt.platformType != nil {
					subDir = fmt.Sprintf("%s/%s", component.Name(), *tt.platformType)
				}
				if tt.subDirSuffix != "" {
					subDir = fmt.Sprintf("%s/%s", component.Name(), tt.subDirSuffix)
				}
				testutil.CompareWithFixture(t, yaml, testutil.WithSubDir(subDir), testutil.WithSuffix(suffix))
			}

		}

		if err := cpContext.ApplyProvider.ValidateUpdateEvents(1); err != nil {
			t.Fatalf("update loop detected: %v", err)
		}
	}
}

func TestAWSSecurityGroupTags(t *testing.T) {
	tests := []struct {
		name         string
		hcp          *hyperv1.HostedControlPlane
		expectedTags map[string]string
	}{
		{
			name: "No additional tags, no AutoNode",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "test-infra",
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{},
						},
					},
				},
			},
			expectedTags: map[string]string{
				"kubernetes.io/cluster/test-infra": "owned",
				"Name":                             "test-infra-default-sg",
			},
		},
		{
			name: "Additional tags override Name and cluster key",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "myinfra",
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "Name", Value: "custom-name"},
								{Key: "kubernetes.io/cluster/myinfra", Value: "shared"},
								{Key: "foo", Value: "bar"},
							},
						},
					},
				},
			},
			expectedTags: map[string]string{
				"Name":                          "custom-name",
				"kubernetes.io/cluster/myinfra": "shared",
				"foo":                           "bar",
			},
		},
		{
			name: "AutoNode with Karpenter AWS adds karpenter.sh/discovery",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "karpenter-infra",
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{},
					},
					AutoNode: &hyperv1.AutoNode{
						Provisioner: &hyperv1.ProvisionerConfig{
							Name: hyperv1.ProvisionerKarpenter,
							Karpenter: &hyperv1.KarpenterConfig{
								Platform: hyperv1.AWSPlatform,
							},
						},
					},
				},
			},
			expectedTags: map[string]string{
				"kubernetes.io/cluster/karpenter-infra": "owned",
				"Name":                                  "karpenter-infra-default-sg",
				"karpenter.sh/discovery":                "karpenter-infra",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := awsSecurityGroupTags(tc.hcp)
			if len(got) != len(tc.expectedTags) {
				t.Errorf("expected %d tags, got %d: %v", len(tc.expectedTags), len(got), got)
			}
			for k, v := range tc.expectedTags {
				if got[k] != v {
					t.Errorf("expected tag %q=%q, got %q", k, v, got[k])
				}
			}
		})
	}
}

//go:embed testdata/featuregate-generator/feature-gate.yaml
var testFeatureGateYAML string

//go:embed testdata/featuregate-generator/feature-gate-tech-preview-no-upgrade.yaml
var testFeatureGateTechPreviewNoUpgradeYAML string

func componentsFakeObjects(namespace string, featureSet configv1.FeatureSet) ([]client.Object, error) {
	rootCA := manifests.RootCASecret(namespace)
	rootCA.Data = map[string][]byte{
		certs.CASignerCertMapKey: []byte("fake"),
	}
	authenticatorCertSecret := manifests.OpenshiftAuthenticatorCertSecret(namespace)
	authenticatorCertSecret.Data = map[string][]byte{
		corev1.TLSCertKey:       []byte("fake"),
		corev1.TLSPrivateKeyKey: []byte("fake"),
	}
	bootsrapCertSecret := manifests.KASMachineBootstrapClientCertSecret(namespace)
	bootsrapCertSecret.Data = map[string][]byte{
		corev1.TLSCertKey:       []byte("fake"),
		corev1.TLSPrivateKeyKey: []byte("fake"),
	}
	adminCertSecert := manifests.SystemAdminClientCertSecret(namespace)
	adminCertSecert.Data = map[string][]byte{
		corev1.TLSCertKey:       []byte("fake"),
		corev1.TLSPrivateKeyKey: []byte("fake"),
	}
	hccoCertSecert := manifests.HCCOClientCertSecret(namespace)
	hccoCertSecert.Data = map[string][]byte{
		corev1.TLSCertKey:       []byte("fake"),
		corev1.TLSPrivateKeyKey: []byte("fake"),
	}

	azureCredentialsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-credential-information",
			Namespace: "hcp-namespace",
		},
	}

	cloudCredsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fake-cloud-credentials-secret",
			Namespace: namespace,
		},
	}

	csrSigner := manifests.CSRSignerCASecret(namespace)
	csrSigner.Data = map[string][]byte{
		corev1.TLSCertKey:       []byte("fake"),
		corev1.TLSPrivateKeyKey: []byte("fake"),
	}
	fgConfigMap := &corev1.ConfigMap{}
	fgConfigMap.Name = "feature-gate"
	fgConfigMap.Namespace = namespace
	if featureSet == configv1.TechPreviewNoUpgrade {
		fgConfigMap.Data = map[string]string{"feature-gate.yaml": testFeatureGateTechPreviewNoUpgradeYAML}
	} else {
		fgConfigMap.Data = map[string]string{"feature-gate.yaml": testFeatureGateYAML}
	}

	privateRouterSvc := manifests.PrivateRouterService(namespace)
	privateRouterSvc.Spec.ClusterIP = "172.30.0.100"

	return []client.Object{
		rootCA, authenticatorCertSecret, bootsrapCertSecret, adminCertSecert, hccoCertSecert,
		manifests.KubeControllerManagerClientCertSecret(namespace),
		manifests.KubeSchedulerClientCertSecret(namespace),
		azureCredentialsSecret,
		cloudCredsSecret,
		csrSigner,
		fgConfigMap,
		privateRouterSvc,
	}, nil
}

func componentsFakeDependencies(componentName string, namespace string) []client.Object {
	var fakeComponents []client.Object

	// we need this to exist for components to reconcile
	fakeComponentTemplate := &hyperv1.ControlPlaneComponent{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
		},
		Status: hyperv1.ControlPlaneComponentStatus{
			Version: testutil.FakeImageProvider().Version(),
			Conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ControlPlaneComponentAvailable),
					Status: metav1.ConditionTrue,
				},
				{
					Type:   string(hyperv1.ControlPlaneComponentRolloutComplete),
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	// all components depend on KAS and KAS depends on etcd
	if componentName == kasv2.ComponentName {
		fakeComponentTemplate.Name = etcdv2.ComponentName
		fakeComponents = append(fakeComponents, fakeComponentTemplate.DeepCopy())
		fakeComponentTemplate.Name = fg.ComponentName
		fakeComponents = append(fakeComponents, fakeComponentTemplate.DeepCopy())
	} else {
		fakeComponentTemplate.Name = kasv2.ComponentName
		fakeComponents = append(fakeComponents, fakeComponentTemplate.DeepCopy())
	}

	if componentName != oapiv2.ComponentName {
		fakeComponentTemplate.Name = oapiv2.ComponentName
		fakeComponents = append(fakeComponents, fakeComponentTemplate.DeepCopy())
	}

	if componentName == ignitionproxyv2.ComponentName {
		fakeComponentTemplate.Name = ignitionserverv2.ComponentName
		fakeComponents = append(fakeComponents, fakeComponentTemplate.DeepCopy())
	}

	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "hcp-namespace"},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{}`),
		},
	}

	fakeComponents = append(fakeComponents, pullSecret.DeepCopy())

	return fakeComponents
}

func TestControlPlaneComponentsAvailable(t *testing.T) {
	testNamespace := "test-namespace"

	testCases := []struct {
		name           string
		components     []hyperv1.ControlPlaneComponent
		expectError    bool
		expectedMsg    string
		setupClientErr bool
	}{
		{
			name:        "When no components exist, it should return message indicating components not created",
			components:  []hyperv1.ControlPlaneComponent{},
			expectError: false,
			expectedMsg: "Control plane components have not been created yet",
		},
		{
			name: "When all components are available, it should return empty message",
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: testNamespace,
					},
					Status: hyperv1.ControlPlaneComponentStatus{
						Conditions: []metav1.Condition{
							{
								Type:   string(hyperv1.ControlPlaneComponentAvailable),
								Status: metav1.ConditionTrue,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-controller-manager",
						Namespace: testNamespace,
					},
					Status: hyperv1.ControlPlaneComponentStatus{
						Conditions: []metav1.Condition{
							{
								Type:   string(hyperv1.ControlPlaneComponentAvailable),
								Status: metav1.ConditionTrue,
							},
						},
					},
				},
			},
			expectError: false,
			expectedMsg: "",
		},
		{
			name: "When component has no Available condition, it should list component as not available",
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: testNamespace,
					},
					Status: hyperv1.ControlPlaneComponentStatus{
						Conditions: []metav1.Condition{},
					},
				},
			},
			expectError: false,
			expectedMsg: "Waiting for components to be available: kube-apiserver",
		},
		{
			name: "When component Available condition is False, it should list component as not available",
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: testNamespace,
					},
					Status: hyperv1.ControlPlaneComponentStatus{
						Conditions: []metav1.Condition{
							{
								Type:   string(hyperv1.ControlPlaneComponentAvailable),
								Status: metav1.ConditionFalse,
								Reason: "Deploying",
							},
						},
					},
				},
			},
			expectError: false,
			expectedMsg: "Waiting for components to be available: kube-apiserver",
		},
		{
			name: "When multiple components are not available, it should list all unavailable components",
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: testNamespace,
					},
					Status: hyperv1.ControlPlaneComponentStatus{
						Conditions: []metav1.Condition{
							{
								Type:   string(hyperv1.ControlPlaneComponentAvailable),
								Status: metav1.ConditionFalse,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-controller-manager",
						Namespace: testNamespace,
					},
					Status: hyperv1.ControlPlaneComponentStatus{
						Conditions: []metav1.Condition{
							{
								Type:   string(hyperv1.ControlPlaneComponentAvailable),
								Status: metav1.ConditionTrue,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-scheduler",
						Namespace: testNamespace,
					},
					Status: hyperv1.ControlPlaneComponentStatus{
						Conditions: []metav1.Condition{},
					},
				},
			},
			expectError: false,
			expectedMsg: "Waiting for components to be available: kube-apiserver, kube-scheduler",
		},
		{
			name: "When some components are available and others are not, it should list only unavailable components",
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: testNamespace,
					},
					Status: hyperv1.ControlPlaneComponentStatus{
						Conditions: []metav1.Condition{
							{
								Type:   string(hyperv1.ControlPlaneComponentAvailable),
								Status: metav1.ConditionTrue,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "oauth-server",
						Namespace: testNamespace,
					},
					Status: hyperv1.ControlPlaneComponentStatus{
						Conditions: []metav1.Condition{
							{
								Type:   string(hyperv1.ControlPlaneComponentAvailable),
								Status: metav1.ConditionFalse,
							},
						},
					},
				},
			},
			expectError: false,
			expectedMsg: "Waiting for components to be available: oauth-server",
		},
		{
			name:           "When client fails to list components, it should return error",
			components:     []hyperv1.ControlPlaneComponent{},
			setupClientErr: true,
			expectError:    true,
			expectedMsg:    "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
				},
			}

			// Convert components to runtime objects
			objs := []client.Object{hcp}
			for i := range tc.components {
				objs = append(objs, &tc.components[i])
			}

			// Setup client with or without error interceptor
			var c client.Client
			if tc.setupClientErr {
				c = fake.NewClientBuilder().
					WithScheme(api.Scheme).
					WithObjects(objs...).
					WithInterceptorFuncs(interceptor.Funcs{
						List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
							return fmt.Errorf("simulated list error")
						},
					}).
					Build()
			} else {
				c = fake.NewClientBuilder().
					WithScheme(api.Scheme).
					WithObjects(objs...).
					Build()
			}

			// Create reconciler
			r := &HostedControlPlaneReconciler{
				Client: c,
				Log:    zapr.NewLogger(zaptest.NewLogger(t)),
			}

			// Execute the function under test
			msg, err := r.controlPlaneComponentsAvailable(context.Background(), hcp)

			// Verify results
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("failed to list control plane components"))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(msg).To(Equal(tc.expectedMsg))
			}
		})
	}
}

func TestRemoveHCPIngressFromRoutes(t *testing.T) {
	const namespace = "test-ns"

	tests := []struct {
		name           string
		hcp            *hyperv1.HostedControlPlane
		existingRoutes []*routev1.Route
		expectedRoutes []routev1.Route
		expectError    bool
		errorContains  string
	}{
		{
			name: "Route with HCPRouteLabel is skipped",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: namespace,
				},
			},
			existingRoutes: []*routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hcp-managed-route",
						Namespace: namespace,
						Labels: map[string]string{
							util.HCPRouteLabel: namespace,
						},
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "router", Host: "example.com"},
							{RouterName: "other-router", Host: "other.com"},
						},
					},
				},
			},
			expectedRoutes: []routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hcp-managed-route",
						Namespace: namespace,
						Labels: map[string]string{
							util.HCPRouteLabel: namespace,
						},
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "router", Host: "example.com"},
							{RouterName: "other-router", Host: "other.com"},
						},
					},
				},
			},
		},
		{
			name: "Route without HCPRouteLabel has router ingress removed",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: namespace,
				},
			},
			existingRoutes: []*routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "router", Host: "example.com"},
							{RouterName: "other-router", Host: "other.com"},
						},
					},
				},
			},
			expectedRoutes: []routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "other-router", Host: "other.com"},
						},
					},
				},
			},
		},
		{
			name: "Route without router ingress is unchanged",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: namespace,
				},
			},
			existingRoutes: []*routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "other-router", Host: "other.com"},
							{RouterName: "another-router", Host: "another.com"},
						},
					},
				},
			},
			expectedRoutes: []routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "other-router", Host: "other.com"},
							{RouterName: "another-router", Host: "another.com"},
						},
					},
				},
			},
		},
		{
			name: "Route with only router ingress has all ingress removed",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: namespace,
				},
			},
			existingRoutes: []*routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "router-only-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "router", Host: "example.com"},
						},
					},
				},
			},
			expectedRoutes: []routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "router-only-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{},
					},
				},
			},
		},
		{
			name: "Multiple routes handled correctly",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: namespace,
				},
			},
			existingRoutes: []*routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hcp-managed",
						Namespace: namespace,
						Labels: map[string]string{
							util.HCPRouteLabel: namespace,
						},
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "router", Host: "hcp.example.com"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-route-1",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "router", Host: "route1.example.com"},
							{RouterName: "other-router", Host: "route1.other.com"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-route-2",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "other-router", Host: "route2.other.com"},
						},
					},
				},
			},
			expectedRoutes: []routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hcp-managed",
						Namespace: namespace,
						Labels: map[string]string{
							util.HCPRouteLabel: namespace,
						},
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "router", Host: "hcp.example.com"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-route-1",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "other-router", Host: "route1.other.com"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-route-2",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "other-router", Host: "route2.other.com"},
						},
					},
				},
			},
		},
		{
			name: "Route with empty ingress list is unchanged",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: namespace,
				},
			},
			existingRoutes: []*routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "empty-ingress-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{},
					},
				},
			},
			expectedRoutes: []routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "empty-ingress-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{},
					},
				},
			},
		},
		{
			name: "Route with multiple router ingress entries removes all",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: namespace,
				},
			},
			existingRoutes: []*routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "multi-router-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "router", Host: "example1.com"},
							{RouterName: "router", Host: "example2.com"},
							{RouterName: "other-router", Host: "other.com"},
							{RouterName: "router", Host: "example3.com"},
						},
					},
				},
			},
			expectedRoutes: []routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "multi-router-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "other-router", Host: "other.com"},
						},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			ctx := ctrl.LoggerInto(context.Background(), zapr.NewLogger(zaptest.NewLogger(t)))

			// Convert existing routes to client.Object slice
			existingObjects := make([]client.Object, 0, len(tc.existingRoutes))
			for _, route := range tc.existingRoutes {
				existingObjects = append(existingObjects, route)
			}
			existingObjects = append(existingObjects, tc.hcp)

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(existingObjects...).
				WithStatusSubresource(&routev1.Route{}).
				Build()

			r := &HostedControlPlaneReconciler{
				Client: fakeClient,
				Log:    ctrl.LoggerFrom(ctx),
			}

			err := r.removeHCPIngressFromRoutes(ctx, tc.hcp)

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				if tc.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tc.errorContains))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())

				// Verify routes match expected state
				var actualRoutes routev1.RouteList
				err = fakeClient.List(ctx, &actualRoutes, client.InNamespace(namespace))
				g.Expect(err).NotTo(HaveOccurred())

				// Sort routes by name for comparison
				sort.Slice(actualRoutes.Items, func(i, j int) bool {
					return actualRoutes.Items[i].Name < actualRoutes.Items[j].Name
				})
				sort.Slice(tc.expectedRoutes, func(i, j int) bool {
					return tc.expectedRoutes[i].Name < tc.expectedRoutes[j].Name
				})

				g.Expect(len(actualRoutes.Items)).To(Equal(len(tc.expectedRoutes)))

				for i := range actualRoutes.Items {
					actual := actualRoutes.Items[i]
					expected := tc.expectedRoutes[i]

					g.Expect(actual.Name).To(Equal(expected.Name))
					g.Expect(actual.Labels).To(Equal(expected.Labels))
					g.Expect(actual.Status.Ingress).To(HaveLen(len(expected.Status.Ingress)))

					// Compare ingress entries
					for j := range actual.Status.Ingress {
						g.Expect(actual.Status.Ingress[j].RouterName).To(Equal(expected.Status.Ingress[j].RouterName))
						g.Expect(actual.Status.Ingress[j].Host).To(Equal(expected.Status.Ingress[j].Host))
					}
				}
			}
		})
	}
}

func TestReconcileAvailabilityStatus(t *testing.T) {
	testCases := []struct {
		name                      string
		conditions                []metav1.Condition
		kubeConfigAvailable       bool
		healthCheckErr            error
		componentsNotAvailableMsg string
		componentsErr             error
		generation                int64
		expectedReady             bool
		expectedReason            string
		expectedMessage           string
	}{
		{
			name:                "When no conditions exist, it should report status unknown",
			conditions:          []metav1.Condition{},
			kubeConfigAvailable: true,
			expectedReady:       false,
			expectedReason:      hyperv1.StatusUnknownReason,
			expectedMessage:     "",
		},
		{
			name: "When infrastructure is not ready, it should report infrastructure reason",
			conditions: []metav1.Condition{
				{
					Type:    string(hyperv1.InfrastructureReady),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.WaitingOnInfrastructureReadyReason,
					Message: "Load balancer not provisioned",
				},
				{
					Type:   string(hyperv1.EtcdAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.EtcdQuorumAvailableReason,
				},
				{
					Type:   string(hyperv1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
			},
			kubeConfigAvailable: true,
			expectedReady:       false,
			expectedReason:      hyperv1.WaitingOnInfrastructureReadyReason,
			expectedMessage:     "Load balancer not provisioned",
		},
		{
			name: "When kubeconfig is not available, it should report waiting for kubeconfig",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.InfrastructureReady),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:   string(hyperv1.EtcdAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.EtcdQuorumAvailableReason,
				},
				{
					Type:   string(hyperv1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
			},
			kubeConfigAvailable: false,
			expectedReady:       false,
			expectedReason:      hyperv1.KubeconfigWaitingForCreateReason,
			expectedMessage:     "Waiting for hosted control plane kubeconfig to be created",
		},
		{
			name: "When etcd is not available, it should report etcd reason",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.InfrastructureReady),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:    string(hyperv1.EtcdAvailable),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.EtcdWaitingForQuorumReason,
					Message: "Waiting for etcd to reach quorum",
				},
				{
					Type:   string(hyperv1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
			},
			kubeConfigAvailable: true,
			expectedReady:       false,
			expectedReason:      hyperv1.EtcdWaitingForQuorumReason,
			expectedMessage:     "Waiting for etcd to reach quorum",
		},
		{
			name: "When KAS is not available, it should report KAS reason",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.InfrastructureReady),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:   string(hyperv1.EtcdAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.EtcdQuorumAvailableReason,
				},
				{
					Type:    string(hyperv1.KubeAPIServerAvailable),
					Status:  metav1.ConditionFalse,
					Reason:  "DeploymentNotReady",
					Message: "KAS deployment has 0/1 ready replicas",
				},
			},
			kubeConfigAvailable: true,
			expectedReady:       false,
			expectedReason:      "DeploymentNotReady",
			expectedMessage:     "KAS deployment has 0/1 ready replicas",
		},
		{
			name: "When health check fails, it should report KAS load balancer not reachable",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.InfrastructureReady),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:   string(hyperv1.EtcdAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.EtcdQuorumAvailableReason,
				},
				{
					Type:   string(hyperv1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
			},
			kubeConfigAvailable: true,
			healthCheckErr:      fmt.Errorf("connection refused"),
			expectedReady:       false,
			expectedReason:      hyperv1.KASLoadBalancerNotReachableReason,
			expectedMessage:     "connection refused",
		},
		{
			name: "When components check returns error, it should report components not available with error",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.InfrastructureReady),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:   string(hyperv1.EtcdAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.EtcdQuorumAvailableReason,
				},
				{
					Type:   string(hyperv1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
			},
			kubeConfigAvailable: true,
			componentsErr:       fmt.Errorf("failed to list components"),
			expectedReady:       false,
			expectedReason:      hyperv1.ControlPlaneComponentsNotAvailable,
			expectedMessage:     "Failed to check control plane component availability: failed to list components",
		},
		{
			name: "When components are not available, it should report components not available with message",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.InfrastructureReady),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:   string(hyperv1.EtcdAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.EtcdQuorumAvailableReason,
				},
				{
					Type:   string(hyperv1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
			},
			kubeConfigAvailable:       true,
			componentsNotAvailableMsg: "Waiting for components to be available: kube-scheduler",
			expectedReady:             false,
			expectedReason:            hyperv1.ControlPlaneComponentsNotAvailable,
			expectedMessage:           "Waiting for components to be available: kube-scheduler",
		},
		{
			name: "When all conditions are met, it should report available and ready",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.InfrastructureReady),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:   string(hyperv1.EtcdAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.EtcdQuorumAvailableReason,
				},
				{
					Type:   string(hyperv1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
			},
			kubeConfigAvailable: true,
			expectedReady:       true,
			expectedReason:      hyperv1.AsExpectedReason,
			expectedMessage:     "",
		},
		{
			name: "When infrastructure is true but etcd and KAS conditions are nil, it should report available",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.InfrastructureReady),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
			},
			kubeConfigAvailable: true,
			expectedReady:       true,
			expectedReason:      hyperv1.AsExpectedReason,
			expectedMessage:     "",
		},
		{
			name:            "When generation is set, it should propagate to ObservedGeneration",
			conditions:      []metav1.Condition{},
			generation:      42,
			expectedReady:   false,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			ready, condition := reconcileAvailabilityStatus(
				tc.conditions,
				tc.kubeConfigAvailable,
				tc.healthCheckErr,
				tc.componentsNotAvailableMsg,
				tc.componentsErr,
				tc.generation,
			)

			g.Expect(ready).To(Equal(tc.expectedReady))
			g.Expect(condition.Type).To(Equal(string(hyperv1.HostedControlPlaneAvailable)))
			g.Expect(condition.Reason).To(Equal(tc.expectedReason))
			g.Expect(condition.Message).To(Equal(tc.expectedMessage))
			if tc.expectedReady {
				g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			} else {
				g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			}
			g.Expect(condition.ObservedGeneration).To(Equal(tc.generation))
		})
	}
}

func TestEtcdStatefulSetCondition(t *testing.T) {
	testNamespace := "test-namespace"

	testCases := []struct {
		name              string
		sts               *appsv1.StatefulSet
		pvcs              []corev1.PersistentVolumeClaim
		expectedCondition metav1.Condition
		expectError       bool
	}{
		{
			name: "When all replicas are ready, it should report quorum available",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: testNamespace,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.StatefulSetStatus{
					ReadyReplicas: 3,
				},
			},
			expectedCondition: metav1.Condition{
				Type:   string(hyperv1.EtcdAvailable),
				Status: metav1.ConditionTrue,
				Reason: hyperv1.EtcdQuorumAvailableReason,
			},
		},
		{
			name: "When majority of replicas are ready, it should report quorum available",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: testNamespace,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.StatefulSetStatus{
					ReadyReplicas: 2,
				},
			},
			expectedCondition: metav1.Condition{
				Type:   string(hyperv1.EtcdAvailable),
				Status: metav1.ConditionTrue,
				Reason: hyperv1.EtcdQuorumAvailableReason,
			},
		},
		{
			name: "When single replica is ready out of one, it should report quorum available",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: testNamespace,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](1),
				},
				Status: appsv1.StatefulSetStatus{
					ReadyReplicas: 1,
				},
			},
			expectedCondition: metav1.Condition{
				Type:   string(hyperv1.EtcdAvailable),
				Status: metav1.ConditionTrue,
				Reason: hyperv1.EtcdQuorumAvailableReason,
			},
		},
		{
			name: "When no replicas are ready and PVCs are bound, it should report waiting for quorum",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: testNamespace,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.StatefulSetStatus{
					ReadyReplicas: 0,
				},
			},
			pvcs: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "data-etcd-0",
						Namespace: testNamespace,
						Labels:    map[string]string{"app": "etcd"},
					},
					Status: corev1.PersistentVolumeClaimStatus{
						Phase: corev1.ClaimBound,
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:    string(hyperv1.EtcdAvailable),
				Status:  metav1.ConditionFalse,
				Reason:  hyperv1.EtcdWaitingForQuorumReason,
				Message: "Waiting for etcd to reach quorum",
			},
		},
		{
			name: "When no replicas are ready and no PVCs exist, it should report waiting for quorum",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: testNamespace,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.StatefulSetStatus{
					ReadyReplicas: 0,
				},
			},
			expectedCondition: metav1.Condition{
				Type:    string(hyperv1.EtcdAvailable),
				Status:  metav1.ConditionFalse,
				Reason:  hyperv1.EtcdWaitingForQuorumReason,
				Message: "Waiting for etcd to reach quorum",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			clientBuilder := fake.NewClientBuilder()
			if len(tc.pvcs) > 0 {
				pvcList := &corev1.PersistentVolumeClaimList{Items: tc.pvcs}
				clientBuilder = clientBuilder.WithLists(pvcList)
			}
			fakeClient := clientBuilder.Build()

			r := &HostedControlPlaneReconciler{
				Client: fakeClient,
				Log:    ctrl.LoggerFrom(t.Context()),
			}

			condition, err := r.etcdStatefulSetCondition(t.Context(), tc.sts)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(condition).ToNot(BeNil())
			g.Expect(condition.Type).To(Equal(tc.expectedCondition.Type))
			g.Expect(condition.Status).To(Equal(tc.expectedCondition.Status))
			g.Expect(condition.Reason).To(Equal(tc.expectedCondition.Reason))
			if tc.expectedCondition.Message != "" {
				g.Expect(condition.Message).To(Equal(tc.expectedCondition.Message))
			}
		})
	}
}
