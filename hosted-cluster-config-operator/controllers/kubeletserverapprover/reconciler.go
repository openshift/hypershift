package kubeletserverapprover

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"strings"

	certsv1 "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	certslister "k8s.io/client-go/listers/certificates/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

type KubeletServerApprover struct {
	Lister     certslister.CertificateSigningRequestLister
	KubeClient kubeclient.Interface
	Log        logr.Logger
}

func (a *KubeletServerApprover) Reconcile(_ context.Context, req ctrl.Request) (ctrl.Result, error) {
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
	if !isKubeletServingCert(csr) {
		logger.Info("CSR is not a kubelet serving cert. not processing")
		return ctrl.Result{}, nil
	}
	x509CSRData, err := parseCSR(csr)
	if err != nil {
		logger.Error(err, "failed to extract x509 csr data")
		return ctrl.Result{}, nil
	}
	if x509CSRData == nil {
		logger.Error(fmt.Errorf("x509 csr data is nil"), "")
		return ctrl.Result{}, nil
	}
	nodeName := extractNodeNameFromCSR(x509CSRData)
	nodeData, err := a.KubeClient.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
	if err != nil {
		logger.Error(err, "failed to fetch node data")
		return ctrl.Result{}, nil
	}
	if nodeData == nil {
		logger.Error(fmt.Errorf("node data is nil"), "")
		return ctrl.Result{}, nil
	}
	if !validateAddressData(nodeData, x509CSRData) {
		logger.Error(fmt.Errorf("csr address data does not match kube node data"), "")
		return ctrl.Result{}, nil
	}
	if !isCreatedByNodeClient(csr, nodeData) {
		logger.Error(fmt.Errorf("csr wasn't created by a node client"), "")
		return ctrl.Result{}, nil
	}
	err = a.approveCSR(csr)
	return ctrl.Result{}, err
}

func (a *KubeletServerApprover) approveCSR(csr *certsv1.CertificateSigningRequest) error {
	csr.Status.Conditions = append(csr.Status.Conditions, certsv1.CertificateSigningRequestCondition{
		Type:           certsv1.CertificateApproved,
		Reason:         "KubectlApprove",
		Message:        "This CSR was approved by the kubelet serving csr approver.",
		LastUpdateTime: metav1.Now(),
		Status:         v1.ConditionTrue,
	})
	var _, err = a.KubeClient.CertificatesV1().CertificateSigningRequests().UpdateApproval(context.TODO(), csr.Name, csr, metav1.UpdateOptions{})
	return err
}

func isApproved(csr *certsv1.CertificateSigningRequest) bool {
	for _, c := range csr.Status.Conditions {
		if c.Type == certsv1.CertificateApproved && c.Status == v1.ConditionTrue {
			return true
		}
	}
	return false
}

func isKubeletServingCert(csr *certsv1.CertificateSigningRequest) bool {
	return csr.Spec.SignerName == certsv1.KubeletServingSignerName
}

func isCreatedByNodeClient(csr *certsv1.CertificateSigningRequest, nodeData *v1.Node) bool {
	return csr.Spec.Username == fmt.Sprintf("system:node:%s", nodeData.Name)
}

func extractNodeNameFromCSR(csrData *x509.CertificateRequest) string {
	nodeNameRaw := strings.Split(csrData.Subject.CommonName, "system:node:")
	return nodeNameRaw[len(nodeNameRaw)-1]
}

// validateAddressData
func validateAddressData(nodeData *v1.Node, csrData *x509.CertificateRequest) bool {
	nodeAddressMap := map[string]bool{}
	for _, nodeAddress := range nodeData.Status.Addresses {
		if len(nodeAddress.Address) > 0 {
			nodeAddressMap[nodeAddress.Address] = true
		}
	}
	csrAddressMap := map[string]bool{}
	for _, csrAddress := range csrData.IPAddresses {
		csrAddressMap[csrAddress.String()] = true
	}
	for _, csrAddress := range csrData.DNSNames {
		csrAddressMap[csrAddress] = true
	}
	// key observation: any address reported by the csr MUST be contained in the node address map.
	// if it isn't it cannot be approved
	for csrAddressValue := range csrAddressMap {
		if _, ok := nodeAddressMap[csrAddressValue]; !ok {
			return false
		}
	}
	return true
}

// parseCSR extracts the CSR from the API object and decodes it.
func parseCSR(csr *certsv1.CertificateSigningRequest) (*x509.CertificateRequest, error) {
	// extract PEM from request object
	block, _ := pem.Decode(csr.Spec.Request)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf("PEM block type must be CERTIFICATE REQUEST")
	}
	return x509.ParseCertificateRequest(block.Bytes)
}
