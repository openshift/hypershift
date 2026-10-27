package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Clients provides access to AWS service clients.
type Clients struct {
	Cfg       aws.Config
	AccountID string
}

// NewClients creates a new Clients from the default AWS config.
func NewClients(ctx context.Context, region string) (*Clients, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	stsClient := sts.NewFromConfig(cfg)
	identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("getting caller identity: %w", err)
	}

	return &Clients{
		Cfg:       cfg,
		AccountID: aws.ToString(identity.Account),
	}, nil
}

// EC2 returns an EC2 client for the given region.
func (c *Clients) EC2(region string) *ec2.Client {
	cfg := c.Cfg.Copy()
	cfg.Region = region
	return ec2.NewFromConfig(cfg)
}

// IAM returns an IAM client.
func (c *Clients) IAM() *iam.Client {
	return iam.NewFromConfig(c.Cfg)
}

// Route53 returns a Route53 client.
func (c *Clients) Route53() *route53.Client {
	return route53.NewFromConfig(c.Cfg)
}

// S3 returns an S3 client for the given region.
func (c *Clients) S3(region string) *s3.Client {
	cfg := c.Cfg.Copy()
	cfg.Region = region
	return s3.NewFromConfig(cfg)
}

// Tagging returns a Resource Groups Tagging API client for the given region.
func (c *Clients) Tagging(region string) *resourcegroupstaggingapi.Client {
	cfg := c.Cfg.Copy()
	cfg.Region = region
	return resourcegroupstaggingapi.NewFromConfig(cfg)
}

// ELB returns an ELB (Classic) client for the given region.
func (c *Clients) ELB(region string) *elasticloadbalancing.Client {
	cfg := c.Cfg.Copy()
	cfg.Region = region
	return elasticloadbalancing.NewFromConfig(cfg)
}

// ELBv2 returns an ELBv2 (ALB/NLB) client for the given region.
func (c *Clients) ELBv2(region string) *elasticloadbalancingv2.Client {
	cfg := c.Cfg.Copy()
	cfg.Region = region
	return elasticloadbalancingv2.NewFromConfig(cfg)
}

// STS returns an STS client.
func (c *Clients) STS() *sts.Client {
	return sts.NewFromConfig(c.Cfg)
}
