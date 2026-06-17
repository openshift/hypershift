package hostedcluster

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestReconcileReport(t *testing.T) {
	t.Run("When no operations are recorded it should report no errors", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}

		g.Expect(report.hasCriticalFailure()).To(BeFalse())
		g.Expect(report.allErrors()).To(BeEmpty())
		g.Expect(report.aggregate()).To(BeNil())
		g.Expect(report.logSummary()).To(BeEmpty())
		g.Expect(report.criticalFailureNames()).To(BeEmpty())
		g.Expect(report.blockedNames()).To(BeEmpty())
	})

	t.Run("When all operations succeed it should report no errors", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.execute("PullSecretSync", critical, func() error { return nil })
		report.execute("SSHKeySync", nonCritical, func() error { return nil })

		g.Expect(report.hasCriticalFailure()).To(BeFalse())
		g.Expect(report.allErrors()).To(BeEmpty())
		g.Expect(report.aggregate()).To(BeNil())
		g.Expect(report.logSummary()).To(BeEmpty())
	})

	t.Run("When a critical operation fails it should report critical failure", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.execute("PullSecretSync", critical, func() error {
			return fmt.Errorf("secret not found")
		})
		report.execute("SSHKeySync", nonCritical, func() error { return nil })

		g.Expect(report.hasCriticalFailure()).To(BeTrue())
		g.Expect(report.criticalFailureNames()).To(ConsistOf("PullSecretSync"))
		g.Expect(report.allErrors()).To(HaveLen(1))
		g.Expect(report.aggregate()).To(HaveOccurred())
	})

	t.Run("When a non-critical operation fails it should not report critical failure", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.execute("PullSecretSync", critical, func() error { return nil })
		report.execute("SSHKeySync", nonCritical, func() error {
			return fmt.Errorf("key not found")
		})

		g.Expect(report.hasCriticalFailure()).To(BeFalse())
		g.Expect(report.criticalFailureNames()).To(BeEmpty())
		g.Expect(report.allErrors()).To(HaveLen(1))
		g.Expect(report.aggregate()).To(HaveOccurred())
	})

	t.Run("When operations are blocked it should track blocked names", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.execute("PullSecretSync", critical, func() error {
			return fmt.Errorf("secret not found")
		})
		report.executeOrBlock("OperatorDeployments", func() error { return nil })
		report.executeOrBlock("RBACAndPolicies", func() error { return nil })

		g.Expect(report.hasCriticalFailure()).To(BeTrue())
		g.Expect(report.blockedNames()).To(ConsistOf("OperatorDeployments", "RBACAndPolicies"))
		g.Expect(report.allErrors()).To(HaveLen(1))
		g.Expect(report.aggregate()).To(HaveOccurred())
		g.Expect(report.aggregate().Error()).To(ContainSubstring("secret not found"))
		g.Expect(report.aggregate().Error()).To(ContainSubstring("blocked operations: [OperatorDeployments, RBACAndPolicies]"))
	})

	t.Run("When blocked operations are recorded it should not count as critical failure", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.execute("PullSecretSync", critical, func() error { return nil })
		report.executeOrBlock("CoreHCPChain", func() error { return nil })

		g.Expect(report.hasCriticalFailure()).To(BeFalse())
		g.Expect(report.blockedNames()).To(BeEmpty())
	})

	t.Run("When multiple critical operations fail it should deduplicate names", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.execute("PullSecretSync", critical, func() error {
			return fmt.Errorf("error 1")
		})
		report.execute("PlatformCredentials", critical, func() error {
			return fmt.Errorf("error 2")
		})

		g.Expect(report.criticalFailureNames()).To(ConsistOf("PullSecretSync", "PlatformCredentials"))
	})

	t.Run("When requeueAfter is set it should be preserved", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		d := 5 * time.Minute
		report.requeueAfter = &d

		g.Expect(report.requeueAfter).ToNot(BeNil())
		g.Expect(*report.requeueAfter).To(Equal(5 * time.Minute))
	})
}

func TestBlockingBehavior(t *testing.T) {
	t.Run("When a non-critical operation fails it should allow subsequent execute calls", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.execute("SSHKeySync", nonCritical, func() error {
			return fmt.Errorf("key not found")
		})
		executed := false
		report.execute("GlobalConfigSync", nonCritical, func() error {
			executed = true
			return nil
		})

		g.Expect(executed).To(BeTrue())
		g.Expect(report.blockedNames()).To(BeEmpty())
		g.Expect(report.allErrors()).To(HaveLen(1))
	})

	t.Run("When a non-critical operation fails it should allow executeOrBlock calls", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.execute("SSHKeySync", nonCritical, func() error {
			return fmt.Errorf("key not found")
		})
		executed := false
		report.executeOrBlock("OperatorDeployments", func() error {
			executed = true
			return nil
		})

		g.Expect(executed).To(BeTrue())
		g.Expect(report.blockedNames()).To(BeEmpty())
	})

	t.Run("When a critical operation fails it should block executeOrBlock calls", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.execute("PullSecretSync", critical, func() error {
			return fmt.Errorf("secret not found")
		})
		executed := false
		report.executeOrBlock("OperatorDeployments", func() error {
			executed = true
			return nil
		})

		g.Expect(executed).To(BeFalse())
		g.Expect(report.blockedNames()).To(ConsistOf("OperatorDeployments"))
	})
}

