//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	. "github.com/onsi/gomega"
	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type DRMigrationPublicAndPrivateTest struct {
	ctx            context.Context
	srcMgmtClient  crclient.Client
	srcMgmtConfig  *rest.Config
	dstMgmtClient  crclient.Client
	dstMgmtCluster *hyperv1.HostedCluster
	etcdVars       map[string]string
	clusterOpts    core.CreateOptions
	s3Details      map[string]string
}

func NewDRMigrationPublicAndPrivateTest(ctx context.Context, srcMgmtClient crclient.Client, srcMgmtConfig *rest.Config, dstMgmtCluster *hyperv1.HostedCluster,
	dstMgmtClient crclient.Client, etcdVars map[string]string, clusterOpts core.CreateOptions, s3Details map[string]string) *DRMigrationPublicAndPrivateTest {
	return &DRMigrationPublicAndPrivateTest{
		ctx:            ctx,
		srcMgmtClient:  srcMgmtClient,
		srcMgmtConfig:  srcMgmtConfig,
		dstMgmtCluster: dstMgmtCluster,
		dstMgmtClient:  dstMgmtClient,
		etcdVars:       etcdVars,
		clusterOpts:    clusterOpts,
		s3Details:      s3Details,
	}
}

func (pr *DRMigrationPublicAndPrivateTest) Setup(t *testing.T) {}

func (pr *DRMigrationPublicAndPrivateTest) BuildHostedClusterManifest() core.CreateOptions {
	opts := pr.clusterOpts

	opts.AWSPlatform.EndpointAccess = string(hyperv1.PublicAndPrivate)
	opts.AWSPlatform.InstanceType = "m5.large"

	// Adding Label to avoid ClusterDeletion
	opts.BeforeApply = func(o crclient.Object) {
		hostedCluster, isHostedCluster := o.(*hyperv1.HostedCluster)
		if !isHostedCluster {
			return
		}
		hostedCluster.Labels = map[string]string{e2eutil.AvoidClusterDeletetion: "false"}
	}

	// This annotation will avoid the cleanup of aws resources in the old HC
	// located in the srcMgmtCluster, the annotation is enabled on RestoreETCD function
	opts.Annotations = []string{
		fmt.Sprintf("%s=false", hyperv1.CleanupCloudResourcesAnnotation),
	}

	return opts
}

