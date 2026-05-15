package etcdbackupgcs

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestAdaptAlertingRules(t *testing.T) {
	t.Run("When adapting alerting rules it should inject cluster ID label into all rules", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cpContext := component.WorkloadContext{
			HCP: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					ClusterID: "test-cluster-id-123",
				},
			},
		}
		rule := &prometheusoperatorv1.PrometheusRule{
			Spec: prometheusoperatorv1.PrometheusRuleSpec{
				Groups: []prometheusoperatorv1.RuleGroup{
					{
						Name: "etcd-backup.rules",
						Rules: []prometheusoperatorv1.Rule{
							{
								Alert: "EtcdBackupStale",
								Expr:  intstr.FromString(`(time() - kube_cronjob_status_last_successful_time{cronjob="etcd-backup-gcs"}) > 7200`),
								Labels: map[string]string{
									"severity": "warning",
									"_id":      "cluster_id",
								},
							},
							{
								Alert: "EtcdRestoreFailed",
								Expr:  intstr.FromString(`kube_job_status_failed{job_name=~"etcd-restore.*"} > 0`),
								Labels: map[string]string{
									"severity": "critical",
									"_id":      "cluster_id",
								},
							},
						},
					},
				},
			},
		}

		err := adaptAlertingRules(cpContext, rule)
		g.Expect(err).ToNot(HaveOccurred())

		for _, group := range rule.Spec.Groups {
			for _, r := range group.Rules {
				g.Expect(r.Labels["_id"]).To(Equal("test-cluster-id-123"),
					"expected _id label to be set to cluster ID for alert %s", r.Alert)
			}
		}
	})

	t.Run("When adapting alerting rules with nil labels it should create labels map", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cpContext := component.WorkloadContext{
			HCP: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					ClusterID: "test-cluster-id-456",
				},
			},
		}
		rule := &prometheusoperatorv1.PrometheusRule{
			Spec: prometheusoperatorv1.PrometheusRuleSpec{
				Groups: []prometheusoperatorv1.RuleGroup{
					{
						Name: "etcd-backup.rules",
						Rules: []prometheusoperatorv1.Rule{
							{
								Alert: "EtcdBackupStale",
								Expr:  intstr.FromString(`test_expr`),
							},
						},
					},
				},
			},
		}

		err := adaptAlertingRules(cpContext, rule)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(rule.Spec.Groups[0].Rules[0].Labels).ToNot(BeNil())
		g.Expect(rule.Spec.Groups[0].Rules[0].Labels["_id"]).To(Equal("test-cluster-id-456"))
	})
}
