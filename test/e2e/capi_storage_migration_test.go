//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/onsi/gomega"

	dto "github.com/prometheus/client_model/go"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	crdassets "github.com/openshift/hypershift/cmd/install/assets/crds"
	capicrdmigrator "github.com/openshift/hypershift/support/capi-crdmigrator"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestCAPIStorageVersionMigration validates that the CAPI CRD storage version migration
// from v1beta1 to v1beta2 completes successfully on a cluster with existing CAPI resources
// and that the hosted cluster remains healthy after migration.
func TestCAPIStorageVersionMigration(t *testing.T) {
	if !globalOpts.RunCAPIMigrationTest {
		t.SkipNow()
	}

	t.Parallel()
	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	t.Log("Starting CAPI storage version migration test")

	// Install HO with migration disabled so CRDs start at v1beta1.
	g := gomega.NewWithT(t)
	disabledOpts := globalOpts.HOInstallationOptions
	disabledOpts.DisableCAPIMigration = true
	err := e2eutil.InstallHyperShiftOperator(ctx, disabledOpts)
	g.Expect(err).ToNot(gomega.HaveOccurred(), "installing HO with migration disabled")
	t.Log("HO installed with CAPI migration disabled")

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g gomega.Gomega, mc crclient.Client, hc *hyperv1.HostedCluster) {
		t.Logf("HostedCluster %s created", crclient.ObjectKeyFromObject(hc))
		_ = e2eutil.WaitForGuestClient(t, ctx, mc, hc)
		t.Log("Hosted cluster is ready")

		// Verify pre-migration state: CAPI CRDs should have v1beta1 in storedVersions
		g.Expect(t.Run("When checking pre-migration CRD state, it should have v1beta1 storedVersions", func(t *testing.T) {
			g := gomega.NewWithT(t)
			for _, crdName := range crdassets.CAPICRDNames() {
				crd := &apiextensionsv1.CustomResourceDefinition{}
				err := mc.Get(ctx, crclient.ObjectKey{Name: crdName}, crd)
				g.Expect(err).ToNot(gomega.HaveOccurred(), "getting CRD %s", crdName)
				g.Expect(crd.Status.StoredVersions).To(gomega.ContainElement("v1beta1"),
					"CRD %s should have v1beta1 in storedVersions before migration", crdName)
				t.Logf("CRD %s storedVersions: %v", crdName, crd.Status.StoredVersions)
			}
		})).To(gomega.BeTrue())

		// Reinstall HO to trigger default v1beta2 storage migration
		g.Expect(t.Run("When reinstalling HO with default CAPI migration, it should succeed", func(t *testing.T) {
			t.Log("Reinstalling HyperShift Operator with default CAPI storage migration")
			installOpts := globalOpts.HOInstallationOptions
			err := e2eutil.InstallHyperShiftOperator(ctx, installOpts)
			g.Expect(err).ToNot(gomega.HaveOccurred(), "reinstalling HO with v1beta2 storage version")
		})).To(gomega.BeTrue())

		// Wait for migration to complete: storedVersions should become ["v1beta2"] only
		g.Expect(t.Run("When waiting for migration, it should update storedVersions to v1beta2", func(t *testing.T) {
			gt := gomega.NewWithT(t)
			for _, crdName := range crdassets.CAPICRDNames() {
				t.Logf("Waiting for CRD %s storedVersions to be migrated", crdName)
				gt.Eventually(func(g gomega.Gomega) {
					crd := &apiextensionsv1.CustomResourceDefinition{}
					err := mc.Get(ctx, crclient.ObjectKey{Name: crdName}, crd)
					g.Expect(err).ToNot(gomega.HaveOccurred())
					g.Expect(crd.Status.StoredVersions).To(gomega.Equal([]string{"v1beta2"}),
						"CRD %s storedVersions should be [v1beta2] after migration", crdName)
				}, "15m", "5s").Should(gomega.Succeed(), "CRD %s migration should complete", crdName)
			}
		})).To(gomega.BeTrue())

		// Verify migration annotation is set
		g.Expect(t.Run("When checking migration annotations, it should have observed-generation set", func(t *testing.T) {
			g := gomega.NewWithT(t)
			for _, crdName := range crdassets.CAPICRDNames() {
				crd := &apiextensionsv1.CustomResourceDefinition{}
				err := mc.Get(ctx, crclient.ObjectKey{Name: crdName}, crd)
				g.Expect(err).ToNot(gomega.HaveOccurred())
				_, hasAnnotation := crd.Annotations[capicrdmigrator.CRDMigrationObservedGenerationAnnotation]
				g.Expect(hasAnnotation).To(gomega.BeTrue(),
					"CRD %s should have migration observed-generation annotation", crdName)
				t.Logf("CRD %s has migration annotation", crdName)
			}
		})).To(gomega.BeTrue())

		// Verify migration status ConfigMap
		g.Expect(t.Run("When checking migration status ConfigMap, it should report complete", func(t *testing.T) {
			gt := gomega.NewWithT(t)
			expectedTotal := len(crdassets.CAPICRDNames())
			gt.Eventually(func(g gomega.Gomega) {
				cm := &corev1.ConfigMap{}
				err := mc.Get(ctx, crclient.ObjectKey{Namespace: "hypershift", Name: capicrdmigrator.StatusConfigMapName}, cm)
				g.Expect(err).ToNot(gomega.HaveOccurred(), "getting migration status ConfigMap")

				var status capicrdmigrator.CAPIMigrationStatus
				err = json.Unmarshal([]byte(cm.Data["status"]), &status)
				g.Expect(err).ToNot(gomega.HaveOccurred(), "unmarshalling migration status")

				g.Expect(status.TotalCRDs).To(gomega.Equal(expectedTotal),
					"TotalCRDs should match number of configured CAPI CRDs")
				g.Expect(status.MigratedCRDs).To(gomega.Equal(expectedTotal),
					"MigratedCRDs should equal TotalCRDs after migration")

				complete := findConditionInStatus(status.Conditions, capicrdmigrator.MigrationCompleteCondition)
				g.Expect(complete).ToNot(gomega.BeNil(), "MigrationComplete condition should exist")
				g.Expect(complete.Status).To(gomega.Equal(metav1.ConditionTrue), "MigrationComplete should be True")

				progressing := findConditionInStatus(status.Conditions, capicrdmigrator.ProgressingCondition)
				g.Expect(progressing).ToNot(gomega.BeNil(), "Progressing condition should exist")
				g.Expect(progressing.Status).To(gomega.Equal(metav1.ConditionFalse), "Progressing should be False")

				degraded := findConditionInStatus(status.Conditions, capicrdmigrator.DegradedCondition)
				g.Expect(degraded).ToNot(gomega.BeNil(), "Degraded condition should exist")
				g.Expect(degraded.Status).To(gomega.Equal(metav1.ConditionFalse), "Degraded should be False")

				t.Logf("Migration status: %d/%d CRDs migrated, MigrationComplete=%s",
					status.MigratedCRDs, status.TotalCRDs, complete.Status)
			}, "5m", "5s").Should(gomega.Succeed(), "migration status ConfigMap should report complete")
		})).To(gomega.BeTrue())

		// Verify migration metrics
		g.Expect(t.Run("When checking migration metrics, it should report accurate values", func(t *testing.T) {
			gt := gomega.NewWithT(t)
			expectedTotal := float64(len(crdassets.CAPICRDNames()))
			gt.Eventually(func(g gomega.Gomega) {
				mf, err := e2eutil.GetMetricsFromPod(ctx, mc, "operator", "operator", "hypershift", "9000")
				g.Expect(err).ToNot(gomega.HaveOccurred(), "getting metrics from operator pod")

				var migrationMetrics []string
				for name := range mf {
					if strings.Contains(name, "migration") {
						migrationMetrics = append(migrationMetrics, name)
					}
				}
				t.Logf("Found %d total metric families, migration-related: %v", len(mf), migrationMetrics)

				totalCRDs := getGaugeValue(mf, "hypershift_capi_migration_total_crds")
				g.Expect(totalCRDs).ToNot(gomega.BeNil(), "hypershift_capi_migration_total_crds metric should exist")
				g.Expect(*totalCRDs).To(gomega.Equal(expectedTotal),
					"total CRDs metric should match configured CAPI CRDs count")

				migratedCRDs := getGaugeValue(mf, "hypershift_capi_migration_migrated_crds")
				g.Expect(migratedCRDs).ToNot(gomega.BeNil(), "hypershift_capi_migration_migrated_crds metric should exist")
				g.Expect(*migratedCRDs).To(gomega.Equal(expectedTotal),
					"migrated CRDs metric should equal total after migration")

				t.Logf("Migration metrics: total=%.0f, migrated=%.0f", *totalCRDs, *migratedCRDs)
			}, "5m", "5s").Should(gomega.Succeed(), "migration metrics should report accurate values")
		})).To(gomega.BeTrue())

		// Check HO logs for CRD migrator errors
		g.Expect(t.Run("When checking HO logs, it should have no CRD migrator errors", func(t *testing.T) {
			g := gomega.NewWithT(t)
			checkHOLogsForMigratorErrors(ctx, t, g)
		})).To(gomega.BeTrue())

		// Verify hosted cluster is still healthy
		g.Expect(t.Run("When verifying post-migration cluster health, it should still be operational", func(t *testing.T) {
			g := gomega.NewWithT(t)

			// NodePools should still exist and be healthy
			nodePools := &hyperv1.NodePoolList{}
			err := mc.List(ctx, nodePools, crclient.InNamespace(hc.Namespace))
			g.Expect(err).ToNot(gomega.HaveOccurred())
			g.Expect(nodePools.Items).ToNot(gomega.BeEmpty(), "should have NodePools after migration")

			for _, np := range nodePools.Items {
				t.Logf("NodePool %s: replicas=%d, ready=%d",
					np.Name, *np.Spec.Replicas, np.Status.Replicas)
				g.Expect(np.Status.Replicas).To(gomega.Equal(*np.Spec.Replicas),
					"NodePool %s should have all replicas ready", np.Name)
			}

			// Guest client should still work
			_ = e2eutil.WaitForGuestClient(t, ctx, mc, hc)
			t.Log("Hosted cluster is still accessible after migration")
		})).To(gomega.BeTrue())
	}).WithHOUpgrade().Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "capi-storage-migration", globalOpts.ServiceAccountSigningKey)
}