func (pap *DRMigrationPublicAndPrivateTest) Run(t *testing.T, hostedCluster hyperv1.HostedCluster, hcNodePool hyperv1.NodePool) {
	g := NewWithT(t)
	ctx := pap.ctx

	var url string
	if hostedCluster.Spec.Platform.AWS.EndpointAccess == hyperv1.PublicAndPrivate {
		for _, service := range hostedCluster.Spec.Services {
			if service.Service == hyperv1.APIServer {
				url = service.Route.Hostname
				break
			}
		}
	}
	srcDNSIPAddress, err := e2eutil.WaitForDNS(t, ctx, url)

	fmt.Println("Migrating HostedCluster: ", hostedCluster.Name)
	fmt.Println("Seed: ", pap.s3Details["seedName"])

	var bckManifests []e2eutil.Manifest

	nodepools := &hyperv1.NodePoolList{}
	err = pap.srcMgmtClient.List(ctx, nodepools, crclient.InNamespace(hostedCluster.Namespace))
	g.Expect(err).NotTo(HaveOccurred(), "failed to list nodepools in namespace %s: %v", hostedCluster.Namespace, err)

	// Migration process
	t.Log("Starting Migration")
	h := &e2eutil.ManifestHandler{
		Manifests:     bckManifests,
		SrcClient:     pap.srcMgmtClient,
		DstClient:     pap.dstMgmtClient,
		HostedCluster: &hostedCluster,
		NodePools:     nodepools,
		Ctx:           ctx,
		UploadToS3:    false,
	}

	// Stop HC, NP Reconciliation
	if err := h.ManageReconciliation(h.HostedCluster, StringTrue); err != nil {
		t.Fatalf("failed managing reconciliation: %v", err)
	}

	for _, nodePool := range h.NodePools.Items {
		if err := h.ManageReconciliation(&nodePool, StringTrue); err != nil {
			t.Fatalf("failed managing reconciliation: %v", err)
		}
	}

	hcpNamespace := manifests.HostedControlPlaneNamespace(h.HostedCluster.Namespace, h.HostedCluster.Name).Name
	etcdMatchLabels := map[string]string{"app": "etcd"}

	// ETCD Backup
	err = PerformEtcdBackup(t, pap.etcdVars, pap.s3Details, h, pap.srcMgmtConfig)
	g.Expect(err).NotTo(HaveOccurred(), "failed on etcd backup")
	defer func() {
		awsConfig := &aws.Config{
			Region:      aws.String(pap.s3Details["s3Region"]),
			Credentials: credentials.NewSharedCredentials(pap.s3Details["s3Creds"], "default"),
		}

		if hostedCluster.Spec.ControllerAvailabilityPolicy == hyperv1.SingleReplica {
			err := e2eutil.DeleteS3Object(ctx, awsConfig, pap.s3Details["bucketName"], pap.s3Details["etcd-0"])
			g.Expect(err).NotTo(HaveOccurred(), "error deleting object %s in S3 bucket: %s", pap.s3Details["etcd-0"], pap.s3Details["bucketName"])
		} else {
			etcdPods := &corev1.PodList{}

			if err := h.SrcClient.List(h.Ctx, etcdPods, crclient.InNamespace(hcpNamespace), crclient.MatchingLabels(etcdMatchLabels)); err != nil {
				t.Fatal("failed to get etcd pods:", err)
			}

			for _, pod := range etcdPods.Items {
				err := e2eutil.DeleteS3Object(ctx, awsConfig, pap.s3Details["bucketName"], pap.s3Details[pod.Name])
				g.Expect(err).NotTo(HaveOccurred(), "error deleting key %s in S3 bucket: %s", pap.s3Details[pod.Name], pap.s3Details["bucketName"])
			}
		}
	}()

	// Manifest backup
	err = PerformManifestsBackup(t, pap.s3Details, h)
	g.Expect(err).NotTo(HaveOccurred(), "failed on manifests backup")

	// Clean routes in the srcMgmtCluster HCP NS
	routes := &routev1.RouteList{}
	if err := e2eutil.DeleteResource(ctx, h.SrcClient, routes, hcpNamespace, map[string]string{}); err != nil {
		t.Fatalf("error deleting routes in the HCP Namespace at the source ManagementCluster: %v", err)
	}

	// Upload to S3
	if h.UploadToS3 {
		err = h.UploadManifests(pap.s3Details)
		g.Expect(err).NotTo(HaveOccurred(), "failed on manifests upload")
	}

	// Restore Manifests in destination cluster
	err = RestoreManifestsBackup(t, h, pap.s3Details)
	g.Expect(err).NotTo(HaveOccurred(), "failed restoring manifests in destination cluster")

	// Recover from migration status
	for _, nodePool := range nodepools.Items {
		e2eutil.WaitForXNodePoolReplicas(t, h.Ctx, h.DstClient, *nodePool.Spec.Replicas, &nodePool)
	}

	// Setting the cleanup for the HostedCluster in the destination MGMT Cluster, It was not created with createCluster function
	t.Cleanup(func() {
		e2eutil.Teardown(context.Background(), t, h.DstClient, h.HostedCluster, &pap.clusterOpts, globalOpts.ArtifactDir)
	})
	t.Cleanup(func() {
		e2eutil.EnsureAllContainersHavePullPolicyIfNotPresent(t, context.Background(), h.DstClient, h.HostedCluster)
	})
	t.Cleanup(func() {
		e2eutil.EnsureHCPContainersHaveResourceRequests(t, context.Background(), h.DstClient, h.HostedCluster)
	})
	t.Cleanup(func() {
		e2eutil.EnsureNoPodsWithTooHighPriority(t, context.Background(), h.DstClient, h.HostedCluster)
	})
	t.Cleanup(func() {
		e2eutil.NoticePreemptionOrFailedScheduling(t, context.Background(), h.DstClient, h.HostedCluster)
	})
	t.Cleanup(func() {
		e2eutil.EnsureAllRoutesUseHCPRouter(t, context.Background(), h.DstClient, h.HostedCluster)
	})

	// Scaling down the Deps and StatefulSets to allow the migration
	err = ScaleHCPResources(t, h.Ctx, hostedCluster, h.SrcClient, pap.srcMgmtConfig, zeroReplicas)
	// Scalling up the CPO Deployment when teardown, to manage the finalizer removal
	defer func() {
		fmt.Println("Rescaling CPO in HCP Namespace")
		cpoDep := &appsv1.Deployment{}
		cpoDep.Name = "control-plane-operator"
		cpoDep.Namespace = hcpNamespace
		if err := h.SrcClient.Get(h.Ctx, crclient.ObjectKeyFromObject(cpoDep), cpoDep); err != nil {
			t.Logf("failed getting CPO Deployment: %v", err)
		}

		cpoDepOrig := cpoDep.DeepCopy()
		cpoDep.Spec.Replicas = &oneReplicas
		if err := h.SrcClient.Patch(h.Ctx, cpoDep, crclient.MergeFrom(cpoDepOrig)); err != nil {
			t.Logf("failed scaling up CPO Deployment: %v", err)
		}
	}()
	g.Expect(err).NotTo(HaveOccurred(), "error scalling down the old hosted cluster: %v", err)

	for _, nodePool := range nodepools.Items {
		e2eutil.WaitForXNodePoolReplicas(t, h.Ctx, h.DstClient, *nodePool.Spec.Replicas, &nodePool)
	}

	for _, nodePool := range nodepools.Items {
		e2eutil.WaitForFinishNodePoolUpdate(t, h.Ctx, h.DstClient, &nodePool)
	}

	// Wait for DNS Propagation
	for _, service := range h.HostedCluster.Spec.Services {
		if service.Service == hyperv1.APIServer {
			url = service.Route.Hostname
			break
		}
	}
	dstDNSIPAddress, err := e2eutil.WaitForDNS(t, ctx, url)
	g.Expect(err).NotTo(HaveOccurred(), "failed to reach DNS URL")

	newHostedClient := e2eutil.WaitForGuestClient(t, ctx, h.DstClient, &hostedCluster)

	// Sometimes the DNS's takes more than expected to be propagated, so it causes
	// unexpected behaviour into the migration.
	if srcDNSIPAddress.String() == dstDNSIPAddress.String() {
		// Wait for NodePool to be aware of the API change, expecting 0 replicas in NodePool
		for _, nodePool := range nodepools.Items {
			e2eutil.WaitForXNodePoolReplicas(t, h.Ctx, h.DstClient, int32(0), &nodePool)
		}
	}

	// Once the OVN pods gets deleted, need to ensure reliablity of the HC API
	newHostedClient = e2eutil.WaitForGuestClient(t, ctx, h.DstClient, &hostedCluster)

	// Clean Routes in the HCP namespace at the Source Management Cluster
	routeList := &routev1.RouteList{}
	err = h.SrcClient.List(ctx, routeList, crclient.InNamespace(hcpNamespace))
	g.Expect(int32(len(routeList.Items))).NotTo(BeZero(), "route list in the control plane namespace has zero length")

	for _, route := range routeList.Items {
		err := h.SrcClient.Delete(ctx, &route)
		g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("couldn't delete route: %s\n", route.Name))
	}

	// Wait for HC Nodes
	hcNodes := e2eutil.WaitForNReadyNodesByNodePool(t, ctx, newHostedClient, *hcNodePool.Spec.Replicas, hostedCluster.Spec.Platform.Type, hcNodePool.Name)
	g.Expect(int32(len(hcNodes))).To(Equal(*hcNodePool.Spec.Replicas))

	// Wait for X replicas in NodePool
	for _, nodePool := range nodepools.Items {
		e2eutil.WaitForXNodePoolReplicas(t, h.Ctx, h.DstClient, *nodePool.Spec.Replicas, &nodePool)
	}
}

