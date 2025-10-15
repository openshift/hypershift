package karpenter

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"

	certificatesv1 "k8s.io/api/certificates/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

type fakeEC2Client struct {
	ec2iface.EC2API

	instances []*ec2.Instance
}

func (fake *fakeEC2Client) DescribeInstancesWithContext(aws.Context, *ec2.DescribeInstancesInput, ...request.Option) (*ec2.DescribeInstancesOutput, error) {
	return &ec2.DescribeInstancesOutput{
		Reservations: []*ec2.Reservation{
			{
				Instances: fake.instances,
			},
		},
	}, nil
}

func TestAuthorizeClientCSR(t *testing.T) {
	fakeNodeClaim := &karpenterv1.NodeClaim{
		Status: karpenterv1.NodeClaimStatus{
			ProviderID: "aws:///fakeproviderID",
		},
	}

	testCases := []struct {
		name      string
		instances []*ec2.Instance
		x509csr   []byte
		objects   []client.Object
		wantErr   string
		authorize bool
	}{
		{
			name:      "When CSR request is invalid it should error",
			x509csr:   []byte("-----BEGIN??\n"),
			wantErr:   "PEM block type must be CERTIFICATE REQUEST",
			authorize: false,
		},
		{
			name:      "When CSR common name is invalid node name it should error",
			x509csr:   createCSR("system:node:"),
			wantErr:   "subject common name does not have a valid node name",
			authorize: false,
		},
		{
			name:      "When there are no nodeClaims it should not be authorized",
			x509csr:   createCSR("system:node:test1"),
			authorize: false,
		},
		{
			name:      "When there are no EC2 instances it should not be authorized",
			x509csr:   createCSR("system:node:test1"),
			objects:   []client.Object{fakeNodeClaim},
			authorize: false,
		},
		{
			name: "When CSR common name does NOT match any EC2 instance PrivateDnsName it should not be authorized",
			instances: []*ec2.Instance{
				{
					PrivateDnsName: aws.String("test2"),
				},
			},
			x509csr:   createCSR("system:node:test1"),
			objects:   []client.Object{fakeNodeClaim},
			authorize: false,
		},
		{
			name: "When CSR common name matches an EC2 instance PrivateDnsName it should be authorized",
			instances: []*ec2.Instance{
				{
					PrivateDnsName: aws.String("test1"),
				},
			},
			x509csr:   createCSR("system:node:test1"),
			objects:   []client.Object{fakeNodeClaim},
			authorize: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme()).
				WithObjects(tc.objects...).
				Build()

			r := &MachineApproverController{
				client: fakeClient,
			}
			fakeEC2Client := &fakeEC2Client{
				instances: tc.instances,
			}

			csr := &certificatesv1.CertificateSigningRequest{
				Spec: certificatesv1.CertificateSigningRequestSpec{
					Request:    tc.x509csr,
					SignerName: certificatesv1.KubeAPIServerClientKubeletSignerName,
				},
			}

			authorized, err := r.authorize(t.Context(), csr, fakeEC2Client)
			if tc.wantErr != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.wantErr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(authorized).To(Equal(tc.authorize))
			}

		})
	}
}

func TestAuthorizeServingCSR(t *testing.T) {
	nodeClaimWithNodeName := func(nodeName string) *karpenterv1.NodeClaim {
		return &karpenterv1.NodeClaim{
			Status: karpenterv1.NodeClaimStatus{
				ProviderID: "aws:///fakeproviderID",
				NodeName:   nodeName,
			},
		}
	}

	testCases := []struct {
		name        string
		csrUserName string
		instances   []*ec2.Instance
		objects     []client.Object
		wantErr     string
		authorize   bool
	}{
		{
			name:        "When CSR username is invalid node name it should error",
			csrUserName: "system:node:",
			wantErr:     "csr username does not have a valid node name",
			authorize:   false,
		},
		{
			name:        "When there are no nodeClaims it should not be authorized",
			csrUserName: "system:node:test1",
			authorize:   false,
		},
		{
			name:        "When there are no nodeClaims that matches the CSR nodeName it should not be authorized",
			csrUserName: "system:node:test1",
			objects:     []client.Object{nodeClaimWithNodeName("test2")},
			authorize:   false,
		},
		{
			name:        "When there are no EC2 instances it should not be authorized",
			csrUserName: "system:node:test1",
			objects:     []client.Object{nodeClaimWithNodeName("test1")},
			authorize:   false,
		},
		{
			name:        "When CSR username does NOT match any EC2 instance PrivateDnsName it should not be authorized",
			csrUserName: "system:node:test1",
			objects:     []client.Object{nodeClaimWithNodeName("test1")},
			instances: []*ec2.Instance{
				{
					PrivateDnsName: aws.String("test2"),
				},
			},
			authorize: false,
		},
		{
			name:        "When CSR username matches an EC2 instance PrivateDnsName it should be authorized",
			csrUserName: "system:node:test1",
			objects:     []client.Object{nodeClaimWithNodeName("test1")},
			instances: []*ec2.Instance{
				{
					PrivateDnsName: aws.String("test1"),
				},
			},
			authorize: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme()).
				WithObjects(tc.objects...).
				Build()

			r := &MachineApproverController{
				client: fakeClient,
			}
			fakeEC2Client := &fakeEC2Client{
				instances: tc.instances,
			}

			csr := &certificatesv1.CertificateSigningRequest{
				Spec: certificatesv1.CertificateSigningRequestSpec{
					Username:   tc.csrUserName,
					SignerName: certificatesv1.KubeletServingSignerName,
				},
			}

			authorized, err := r.authorize(t.Context(), csr, fakeEC2Client)
			if tc.wantErr != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.wantErr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(authorized).To(Equal(tc.authorize))
			}

		})
	}
}

func scheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = hyperv1.AddToScheme(scheme)

	// Register the NodeClaim GVK in the scheme
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{
		Group:   "karpenter.sh",
		Version: "v1",
		Kind:    "NodeClaim",
	}, &karpenterv1.NodeClaim{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{
		Group:   "karpenter.sh",
		Version: "v1",
		Kind:    "NodeClaimList",
	}, &karpenterv1.NodeClaimList{})

	return scheme
}

func createCSR(commonName string) []byte {
	keyBytes, _ := rsa.GenerateKey(rand.Reader, 2048)
	subj := pkix.Name{
		Organization: []string{"system:nodes"},
		CommonName:   commonName,
	}

	template := x509.CertificateRequest{
		Subject:            subj,
		SignatureAlgorithm: x509.SHA256WithRSA,
		IPAddresses:        []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:           []string{"node1", "node1.local"},
	}
	csrOut := new(bytes.Buffer)

	csrBytes, _ := x509.CreateCertificateRequest(rand.Reader, &template, keyBytes)
	_ = pem.Encode(csrOut, &pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrBytes})
	return csrOut.Bytes()
}
