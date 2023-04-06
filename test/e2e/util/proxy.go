package util

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	. "github.com/onsi/gomega"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const CA_CONFIGMAP_KEY = "ca-bundle.crt"

func CreateProxyInstance(t *testing.T, ctx context.Context, client crclient.Client, opts core.CreateOptions, hostedCluster *hyperv1.HostedCluster) *v1.ProxySpec {
	g := NewWithT(t)

	infraOptions := awsinfra.CreateInfraOptions{
		Name:    "ssl-http-proxy",
		InfraID: opts.InfraID,
	}

	if hostedCluster.Spec.Platform.AWS.CloudProviderConfig.Subnet == nil || hostedCluster.Spec.Platform.AWS.CloudProviderConfig.Subnet.ID == nil {
		t.Errorf("AWS.Subnet is not set")
	}
	subnetID := hostedCluster.Spec.Platform.AWS.CloudProviderConfig.Subnet.ID

	if hostedCluster.Spec.Platform.AWS.CloudProviderConfig == nil {
		t.Errorf("AWS.CloudProviderConfig is not set")
	}
	vpcID := hostedCluster.Spec.Platform.AWS.CloudProviderConfig.VPC

	ec2Client := Ec2Client(opts.AWSPlatform.AWSCredentialsFile, opts.AWSPlatform.Region)
	res, err := ec2Client.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("group-name"),
				Values: aws.StringSlice([]string{awsinfra.ProxySecurityGroupName}),
			},
			{
				Name:   aws.String("vpc-id"),
				Values: aws.StringSlice([]string{vpcID}),
			},
		},
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(res.SecurityGroups).ToNot(BeEmpty())

	// generate self-signed CA
	ca, key, err := generateCACert()
	g.Expect(err).ToNot(HaveOccurred())

	caAndKey := strings.Join([]string{key, ca}, "")

	userData := []byte(fmt.Sprintf(mitmProxyScript, caAndKey))
	proxyAddr, err := infraOptions.CreateProxyHost(ctx, opts.Log, ec2Client, *subnetID, *res.SecurityGroups[0].GroupId, userData)
	g.Expect(err).ToNot(HaveOccurred(), "failed to create proxy")

	caConfigMap := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "proxy-ca-bundle",
			Namespace: hostedCluster.Namespace},
		Data: map[string]string{
			CA_CONFIGMAP_KEY: ca,
		},
	}

	err = client.Create(ctx, &caConfigMap)
	g.Expect(err).ToNot(HaveOccurred(), "failed to create CA configmap")

	return &v1.ProxySpec{
		HTTPProxy:  proxyAddr,
		HTTPSProxy: proxyAddr,
		TrustedCA: v1.ConfigMapNameReference{
			Name: caConfigMap.Name,
		},
	}
}

func generateCACert() (string, string, error) {
	// openssl req -new -newkey rsa:2048 -sha256 -days 365 -nodes -x509 -extensions v3_ca -keyout mitmproxy-ca.pem -out mitmproxy-ca.pem
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(2019),
		Subject: pkix.Name{
			Organization: []string{"openshift"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	caPrivKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}

	caBytes, err := x509.CreateCertificate(rand.Reader, ca, ca, &caPrivKey.PublicKey, caPrivKey)
	if err != nil {
		return "", "", err
	}

	caPEM := new(bytes.Buffer)
	pem.Encode(caPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})

	caPrivKeyPEM := new(bytes.Buffer)
	pem.Encode(caPrivKeyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(caPrivKey),
	})

	return caPEM.String(), caPrivKeyPEM.String(), nil
}

const mitmProxyScript = `#!/bin/bash
wget https://snapshots.mitmproxy.org/7.0.2/mitmproxy-7.0.2-linux.tar.gz
mkdir -p /home/ec2-user/mitm
tar zxvf mitmproxy-7.0.2-linux.tar.gz -C /home/ec2-user/mitm

# Copy generated CA
echo '%s' > /home/ec2-user/mitm/mitmproxy-ca.pem

nohup /home/ec2-user/mitm/mitmdump --showhost --ssl-insecure --ignore-hosts quay.io --ignore-hosts registry.redhat.io \
  --set confdir=/home/ec2-user/mitm --set listen_port=3128  > /home/ec2-user/mitm/mitm.log  &
`

const squidProxyWithSSLScript = `#!/bin/bash
yum install -y squid

# Copy generated CA
mkdir /etc/squid/ssl_cert/
echo '%s' > /etc/squid/ssl_cert/myCA.pem
chown -R squid:squid /etc/squid/ssl_cert/

openssl dhparam -outform PEM -out /etc/squid/bump_dhparam.pem 2048
chown squid:squid /etc/squid/bump*
chmod 400 /etc/squid/bump*

# Create SSL db
/usr/lib64/squid/ssl_crtd -c -s /var/lib/ssl_db
chown -R squid:squid /var/lib/ssl_db

# By default, squid only allows connect on port 443
sed -E 's/(^http_access deny CONNECT.*)/#\1/' -i /etc/squid/squid.conf

# Setup intermediate CA and ssl bump
sed -E '/^http_port 3128.*/c\
http_port 3128 ssl-bump generate-host-certificates=on dynamic_cert_mem_cache_size=20MB cert=/etc/squid/ssl_cert/myCA.pem cipher=HIGH:MEDIUM:!LOW:!RC4:!SEED:!IDEA:!3DES:!MD5:!EXP:!PSK:!DSS options=NO_TLSv1,NO_SSLv3,NO_SSLv2,SINGLE_DH_USE,SINGLE_ECDH_USE tls-dh=prime256v1:/etc/squid/bump_dhparam.pem\
https_port 3129 intercept ssl-bump generate-host-certificates=on dynamic_cert_mem_cache_size=4MB cert=/etc/squid/ssl_cert/myCA.pem cipher=HIGH:MEDIUM:!LOW:!RC4:!SEED:!IDEA:!3DES:!MD5:!EXP:!PSK:!DSS options=NO_TLSv1,NO_SSLv3,NO_SSLv2,SINGLE_DH_USE,SINGLE_ECDH_USE tls-dh=prime256v1:/etc/squid/bump_dhparam.pem\
\
sslcrtd_program /usr/lib64/squid/ssl_crtd -s /var/lib/ssl_db -M 20MB\
sslproxy_cert_error allow all\
ssl_bump stare all' -i /etc/squid/squid.conf

systemctl enable --now squid
`