func findConditionInStatus(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}

func getGaugeValue(mf map[string]*dto.MetricFamily, name string) *float64 {
	family, ok := mf[name]
	if !ok || family == nil {
		return nil
	}
	for _, m := range family.GetMetric() {
		if g := m.GetGauge(); g != nil {
			v := g.GetValue()
			return &v
		}
	}
	return nil
}

func checkHOLogsForMigratorErrors(ctx context.Context, t *testing.T, g gomega.Gomega) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		cfg, err = e2eutil.GetConfig()
		g.Expect(err).ToNot(gomega.HaveOccurred(), "getting kubeconfig")
	}
	kubeClient, err := kubeclient.NewForConfig(cfg)
	g.Expect(err).ToNot(gomega.HaveOccurred(), "creating kube client")

	pods, err := kubeClient.CoreV1().Pods("hypershift").List(ctx, metav1.ListOptions{
		LabelSelector: "name=operator",
	})
	g.Expect(err).ToNot(gomega.HaveOccurred(), "listing HO pods")
	g.Expect(pods.Items).ToNot(gomega.BeEmpty(), "should find HO pods")

	for _, pod := range pods.Items {
		req := kubeClient.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{})
		stream, err := req.Stream(ctx)
		g.Expect(err).ToNot(gomega.HaveOccurred(), "streaming logs from pod %s", pod.Name)

		scanner := bufio.NewScanner(stream)
		var migratorErrors []string
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, `"controller":"crdmigrator"`) && strings.Contains(line, `"level":"error"`) {
				migratorErrors = append(migratorErrors, line)
			}
		}
		stream.Close()
		g.Expect(scanner.Err()).ToNot(gomega.HaveOccurred(), "scanning HO logs")

		g.Expect(migratorErrors).To(gomega.BeEmpty(),
			fmt.Sprintf("HO pod %s should have no CRD migrator errors, found:\n%s",
				pod.Name, strings.Join(migratorErrors, "\n")))
		t.Logf("HO pod %s: no CRD migrator errors found in logs", pod.Name)
	}
}
