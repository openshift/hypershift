package aws

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudformation"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/aws/aws-sdk-go/service/route53/route53iface"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3iface"
	"github.com/spf13/cobra"
	"gopkg.in/square/go-jose.v2"
	"k8s.io/apimachinery/pkg/util/wait"

	hypercf "github.com/openshift/hypershift/cmd/infra/aws/cloudformation"
)

type CreateInfraOptions struct {
	AWSCredentialsFile string

	InfraID        string
	Region         string
	BaseDomain     string
	Subdomain      string
	AdditionalTags []string

	PreserveOnFailure bool
}

type CreateInfraOutput struct {
	StackID                  string `json:"stackID"`
	Region                   string `json:"region"`
	Zone                     string `json:"zone"`
	InfraID                  string `json:"infraID"`
	ComputeCIDR              string `json:"computeCIDR"`
	VPCID                    string `json:"vpcID"`
	PrivateSubnetID          string `json:"privateSubnetID"`
	PublicSubnetID           string `json:"publicSubnetID"`
	WorkerSecurityGroupID    string `json:"workerSecurityGroupID"`
	WorkerInstanceProfileID  string `json:"workerInstanceProfileID"`
	BaseDomainZoneID         string `json:"baseDomainZoneID"`
	Subdomain                string `json:"subdomain"`
	SubdomainPrivateZoneID   string `json:"subdomainPrivateZoneID"`
	SubdomainPublicZoneID    string `json:"subdomainPublicZoneID"`
	OIDCIngressRoleArn       string `json:"oidcIngressRoleArn"`
	OIDCImageRegistryRoleArn string `json:"oidcImageRegistryRoleArn"`
	OIDCCSIDriverRoleArn     string `json:"oidcCSIDriverRoleArn"`
	OIDCIssuerURL            string `json:"oidcIssuerURL"`
	OIDCBucketName           string `json:"oidcBucketName"`
	ServiceAccountSigningKey []byte `json:"serviceAccountSigningKey"`
}

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "aws",
		Short: "Creates AWS infrastructure resources for a cluster",
	}

	opts := CreateInfraOptions{
		Region:            "us-east-1",
		PreserveOnFailure: false,
	}

	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Cluster ID with which to tag AWS resources (required)")
	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region where cluster infra should be created")
	cmd.Flags().StringSliceVar(&opts.AdditionalTags, "additional-tags", opts.AdditionalTags, "Additional tags to set on AWS resources")
	cmd.Flags().StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, "The base domain for the cluster")
	cmd.Flags().StringVar(&opts.Subdomain, "subdomain", opts.Subdomain, "The subdomain for the cluster")
	cmd.Flags().BoolVar(&opts.PreserveOnFailure, "preserve-on-failure", opts.PreserveOnFailure, "Preserve the stack if creation fails and is rolled back")

	cmd.MarkFlagRequired("infra-id")
	cmd.MarkFlagRequired("aws-creds")
	cmd.MarkFlagRequired("base-domain")
	cmd.MarkFlagRequired("subdomain")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()
		output, err := opts.Run(ctx)
		if err != nil {
			return err
		}
		data, err := json.Marshal(output)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	return cmd
}

func (o *CreateInfraOptions) Run(ctx context.Context) (*CreateInfraOutput, error) {
	awsConfig := aws.NewConfig().WithRegion(o.Region).WithCredentials(credentials.NewSharedCredentials(o.AWSCredentialsFile, "default"))
	awsSession := session.Must(session.NewSession())
	cf := cloudformation.New(awsSession, awsConfig)
	s3client := s3.New(awsSession, awsConfig)
	// Route53 is weird about regions
	// https://github.com/openshift/cluster-ingress-operator/blob/5660b43d66bd63bbe2dcb45fb40df98d8d91347e/pkg/dns/aws/dns.go#L163-L169
	r53 := route53.New(awsSession, awsConfig.WithRegion("us-east-1"))

	// Create or get an existing stack
	infra, err := o.getOrCreateStack(ctx, cf, r53)
	if err != nil {
		return nil, err
	}

	// Generate PKI
	privKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}
	infra.ServiceAccountSigningKey = pem.EncodeToMemory(&pem.Block{
		Type:    "RSA PRIVATE KEY",
		Headers: nil,
		Bytes:   x509.MarshalPKCS1PrivateKey(privKey),
	})

	// Configure OIDC
	err = o.configureOIDC(s3client, privKey, infra.OIDCBucketName, infra.OIDCIssuerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to install OIDC discovery data: %w", err)
	}

	// Configure DNS for the subdomain
	err = o.configureSubdomain(ctx, cf, r53, infra.StackID)

	return infra, err
}