type DRMigrationPrivateTest struct {
	ctx            context.Context
	srcMgmtClient  crclient.Client
	srcMgmtConfig  *rest.Config
	dstMgmtClient  crclient.Client
	dstMgmtCluster *hyperv1.HostedCluster
	etcdVars       map[string]string
	clusterOpts    core.CreateOptions
	s3Details      map[string]string
}

func NewDRMigrationPrivateTest(ctx context.Context, srcMgmtClient crclient.Client, srcMgmtConfig *rest.Config, dstMgmtCluster *hyperv1.HostedCluster,
	dstMgmtClient crclient.Client, etcdVars map[string]string, clusterOpts core.CreateOptions, s3Details map[string]string) *DRMigrationPublicAndPrivateTest {
	return &DRMigrationPublicAndPrivateTest{
		ctx:            ctx,
		srcMgmtClient:  srcMgmtClient,
		srcMgmtConfig:  srcMgmtConfig,
		dstMgmtCluster: dstMgmtCluster,
		dstMgmtClient:  dstMgmtClient,
		etcdVars:       etcdVars,
		clusterOpts:    clusterOpts,
		s3Details:      s3Details,
	}
}

func (pr *DRMigrationPrivateTest) Setup(t *testing.T) {}

func (pr *DRMigrationPrivateTest) BuildHostedClusterManifest() core.CreateOptions {
	opts := pr.clusterOpts

	opts.AWSPlatform.EndpointAccess = string(hyperv1.Private)
	opts.AWSPlatform.InstanceType = "m5.large"

	// Adding Label to avoid ClusterDeletion
	opts.BeforeApply = func(o crclient.Object) {
		hostedCluster, isHostedCluster := o.(*hyperv1.HostedCluster)
		if !isHostedCluster {
			return
		}
		hostedCluster.Labels = map[string]string{e2eutil.AvoidClusterDeletetion: "false"}
	}

	// This annotation will avoid the cleanup of aws resources in the old HC
	// located in the srcMgmtCluster, the annotation is enabled on RestoreETCD function
	opts.Annotations = []string{
		fmt.Sprintf("%s=false", hyperv1.CleanupCloudResourcesAnnotation),
	}

	return opts
}

