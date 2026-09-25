package gcp

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"google.golang.org/api/compute/v1"
)

func TestClassifyOpError(t *testing.T) {
	t.Run("When every error is RESOURCE_NOT_READY, it should be degraded and waiting for infra", func(t *testing.T) {
		g := NewWithT(t)
		m := testManager(nil)
		op := &compute.Operation{Error: &compute.OperationError{Errors: []*compute.OperationErrorErrors{
			{Code: "RESOURCE_NOT_READY"},
		}}}
		result := m.classifyOpError(op, "insert")
		g.Expect(result.Status).To(Equal(OutcomeDegraded))
		g.Expect(result.Reason).To(Equal(hyperv1.GCPFirewallWaitingForInfra))
	})

	t.Run("When every error is PERMISSION_DENIED, it should be degraded with insufficient permissions", func(t *testing.T) {
		g := NewWithT(t)
		m := testManager(nil)
		op := &compute.Operation{Error: &compute.OperationError{Errors: []*compute.OperationErrorErrors{
			{Code: "PERMISSION_DENIED"},
		}}}
		result := m.classifyOpError(op, "insert")
		g.Expect(result.Status).To(Equal(OutcomeDegraded))
		g.Expect(result.Reason).To(Equal(hyperv1.GCPFirewallInsufficientPermissions))
	})

	t.Run("When a recoverable error is mixed with an unrecognized terminal error, it should be an OutcomeError", func(t *testing.T) {
		g := NewWithT(t)
		m := testManager(nil)
		// A single unrecognized code (e.g. INVALID_FIELD_VALUE) can never succeed on
		// retry, so it must not be hidden by an accompanying recoverable code.
		op := &compute.Operation{Error: &compute.OperationError{Errors: []*compute.OperationErrorErrors{
			{Code: "RESOURCE_NOT_READY"},
			{Code: "INVALID_FIELD_VALUE"},
		}}}
		result := m.classifyOpError(op, "insert")
		g.Expect(result.Status).To(Equal(OutcomeError))
		g.Expect(result.Err).To(HaveOccurred())
	})

	t.Run("When there are no errors, it should be an OutcomeError", func(t *testing.T) {
		g := NewWithT(t)
		m := testManager(nil)
		op := &compute.Operation{Error: &compute.OperationError{}}
		result := m.classifyOpError(op, "insert")
		g.Expect(result.Status).To(Equal(OutcomeError))
	})
}