func (o *CreateInfraOptions) getOrCreateStack(ctx context.Context, cf *cloudformation.CloudFormation, r53 route53iface.Route53API) (*CreateInfraOutput, error) {
	log.Info("Creating infrastructure", "id", o.InfraID, "baseDomain", o.BaseDomain, "subdomain", o.Subdomain)

	publicZoneID, err := lookupZone(r53, o.BaseDomain, false)
	if err != nil {
		return nil, fmt.Errorf("couldn't find a public zone for base domain %s: %w", o.BaseDomain, err)
	}
	log.Info("Discovered base domain zone", "baseDomain", o.BaseDomain, "id", publicZoneID)

	stackName := o.InfraID

	var stack *cloudformation.Stack
	if existing, err := getStack(cf, stackName); err == nil {
		stack = existing
		log.Info("Found existing stack", "id", *stack.StackId)
	} else {
		newStack, err := o.createStack(ctx, cf, publicZoneID)
		if err != nil {
			return nil, fmt.Errorf("failed to create stack: %w", err)
		}
		stack = newStack
	}

	output := &CreateInfraOutput{
		InfraID:                  o.InfraID,
		StackID:                  *stack.StackId,
		Region:                   getStackOutput(stack, "Region"),
		Zone:                     getStackOutput(stack, "Zone"),
		ComputeCIDR:              getStackOutput(stack, "ComputeCIDR"),
		VPCID:                    getStackOutput(stack, "VPCId"),
		PrivateSubnetID:          getStackOutput(stack, "PrivateSubnetId"),
		PublicSubnetID:           getStackOutput(stack, "PublicSubnetId"),
		WorkerSecurityGroupID:    getStackOutput(stack, "WorkerSecurityGroupId"),
		WorkerInstanceProfileID:  getStackOutput(stack, "WorkerInstanceProfileId"),
		BaseDomainZoneID:         getStackOutput(stack, "BaseDomainHostedZoneId"),
		Subdomain:                getStackOutput(stack, "Subdomain"),
		SubdomainPrivateZoneID:   getStackOutput(stack, "SubdomainPrivateZoneId"),
		SubdomainPublicZoneID:    getStackOutput(stack, "SubdomainPublicZoneId"),
		OIDCIngressRoleArn:       getStackOutput(stack, "OIDCIngressRoleArn"),
		OIDCImageRegistryRoleArn: getStackOutput(stack, "OIDCImageRegistryRoleArn"),
		OIDCCSIDriverRoleArn:     getStackOutput(stack, "OIDCCSIDriverRoleArn"),
		OIDCIssuerURL:            getStackOutput(stack, "OIDCIssuerURL"),
		OIDCBucketName:           getStackOutput(stack, "OIDCBucketName"),
	}

	return output, nil
}

func (o *CreateInfraOptions) createStack(ctx context.Context, cf *cloudformation.CloudFormation, baseDomainZoneID string) (*cloudformation.Stack, error) {
	createStackInput := &cloudformation.CreateStackInput{
		Capabilities: []*string{aws.String(cloudformation.CapabilityCapabilityNamedIam)},
		TemplateBody: &hypercf.ClusterTemplate,
		StackName:    aws.String(o.InfraID),
		Tags: []*cloudformation.Tag{
			{
				Key:   aws.String("hypershift.openshift.io/infra"),
				Value: aws.String("owned"),
			},
		},
		Parameters: []*cloudformation.Parameter{
			{
				ParameterKey:   aws.String("InfrastructureName"),
				ParameterValue: aws.String(o.InfraID),
			},
			{
				ParameterKey:   aws.String("BaseDomainHostedZoneId"),
				ParameterValue: aws.String(baseDomainZoneID),
			},
			{
				ParameterKey:   aws.String("Subdomain"),
				ParameterValue: aws.String(o.Subdomain),
			},
		},
	}

	if !o.PreserveOnFailure {
		createStackInput.OnFailure = aws.String(cloudformation.OnFailureDelete)
	}

	createStackOutput, err := cf.CreateStack(createStackInput)
	if err != nil {
		return nil, err
	}

	log.Info("Waiting for infrastructure to be created", "id", o.InfraID, "stackID", *createStackOutput.StackId)
	var stack *cloudformation.Stack
	err = wait.PollUntil(5*time.Second, func() (bool, error) {
		latest, err := getStack(cf, *createStackOutput.StackId)
		if err != nil {
			log.Error(err, "failed to get stack", "id", *createStackOutput.StackId)
			return false, nil
		}
		stack = latest
		switch *stack.StackStatus {
		case cloudformation.StackStatusCreateComplete:
			return true, nil
		case cloudformation.StackStatusCreateInProgress:
			return false, nil
		case cloudformation.StackStatusCreateFailed:
			return false, fmt.Errorf("stack creation failed")
		case cloudformation.StackStatusRollbackInProgress,
			cloudformation.StackStatusRollbackComplete,
			cloudformation.StackStatusRollbackFailed:
			return false, fmt.Errorf("stack creation failed and was rolled back")
		default:
			return false, fmt.Errorf("unexpected stack creation status: %s", *stack.StackStatus)
		}
	}, ctx.Done())
	if err != nil {
		return nil, err
	}
	return stack, nil
}