func (pr *DRMigrationPrivateTest) Run(t *testing.T, hostedCluster hyperv1.HostedCluster, hcNodePool hyperv1.NodePool) {
	g := NewWithT(t)
	ctx := pr.ctx

	fmt.Println("Migrating HostedCluster: ", hostedCluster.Name)
	fmt.Println("Seed: ", pr.s3Details["seedName"])

	var bckManifests []e2eutil.Manifest

	nodepools := &hyperv1.NodePoolList{}
	err := pr.srcMgmtClient.List(ctx, nodepools, crclient.InNamespace(hostedCluster.Namespace))
	g.Expect(err).NotTo(HaveOccurred(), "failed to list nodepools in namespace %s: %v", hostedCluster.Namespace, err)

	// Migration process
	t.Log("Starting Migration")
	h := &e2eutil.ManifestHandler{
		Manifests:     bckManifests,
		SrcClient:     pr.srcMgmtClient,
		DstClient:     pr.dstMgmtClient,
		HostedCluster: &hostedCluster,
		NodePools:     nodepools,
		Ctx:           ctx,
		UploadToS3:    false,
	}

	// Stop HC, NP Reconciliation
	if err := h.ManageReconciliation(h.HostedCluster, StringTrue); err != nil {
		t.Fatalf("failed managing reconciliation: %v", err)
	}

	for _, nodePool := range h.NodePools.Items {
		if err := h.ManageReconciliation(&nodePool, StringTrue); err != nil {
			t.Fatalf("failed managing reconciliation: %v", err)
		}
	}

	hcpNamespace := manifests.HostedControlPlaneNamespace(h.HostedCluster.Namespace, h.HostedCluster.Name).Name
	etcdMatchLabels := map[string]string{"app": "etcd"}

	// ETCD Backup
	err = PerformEtcdBackup(t, pr.etcdVars, pr.s3Details, h, pr.srcMgmtConfig)
	g.Expect(err).NotTo(HaveOccurred(), "failed on etcd backup")
	defer func() {
		awsConfig := &aws.Config{
			Region:      aws.String(pr.s3Details["s3Region"]),
			Credentials: credentials.NewSharedCredentials(pr.s3Details["s3Creds"], "default"),
		}

		if hostedCluster.Spec.ControllerAvailabilityPolicy == hyperv1.SingleReplica {
			err := e2eutil.DeleteS3Object(ctx, awsConfig, pr.s3Details["bucketName"], pr.s3Details["etcd-0"])
			g.Expect(err).NotTo(HaveOccurred(), "error deleting object %s in S3 bucket: %s", pr.s3Details["etcd-0"], pr.s3Details["bucketName"])
		} else {
			etcdPods := &corev1.PodList{}

			if err := h.SrcClient.List(h.Ctx, etcdPods, crclient.InNamespace(hcpNamespace), crclient.MatchingLabels(etcdMatchLabels)); err != nil {
				t.Fatal("failed to get etcd pods:", err)
			}

			for _, pod := range etcdPods.Items {
				err := e2eutil.DeleteS3Object(ctx, awsConfig, pr.s3Details["bucketName"], pr.s3Details[pod.Name])
				g.Expect(err).NotTo(HaveOccurred(), "error deleting key %s in S3 bucket: %s", pr.s3Details[pod.Name], pr.s3Details["bucketName"])
			}
		}
	}()

	// Manifest backup
	err = PerformManifestsBackup(t, pr.s3Details, h)
	g.Expect(err).NotTo(HaveOccurred(), "failed on manifests backup")

	// Upload to S3
	if h.UploadToS3 {
		err = h.UploadManifests(pr.s3Details)
		g.Expect(err).NotTo(HaveOccurred(), "failed on manifests upload")
	}

	// Restore Manifests in destination cluster
	err = RestoreManifestsBackup(t, h, pr.s3Details)
	g.Expect(err).NotTo(HaveOccurred(), "failed restoring manifests in destination cluster")

	// Recover from migration status
	for _, nodePool := range nodepools.Items {
		e2eutil.WaitForXNodePoolReplicas(t, h.Ctx, h.DstClient, *nodePool.Spec.Replicas, &nodePool)
	}

	// Setting the cleanup for the HostedCluster in the destination MGMT Cluster, It was not created with createCluster function
	t.Cleanup(func() {
		e2eutil.Teardown(context.Background(), t, h.DstClient, h.HostedCluster, &pr.clusterOpts, globalOpts.ArtifactDir)
	})
	t.Cleanup(func() {
		e2eutil.EnsureAllContainersHavePullPolicyIfNotPresent(t, context.Background(), h.DstClient, h.HostedCluster)
	})
	t.Cleanup(func() {
		e2eutil.EnsureHCPContainersHaveResourceRequests(t, context.Background(), h.DstClient, h.HostedCluster)
	})
	t.Cleanup(func() {
		e2eutil.EnsureNoPodsWithTooHighPriority(t, context.Background(), h.DstClient, h.HostedCluster)
	})
	t.Cleanup(func() {
		e2eutil.NoticePreemptionOrFailedScheduling(t, context.Background(), h.DstClient, h.HostedCluster)
	})
	t.Cleanup(func() {
		e2eutil.EnsureAllRoutesUseHCPRouter(t, context.Background(), h.DstClient, h.HostedCluster)
	})

	// Scaling down the Deps and StatefulSets to allow the migration
	err = ScaleHCPResources(t, h.Ctx, hostedCluster, h.SrcClient, pr.srcMgmtConfig, zeroReplicas)
	// Scalling up the CPO Deployment when teardown, to manage the finalizer removal
	defer func() {
		fmt.Println("Rescaling CPO in HCP Namespace")
		cpoDep := &appsv1.Deployment{}
		cpoDep.Name = "control-plane-operator"
		cpoDep.Namespace = hcpNamespace
		if err := h.SrcClient.Get(h.Ctx, crclient.ObjectKeyFromObject(cpoDep), cpoDep); err != nil {
			t.Logf("failed getting CPO Deployment: %v", err)
		}

		cpoDepOrig := cpoDep.DeepCopy()
		cpoDep.Spec.Replicas = &oneReplicas
		if err := h.SrcClient.Patch(h.Ctx, cpoDep, crclient.MergeFrom(cpoDepOrig)); err != nil {
			t.Logf("failed scaling up CPO Deployment: %v", err)
		}
	}()
	g.Expect(err).NotTo(HaveOccurred(), "error scalling down the old hosted cluster: %v", err)

	for _, nodePool := range nodepools.Items {
		e2eutil.WaitForXNodePoolReplicas(t, h.Ctx, h.DstClient, *nodePool.Spec.Replicas, &nodePool)
	}

	for _, nodePool := range nodepools.Items {
		e2eutil.WaitForFinishNodePoolUpdate(t, h.Ctx, h.DstClient, &nodePool)
	}

	// Wait for NodePool to be aware of the API change, expecting 0 replicas in NodePool
	for _, nodePool := range nodepools.Items {
		e2eutil.WaitForXNodePoolReplicas(t, h.Ctx, h.DstClient, int32(0), &nodePool)
	}

	// Wait for X replicas in NodePool
	for _, nodePool := range nodepools.Items {
		e2eutil.WaitForXNodePoolReplicas(t, h.Ctx, h.DstClient, *nodePool.Spec.Replicas, &nodePool)
	}
}

