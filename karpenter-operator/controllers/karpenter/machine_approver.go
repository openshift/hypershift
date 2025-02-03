package karpenter

import (
	"context"
	"fmt"
	"os"
	"strings"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"

	awssdk "github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	certificatesv1client "k8s.io/client-go/kubernetes/typed/certificates/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

const (
	nodeBootstrapperUsername = "system:serviceaccount:openshift-machine-config-operator:node-bootstrapper"
)

type MachineApproverController struct {
	client     client.Client
	certClient *certificatesv1client.CertificatesV1Client
}

func (r *MachineApproverController) SetupWithManager(mgr ctrl.Manager) error {
	certClient, err := certificatesv1client.NewForConfig(mgr.GetConfig())
	if err != nil {
		return err
	}
	r.certClient = certClient
	r.client = mgr.GetClient()

	c, err := controller.New("karpenter_machine_approver", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return fmt.Errorf("failed to construct karpenter_machine_approver controller: %w", err)
	}

	csrFilterFn := func(csr *certificatesv1.CertificateSigningRequest) bool {
		if csr.Spec.SignerName != certificatesv1.KubeAPIServerClientKubeletSignerName {
			return false
		}
		// only reconcile pending CSRs (not approved and not denied).
		if !certificates.IsCertificateRequestPending(csr) {
			return false
		}
		// only reconcile kubernetes.io/kube-apiserver-client-kubelet when it is created by the node bootstrapper
		if csr.Spec.Username != nodeBootstrapperUsername {
			mgr.GetLogger().Info("Ignoring csr because it is not from the node bootstrapper", "csr", csr.Name)
			return false
		}
		return true
	}

	if err := c.Watch(source.Kind(
		mgr.GetCache(),
		&certificatesv1.CertificateSigningRequest{},
		&handler.TypedEnqueueRequestForObject[*certificatesv1.CertificateSigningRequest]{},
		predicate.NewTypedPredicateFuncs(csrFilterFn),
	)); err != nil {
		return fmt.Errorf("failed to watch CertificateSigningRequest: %v", err)
	}

	return nil
}

func (r *MachineApproverController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling CSR", "req", req)

	csr := &certificatesv1.CertificateSigningRequest{}
	if err := r.client.Get(ctx, req.NamespacedName, csr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get csr %s: %v", req.NamespacedName, err)
	}

	// Return early if deleted
	if !csr.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// If a CSR is approved/denied after being added to the queue,
	// but before we reconcile it, trying to approve it will result in an error and cause a loop.
	// Return early if the CSR has been approved/denied externally.
	if !certificates.IsCertificateRequestPending(csr) {
		log.Info("CSR is already processed ", "csr", csr.Name)
		return ctrl.Result{}, nil
	}

	ec2Client, err := getEC2Client()
	if err != nil {
		return ctrl.Result{}, err
	}

	authorized, err := r.authorize(ctx, csr, ec2Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	if authorized {
		log.Info("Attempting to approve CSR", "csr", csr.Name)
		if err := r.approve(ctx, csr); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to approve csr %s: %v", csr.Name, err)
		}
	}

	return ctrl.Result{}, nil
}

// TODO: include a creation time window for the nodeclaim, the instance and csr triplets and also ratelimit and short circuit approval based on the number of pending CSRs
func (r *MachineApproverController) authorize(ctx context.Context, csr *certificatesv1.CertificateSigningRequest, ec2Client ec2iface.EC2API) (bool, error) {
	x509cr, err := certificates.ParseCSR(csr.Spec.Request)
	if err != nil {
		return false, err
	}

	nodeName := strings.TrimPrefix(x509cr.Subject.CommonName, "system:node:")
	if len(nodeName) == 0 {
		return false, fmt.Errorf("subject common name does not have a valid node name")
	}

	nodeClaims, err := listNodeClaims(ctx, r.client)
	if err != nil {
		return false, err
	}

	dnsNames, err := getEC2InstancesDNSNames(ctx, nodeClaims, ec2Client)
	if err != nil {
		return false, err
	}

	for _, dnsName := range dnsNames {
		if nodeName == dnsName {
			return true, nil // approve node client cert
		}
	}

	return false, nil
}

func getEC2InstancesDNSNames(ctx context.Context, nodeClaims []karpenterv1.NodeClaim, ec2Client ec2iface.EC2API) ([]string, error) {
	ec2InstanceIDs := []string{}
	for _, claim := range nodeClaims {
		if claim.Status.NodeName != "" {
			// skip if a node is already created for this nodeClaim.
			continue
		}
		providerID := claim.Status.ProviderID
		instanceID := providerID[strings.LastIndex(providerID, "/")+1:]

		ec2InstanceIDs = append(ec2InstanceIDs, instanceID)
	}

	if len(ec2InstanceIDs) == 0 {
		return nil, nil
	}

	output, err := ec2Client.DescribeInstancesWithContext(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: awssdk.StringSlice(ec2InstanceIDs),
	})
	if err != nil {
		return nil, err
	}

	dnsNames := []string{}
	for _, reservation := range output.Reservations {
		for _, instance := range reservation.Instances {
			dnsNames = append(dnsNames, *instance.PrivateDnsName)
		}
	}
	return dnsNames, nil
}

func (r *MachineApproverController) approve(ctx context.Context, csr *certificatesv1.CertificateSigningRequest) error {
	csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1.CertificateSigningRequestCondition{
		Type:    certificatesv1.CertificateApproved,
		Reason:  "KarpenterCSRApprove",
		Message: "Auto approved by karpenter_machine_approver",
		Status:  corev1.ConditionTrue,
	})

	_, err := r.certClient.CertificateSigningRequests().UpdateApproval(ctx, csr.Name, csr, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("error updating approval for csr: %v", err)
	}

	return nil
}

func getEC2Client() (ec2iface.EC2API, error) {
	// AWS_SHARED_CREDENTIALS_FILE and AWS_REGION envvar should be set in operator deployment
	// when reconciling an AWS hosted control plane
	if os.Getenv("AWS_SHARED_CREDENTIALS_FILE") == "" {
		return nil, fmt.Errorf("AWS credentials not set")
	}

	awsSession := awsutil.NewSession("karpenter-operator", "", "", "", "")
	ec2Client := ec2.New(awsSession, awssdk.NewConfig())
	return ec2Client, nil
}

func listNodeClaims(ctx context.Context, client client.Client) ([]karpenterv1.NodeClaim, error) {
	nodeClaimList := &karpenterv1.NodeClaimList{}
	err := client.List(ctx, nodeClaimList)
	if err != nil {
		return nil, fmt.Errorf("failed to list NodeClaims: %w", err)
	}

	return nodeClaimList.Items, nil
}