func (o *CreateInfraOptions) configureOIDC(s3Client s3iface.S3API, privKey *rsa.PrivateKey, bucketName string, issuerURL string) error {
	pubKey := &privKey.PublicKey
	pubKeyDERBytes, err := x509.MarshalPKIXPublicKey(pubKey)
	if err != nil {
		return fmt.Errorf("failed to marshal public key: %w", err)
	}

	hasher := crypto.SHA256.New()
	hasher.Write(pubKeyDERBytes)
	pubKeyDERHash := hasher.Sum(nil)
	kid := base64.RawURLEncoding.EncodeToString(pubKeyDERHash)

	var keys []jose.JSONWebKey
	keys = append(keys, jose.JSONWebKey{
		Key:       pubKey,
		KeyID:     kid,
		Algorithm: string(jose.RS256),
		Use:       "sig",
	})

	const jwksKey = "openid/v1/jwks"
	type KeyResponse struct {
		Keys []jose.JSONWebKey `json:"keys"`
	}
	jwks, err := json.MarshalIndent(KeyResponse{Keys: keys}, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal KeyResponse: %w", err)
	}

	if _, err := s3Client.PutObject(&s3.PutObjectInput{
		ACL:    aws.String("public-read"),
		Body:   bytes.NewReader(jwks),
		Bucket: aws.String(bucketName),
		Key:    aws.String(jwksKey),
	}); err != nil {
		return fmt.Errorf("failed to put jwks in bucket: %w", err)
	}
	log.Info("JWKS document updated", "bucket", bucketName)

	discoveryTemplate := `{
	"issuer": "%s",
	"jwks_uri": "%s/%s",
	"response_types_supported": [
		"id_token"
	],
	"subject_types_supported": [
		"public"
	],
	"id_token_signing_alg_values_supported": [
		"RS256"
	],
	"claims_supported": [
		"aud",
		"exp",
		"sub",
		"iat",
		"iss",
		"sub"
	]
}`

	discoveryJSON := fmt.Sprintf(discoveryTemplate, issuerURL, issuerURL, jwksKey)
	if _, err := s3Client.PutObject(&s3.PutObjectInput{
		ACL:    aws.String("public-read"),
		Body:   aws.ReadSeekCloser(strings.NewReader(discoveryJSON)),
		Bucket: aws.String(bucketName),
		Key:    aws.String(".well-known/openid-configuration"),
	}); err != nil {
		return fmt.Errorf("failed to put discovery JSON in bucket: %w", err)
	}
	log.Info("OIDC discovery document updated", "bucket", bucketName)

	return nil
}

// TODO: Replace direct Route53 manipulation with CloudFormations
func (o *CreateInfraOptions) configureSubdomain(ctx context.Context, cf *cloudformation.CloudFormation, r53 route53iface.Route53API, stackID string) error {
	var nameservers, zoneID, subdomain string
	err := wait.PollUntil(5*time.Second, func() (bool, error) {
		stack, err := getStack(cf, stackID)
		if err != nil {
			log.Error(err, "failed to get stack", "id", stackID)
			return false, nil
		}
		nameservers = getStackOutput(stack, "SubdomainPublicZoneNameServers")
		subdomain = getStackOutput(stack, "Subdomain")
		zoneID = getStackOutput(stack, "BaseDomainHostedZoneId")
		if len(nameservers) > 0 {
			return true, nil
		}
		return false, nil
	}, ctx.Done())
	if err != nil {
		return fmt.Errorf("subdomain nameservers were not published: %w", err)
	}

	records := []*route53.ResourceRecord{}
	for _, ns := range strings.Split(nameservers, ",") {
		records = append(records, &route53.ResourceRecord{Value: aws.String(ns)})
	}

	params := &route53.ChangeResourceRecordSetsInput{
		ChangeBatch: &route53.ChangeBatch{
			Changes: []*route53.Change{
				{
					Action: aws.String("UPSERT"),
					ResourceRecordSet: &route53.ResourceRecordSet{
						Name:            aws.String(subdomain),
						Type:            aws.String("NS"),
						ResourceRecords: records,
						TTL:             aws.Int64(60),
					},
				},
			},
		},
		HostedZoneId: aws.String(zoneID),
	}

	_, err = r53.ChangeResourceRecordSets(params)
	if err != nil {
		return fmt.Errorf("failed to create NS record for subdomain: %w", err)
	}
	log.Info("Updated subdomain NS record in base zone", "zoneID", zoneID, "subdomain", subdomain, "nameservers", nameservers)

	return nil
}