func ScaleHCPResources(t *testing.T, ctx context.Context, hc hyperv1.HostedCluster, mgmtClient crclient.Client, config *rest.Config, replicas int32) error {
	fmt.Printf("Scaling to %d Deployments and Stateful sets from old HostedCluster: %s\n", replicas, hc.Name)
	hcpNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name).Name

	// Deployments
	if err := wait.PollImmediateWithContext(ctx, 10*time.Second, 5*time.Minute, func(ctx context.Context) (done bool, err error) {
		deps := &appsv1.DeploymentList{}
		if err := mgmtClient.List(ctx, deps, &crclient.ListOptions{Namespace: hcpNamespace}); err != nil {
			if apierrors.IsServiceUnavailable(err) || strings.Contains(err.Error(), "connection refused") {
				return false, fmt.Errorf("connection refused, retrying...")
			}
			return false, fmt.Errorf("error getting deployments from %s: %v", hcpNamespace, err)
		}
		if len(deps.Items) > 0 {
			if err := e2eutil.ScaleResources(ctx, mgmtClient, deps, replicas); err != nil {
				return false, fmt.Errorf("error scaling deployments to %d: %v", replicas, err)
			}
		}
		return true, nil
	}); err != nil {
		return fmt.Errorf("error scalling deployments %v", err)
	}

	// StatefulSets
	if err := wait.PollImmediateWithContext(ctx, 10*time.Second, 5*time.Minute, func(ctx context.Context) (done bool, err error) {
		statefulSets := &appsv1.StatefulSetList{}
		if err := mgmtClient.List(ctx, statefulSets, &crclient.ListOptions{Namespace: hcpNamespace}); err != nil {
			if apierrors.IsServiceUnavailable(err) || strings.Contains(err.Error(), "connection refused") {
				return false, fmt.Errorf("connection refused, retrying...")
			}
			return false, fmt.Errorf("error getting statefulSets from %s: %v", hcpNamespace, err)
		}
		if len(statefulSets.Items) > 0 {
			if err := e2eutil.ScaleResources(ctx, mgmtClient, statefulSets, replicas); err != nil {
				return false, fmt.Errorf("error scaling down statefulSets: %v", err)
			}
		}
		return true, nil
	}); err != nil {
		return fmt.Errorf("error scalling stateful sets %v", err)
	}

	return nil
}