func TestExecuteOrBlock(t *testing.T) {
	t.Run("When no critical failure exists it should execute the function", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		executed := false
		report.executeOrBlock("Op", func() error {
			executed = true
			return nil
		})

		g.Expect(executed).To(BeTrue())
		g.Expect(report.blockedNames()).To(BeEmpty())
	})

	t.Run("When a critical failure exists it should block and not execute the function", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.execute("CritOp", critical, func() error {
			return fmt.Errorf("critical failure")
		})
		executed := false
		report.executeOrBlock("Op", func() error {
			executed = true
			return nil
		})

		g.Expect(executed).To(BeFalse())
		g.Expect(report.blockedNames()).To(ConsistOf("Op"))
	})

	t.Run("When no critical failure exists it should record the error", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.executeOrBlock("Op", func() error {
			return fmt.Errorf("err1")
		})

		g.Expect(report.allErrors()).To(HaveLen(1))
		g.Expect(report.blockedNames()).To(BeEmpty())
	})

	t.Run("When a critical failure exists it should block and not execute", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.execute("CritOp", critical, func() error {
			return fmt.Errorf("critical failure")
		})
		report.executeOrBlock("Op", func() error {
			t.Fatal("should not be called")
			return nil
		})

		g.Expect(report.blockedNames()).To(ConsistOf("Op"))
	})
}

func TestLogSummary(t *testing.T) {
	t.Run("When only critical failures exist it should format critical failures", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.execute("PullSecretSync", critical, func() error {
			return fmt.Errorf("not found")
		})
		report.execute("PlatformCredentials", critical, func() error {
			return fmt.Errorf("invalid")
		})

		msg := report.logSummary()
		g.Expect(msg).To(Equal("critical failures: [PlatformCredentials, PullSecretSync]"))
	})

	t.Run("When only blocked operations exist (no critical failure) it should return empty", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.executeOrBlock("OperatorDeployments", func() error { return nil })
		report.executeOrBlock("Auxiliary", func() error { return nil })

		g.Expect(report.logSummary()).To(BeEmpty())
	})

	t.Run("When both critical failures and blocked operations exist it should format both", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.execute("PullSecretSync", critical, func() error {
			return fmt.Errorf("not found")
		})
		report.executeOrBlock("OperatorDeployments", func() error { return nil })
		report.executeOrBlock("Auxiliary", func() error { return nil })

		msg := report.logSummary()
		g.Expect(msg).To(Equal("critical failures: [PullSecretSync]; blocked operations: [Auxiliary, OperatorDeployments]"))
	})

	t.Run("When no failures or blocked operations exist it should return empty string", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.execute("PullSecretSync", critical, func() error { return nil })
		report.execute("SSHKeySync", nonCritical, func() error { return nil })

		g.Expect(report.logSummary()).To(BeEmpty())
	})

	t.Run("When only non-critical failures exist it should return empty string", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.execute("SSHKeySync", nonCritical, func() error {
			return fmt.Errorf("key not found")
		})

		g.Expect(report.logSummary()).To(BeEmpty())
	})
}

func TestAggregate(t *testing.T) {
	t.Run("When critical failure exists it should return critical errors and blocked list", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.execute("PullSecretSync", critical, func() error {
			return fmt.Errorf("pull secret not found")
		})
		report.execute("SSHKeySync", nonCritical, func() error {
			return fmt.Errorf("ssh key not found")
		})
		report.executeOrBlock("OperatorDeployments", func() error { return nil })

		err := report.aggregate()
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("pull secret not found"))
		g.Expect(err.Error()).To(ContainSubstring("blocked operations: [OperatorDeployments]"))
		g.Expect(err.Error()).NotTo(ContainSubstring("ssh key"))
	})

	t.Run("When no critical failure exists it should return all errors", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.execute("SSHKeySync", nonCritical, func() error {
			return fmt.Errorf("ssh key not found")
		})
		report.execute("AuditWebhook", nonCritical, func() error {
			return fmt.Errorf("webhook not found")
		})

		err := report.aggregate()
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("ssh key not found"))
		g.Expect(err.Error()).To(ContainSubstring("webhook not found"))
	})

	t.Run("When no errors exist it should return nil", func(t *testing.T) {
		g := NewWithT(t)
		report := &reconcileReport{}
		report.execute("PullSecretSync", critical, func() error { return nil })
		report.execute("SSHKeySync", nonCritical, func() error { return nil })

		g.Expect(report.aggregate()).To(BeNil())
	})
}
