package autoapprover

import (
	"context"

	"github.com/go-logr/logr"

	certsv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	certsv1lister "k8s.io/client-go/listers/certificates/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

type AutoApprover struct {
	Lister     certsv1lister.CertificateSigningRequestLister
	KubeClient kubeclient.Interface
	Log        logr.Logger
}

func (a *AutoApprover) Reconcile(_ context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := a.Log.WithValues("csr", req.NamespacedName.String())
	logger.Info("Start reconcile")
	csr, err := a.Lister.Get(req.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	if isApproved(csr) {
		logger.Info("CSR is already approved")
		return ctrl.Result{}, nil
	}

	logger.Info("Approving CSR")
	err = a.approveCSR(csr)

	return ctrl.Result{}, err
}

func (a *AutoApprover) approveCSR(csr *certsv1.CertificateSigningRequest) error {
	csr.Status.Conditions = append(csr.Status.Conditions, certsv1.CertificateSigningRequestCondition{
		Type:           certsv1.CertificateApproved,
		Status:         corev1.ConditionTrue,
		Reason:         "KubectlApprove",
		Message:        "This CSR was automatically approved.",
		LastUpdateTime: metav1.Now(),
	})
	var _, err = a.KubeClient.CertificatesV1().CertificateSigningRequests().UpdateApproval(context.TODO(), csr.GetName(), csr, metav1.UpdateOptions{})
	return err
}

func isApproved(csr *certsv1.CertificateSigningRequest) bool {
	for _, c := range csr.Status.Conditions {
		if c.Type == certsv1.CertificateApproved {
			return true
		}
	}
	return false
}