func PerformEtcdBackup(t *testing.T, etcdVars, s3Details map[string]string, h *e2eutil.ManifestHandler, config *rest.Config) error {
	t.Log("Executing ETCD backup")
	etcdMatchLabels := map[string]string{
		"app": "etcd",
	}
	hcpNamespace := manifests.HostedControlPlaneNamespace(h.HostedCluster.Namespace, h.HostedCluster.Name).Name

	date := time.Now()
	dateQuery := date.Format(time.RFC1123Z)

	etcdPods := &corev1.PodList{}
	if err := h.SrcClient.List(h.Ctx, etcdPods, crclient.InNamespace(hcpNamespace), crclient.MatchingLabels(etcdMatchLabels)); err != nil {
		return fmt.Errorf("failed to get etcd pods: %v", err)
	}

	snapshotSave := fmt.Sprintf("env ETCDCTL_API=3 %s --cacert %s --cert %s --key %s --endpoints=localhost:2379 snapshot save %s",
		etcdVars["bin"], etcdVars["CAPath"], etcdVars["CertPath"], etcdVars["CertKeyPath"], etcdVars["snapshotPath"])

	snapshotStatus := fmt.Sprintf("env ETCDCTL_API=3 %s -w table snapshot status %s", etcdVars["bin"], etcdVars["snapshotPath"])

	for _, pod := range etcdPods.Items {
		path := fmt.Sprintf("/%s/hc-backup-%s/etcd/%s-%s-snapshot-%s.db", s3Details["bucketName"], s3Details["seedName"], hcpNamespace, pod.Name, s3Details["seedName"])
		s3Details[pod.Name] = fmt.Sprintf("hc-backup-%s/etcd/%s-%s-snapshot-%s.db", s3Details["seedName"], hcpNamespace, pod.Name, s3Details["seedName"])
		postUrl := fmt.Sprintf("https://%s.s3.%s.amazonaws.com", s3Details["bucketName"], s3Details["s3Region"])
		postUri := fmt.Sprintf("hc-backup-%s/etcd/%s-%s-snapshot-%s.db", s3Details["seedName"], hcpNamespace, pod.Name, s3Details["seedName"])
		s3Details["s3EtcdSnapshotURL"] = fmt.Sprintf("%s/%s", postUrl, postUri)
		out := new(bytes.Buffer)

		podExecuter := e2eutil.PodExecOptions{
			StreamOptions: e2eutil.StreamOptions{
				IOStreams: genericclioptions.IOStreams{
					Out:    out,
					ErrOut: os.Stderr,
				},
			},
			Command:       strings.Split(snapshotSave, " "),
			Namespace:     hcpNamespace,
			PodName:       pod.Name,
			ContainerName: "etcd",
			Config:        config,
		}

		t.Logf("Streaming Backup command on %s", pod.Name)
		if err := podExecuter.Run(); err != nil {
			return fmt.Errorf("failed to execute etcdctl command: %v", err)
		}

		fmt.Printf("ETCD Snapshot creation Out: %s", out.String())

		podExecuter.Command = strings.Split(snapshotStatus, " ")

		t.Logf("Streaming Status command on %s", pod.Name)
		if err := podExecuter.Run(); err != nil {
			return fmt.Errorf("failed to execute etcdctl command: %v", err)
		}

		fmt.Printf("ETCD Snapshot status Out: %s", out.String())

		// Configure AWS Client
		AWSCreds := credentials.NewSharedCredentials(s3Details["s3Creds"], "default")
		secretAWSK, err := AWSCreds.Get()
		if err != nil {
			return fmt.Errorf("failed to recover the AWS Secret: %v", err)
		}

		signatureString := fmt.Sprintf("PUT\n\n%v\n%v\n%s", etcdVars["queryContentType"], dateQuery, path)
		authstring, err := presignUrlCreator(signatureString, secretAWSK)
		if err != nil {
			t.Fatal("error getting the authstring for S3 upload command:", err)
		}

		command := []string{
			"/usr/bin/curl", "-X", "PUT",
			s3Details["s3EtcdSnapshotURL"], "-H",
			fmt.Sprintf(`Host: %s.s3.%s.amazonaws.com`, s3Details["bucketName"], s3Details["s3Region"]), "-H",
			fmt.Sprintf(`Date: %s`, dateQuery), "-H",
			fmt.Sprintf(`Content-Type: %s`, etcdVars["queryContentType"]), "-H",
			authstring, "-T", etcdVars["snapshotPath"],
		}

		fmt.Println("Remote curl command:", command)

		podExecuter.Command = command

		t.Logf("Streaming Curl command on %s", pod.Name)
		if err := podExecuter.Run(); err != nil {
			return fmt.Errorf("failed executing curl command: %v", err)
		}

		if strings.Contains(out.String(), "Error") {
			fmt.Println("Command: ", command)
			return fmt.Errorf("error uploading etcd backup to s3: %v", out.String())
		}

		// Changing folder's permissions in S3
		sess, err := session.NewSession(&aws.Config{
			Region:      aws.String(s3Details["s3Region"]),
			Credentials: credentials.NewSharedCredentials(s3Details["s3Creds"], "default"),
		})
		if err != nil {
			return fmt.Errorf("error creating s3 session: %v", err)
		}

		svc := s3.New(sess)
		params := &s3.PutObjectAclInput{
			Bucket: aws.String(s3Details["bucketName"]),
			Key:    aws.String(fmt.Sprintf("hc-backup-%s/etcd/%s-%s-snapshot-%s.db", s3Details["seedName"], hcpNamespace, pod.Name, s3Details["seedName"])),
			ACL:    aws.String("public-read"),
		}
		_, err = svc.PutObjectAcl(params)
		if err != nil {
			return fmt.Errorf("error setting s3 permissions: %v", err)
		}

	}
	fmt.Println()

	return nil
}

func presignUrlCreator(signatureString string, creds credentials.Value) (string, error) {

	key := []byte(creds.SecretAccessKey)
	h := hmac.New(sha1.New, key)
	h.Write([]byte(signatureString))
	signatureHash := base64.StdEncoding.EncodeToString(h.Sum(nil))
	authstring := fmt.Sprintf("Authorization: AWS %s:%s", creds.AccessKeyID, signatureHash)
	if len(creds.AccessKeyID) < 1 || len(signatureHash) < 1 {
		return "", fmt.Errorf("signature hash or access key id are empty, please check them.")
	}
	return authstring, nil
}

func PerformManifestsBackup(t *testing.T, s3Details map[string]string, h *e2eutil.ManifestHandler) error {
	hcpNsName := manifests.HostedControlPlaneNamespace(h.HostedCluster.Namespace, h.HostedCluster.Name).Name

	// Manifests on HostedCluster Namespace
	// HC Namespace
	hcNs := &corev1.NamespaceList{}
	if err := h.GetNamespace(hcNs, h.HostedCluster.Namespace); err != nil {
		return fmt.Errorf("failed getting existant HostedCluster Namespace: %v", err)
	}
	if err := h.PackResources(hcNs, e2eutil.HcType); err != nil {
		return fmt.Errorf("failed packing existant HostedCluster Namespace: %v", err)
	}

	// HostedClusters
	hcs := &hyperv1.HostedClusterList{}
	if err := h.GetResources(hcs, h.HostedCluster.Namespace); err != nil {
		return fmt.Errorf("failed getting existant HostedClusters: %v", err)
	}
	if err := h.PackResources(hcs, e2eutil.HcType); err != nil {
		return fmt.Errorf("failed packing existant HostedClusters: %v", err)
	}

	// NodePools
	nps := &hyperv1.NodePoolList{}
	if err := h.GetResources(nps, h.HostedCluster.Namespace); err != nil {
		return fmt.Errorf("failed getting existant NodePools: %v", err)
	}
	if err := h.PackResources(nps, e2eutil.HcType); err != nil {
		return fmt.Errorf("failed packing existant NodePools: %v", err)
	}

	// Configmaps
	hcConfigMaps, err := h.GetReferencedConfigMaps()
	if err != nil {
		return fmt.Errorf("failed getting existant referenced ConfigMaps in hostedCluster %s: %v", h.HostedCluster.Name, err)
	}
	if err := h.PackResources(hcConfigMaps, e2eutil.HcType); err != nil {
		return fmt.Errorf("failed packing existant ConfigMaps: %v", err)
	}

	// Secrets
	hcSecrets, err := h.GetReferencedSecrets()
	if err != nil {
		return fmt.Errorf("failed getting existant referenced Secrets in hostedCluster %s: %v", h.HostedCluster.Name, err)
	}
	if err := h.PackResources(hcSecrets, e2eutil.HcType); err != nil {
		return fmt.Errorf("failed packing existant Secrets: %v", err)
	}

	// HCP Objects backup
	hcpNs := &corev1.NamespaceList{}
	if err := h.GetNamespace(hcpNs, hcpNsName); err != nil {
		return fmt.Errorf("failed getting existant HostedControlPlane Namespace: %v", err)
	}
	if err := h.PackResources(hcpNs, e2eutil.HcpType); err != nil {
		return fmt.Errorf("failed packing existant HostedControlPlane Namespace: %v", err)
	}

	hcpCMs := &corev1.ConfigMapList{}
	if err := h.GetResources(hcpCMs, hcpNsName); err != nil {
		return fmt.Errorf("failed getting existant HCP ConfigMaps: %v", err)
	}
	if err := h.PackResources(hcpCMs, e2eutil.HcpType); err != nil {
		return fmt.Errorf("failed packing existant HCP ConfigMaps: %v", err)
	}

	hcpSecrets := &corev1.SecretList{}
	if err := h.GetResources(hcpSecrets, hcpNsName); err != nil {
		return fmt.Errorf("failed getting existant HCP Secrets: %v", err)
	}
	if err := h.PackResources(hcpSecrets, e2eutil.HcpType); err != nil {
		return fmt.Errorf("failed packing existant HCP Secrets: %v", err)
	}

	hcp := &hyperv1.HostedControlPlaneList{}
	if err := h.GetResources(hcp, hcpNsName); err != nil {
		return fmt.Errorf("failed getting existant HCP: %v", err)
	}
	if err := h.PackResources(hcp, e2eutil.HcpType); err != nil {
		return fmt.Errorf("failed packing existant HCP: %v", err)
	}

	hcpAWSCluster := &capiaws.AWSClusterList{}
	if err := h.GetResources(hcpAWSCluster, hcpNsName); err != nil {
		return fmt.Errorf("failed getting existant HCP AWS Cluster: %v", err)
	}
	if err := h.PackResources(hcpAWSCluster, e2eutil.HcpType); err != nil {
		return fmt.Errorf("failed packing existant HCP AWS Cluster: %v", err)
	}

	hcpAWSMachineTemplates := &capiaws.AWSMachineTemplateList{}
	if err := h.GetResources(hcpAWSMachineTemplates, hcpNsName); err != nil {
		return fmt.Errorf("failed getting existant HCP AWS Machine Templates: %v", err)
	}
	if err := h.PackResources(hcpAWSMachineTemplates, e2eutil.HcpType); err != nil {
		return fmt.Errorf("failed packing existant HCP AWS Machine Templates: %v", err)
	}

	hcpAWSMachines := &capiaws.AWSMachineList{}
	if err := h.GetResources(hcpAWSMachines, hcpNsName); err != nil {
		return fmt.Errorf("failed getting existant HCP AWS Machines: %v", err)
	}
	if err := h.PackResources(hcpAWSMachines, e2eutil.HcpType); err != nil {
		return fmt.Errorf("failed packing existant HCP AWS Machines: %v", err)
	}

	hcpCluster := &capiv1.ClusterList{}
	if err := h.GetResources(hcpCluster, hcpNsName); err != nil {
		return fmt.Errorf("failed getting existant HCP Clusters: %v", err)
	}
	if err := h.PackResources(hcpCluster, e2eutil.HcpType); err != nil {
		return fmt.Errorf("failed packing existant HCP Clusters: %v", err)
	}

	hcpMachineDeployments := &capiv1.MachineDeploymentList{}
	if err := h.GetResources(hcpMachineDeployments, hcpNsName); err != nil {
		return fmt.Errorf("failed getting existant HCP Machine Deployments: %v", err)
	}
	if err := h.PackResources(hcpMachineDeployments, e2eutil.HcpType); err != nil {
		return fmt.Errorf("failed packing existant HCP Machine Deployments: %v", err)
	}

	hcpMachineSets := &capiv1.MachineSetList{}
	if err := h.GetResources(hcpMachineSets, hcpNsName); err != nil {
		return fmt.Errorf("failed getting existant HCP Machine Sets: %v", err)
	}
	if err := h.PackResources(hcpMachineSets, e2eutil.HcpType); err != nil {
		return fmt.Errorf("failed packing existant HCP Machine Sets: %v", err)
	}

	hcpMachines := &capiv1.MachineList{}
	if err := h.GetResources(hcpMachines, hcpNsName); err != nil {
		return fmt.Errorf("failed getting existant HCP Machines: %v", err)
	}
	if err := h.PackResources(hcpMachines, e2eutil.HcpType); err != nil {
		return fmt.Errorf("failed packing existant HCP Machines: %v", err)
	}

	return nil
}

func RestoreManifestsBackup(t *testing.T, h *e2eutil.ManifestHandler, s3Details map[string]string) error {
	hcNs := &corev1.Namespace{}
	hcNs.Kind = "Namespace"
	if err := h.RestoreResource(hcNs, e2eutil.HcType); err != nil {
		return fmt.Errorf("error restoring HC Namespace: %v\n", err)
	}

	hcSecret := &corev1.Secret{}
	hcSecret.Kind = "Secret"
	if err := h.RestoreResource(hcSecret, e2eutil.HcType); err != nil {
		return fmt.Errorf("error restoring HC secrets: %v\n", err)
	}

	hcConfigMap := &corev1.ConfigMap{}
	hcConfigMap.Kind = "ConfigMap"
	if err := h.RestoreResource(hcConfigMap, e2eutil.HcType); err != nil {
		return fmt.Errorf("error restoring HC ConfigMaps: %v\n", err)
	}

	hcpNs := &corev1.Namespace{}
	hcpNs.Kind = "Namespace"
	if err := h.RestoreResource(hcpNs, e2eutil.HcpType); err != nil {
		return fmt.Errorf("error restoring HCP Namespace: %v\n", err)
	}

	hcpSecrets := &corev1.Secret{}
	hcpSecrets.Kind = "Secret"
	if err := h.RestoreResource(hcpSecrets, e2eutil.HcpType); err != nil {
		return fmt.Errorf("error restoring HCP Secrets: %v\n", err)
	}

	hostedControlPlane := &hyperv1.HostedControlPlane{}
	hostedControlPlane.Kind = "HostedControlPlane"
	if err := h.RestoreResource(hostedControlPlane, e2eutil.HcpType); err != nil {
		return fmt.Errorf("error restoring HostedControlPlane: %v\n", err)
	}

	hcpAWSCluster := &capiaws.AWSCluster{}
	hcpAWSCluster.Kind = "AWSCluster"
	if err := h.RestoreResource(hcpAWSCluster, e2eutil.HcpType); err != nil {
		return fmt.Errorf("error restoring HCP AWS Cluster: %v\n", err)
	}

	hcpAWSMachineTemplate := &capiaws.AWSMachineTemplate{}
	hcpAWSMachineTemplate.Kind = "AWSMachineTemplate"
	if err := h.RestoreResource(hcpAWSMachineTemplate, e2eutil.HcpType); err != nil {
		return fmt.Errorf("error restoring HCP AWS Machine Template: %v\n", err)
	}

	hcpAWSMachine := &capiaws.AWSMachine{}
	hcpAWSMachine.Kind = "AWSMachine"
	if err := h.RestoreResource(hcpAWSMachine, e2eutil.HcpType); err != nil {
		return fmt.Errorf("error restoring HCP AWS Machine: %v\n", err)
	}

	hcpCluster := &capiv1.Cluster{}
	hcpCluster.Kind = "Cluster"
	if err := h.RestoreResource(hcpCluster, e2eutil.HcpType); err != nil {
		return fmt.Errorf("error restoring HCP Cluster: %v\n", err)
	}

	hcpMachineDeployment := &capiv1.MachineDeployment{}
	hcpMachineDeployment.Kind = "MachineDeployment"
	if err := h.RestoreResource(hcpMachineDeployment, e2eutil.HcpType); err != nil {
		return fmt.Errorf("error restoring HCP MachineDeployment: %v\n", err)
	}

	hcpMachine := &capiv1.Machine{}
	hcpMachine.Kind = "Machine"
	if err := h.RestoreResource(hcpMachine, e2eutil.HcpType); err != nil {
		return fmt.Errorf("error restoring HCP Machine: %v\n", err)
	}

	hcpMachineSet := &capiv1.MachineSet{}
	hcpMachineSet.Kind = "MachineSet"
	if err := h.RestoreResource(hcpMachineSet, e2eutil.HcpType); err != nil {
		return fmt.Errorf("error restoring HCP MachineSet: %v\n", err)
	}

	hc := &hyperv1.HostedCluster{}
	hc.Kind = "HostedCluster"
	if err := h.RestoreETCD(hc, s3Details); err != nil {
		return fmt.Errorf("error restoring ETCD")
	}

	np := &hyperv1.NodePool{}
	np.Kind = "NodePool"
	if err := h.RestoreResource(np, e2eutil.HcType); err != nil {
		return fmt.Errorf("error restoring ETCD")
	}

	return nil
}
