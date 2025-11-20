/*
Copyright The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package awsclient

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"k8s.io/klog/v2"
)

const (
	// awsRegionsCacheExpirationDuration is the duration for which the AWS regions cache is valid
	awsRegionsCacheExpirationDuration = time.Minute * 30
)

// Client is a minimal AWS client interface for EC2 instance type queries
type Client interface {
	DescribeInstanceTypes(context.Context, *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error)
}

type awsClient struct {
	ec2Client *ec2.Client
}

func (c *awsClient) DescribeInstanceTypes(ctx context.Context, input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
	return c.ec2Client.DescribeInstanceTypes(ctx, input)
}

// DescribeRegionsData holds output of DescribeRegions API call and the time when it was last updated.
type DescribeRegionsData struct {
	err                   error
	describeRegionsOutput *ec2.DescribeRegionsOutput
	lastUpdated           time.Time
}

type regionCache struct {
	data  map[string]DescribeRegionsData
	mutex sync.RWMutex
}

// RegionCache caches successful DescribeRegions API calls.
type RegionCache interface {
	GetCachedDescribeRegions(ctx context.Context, cfg aws.Config) (*ec2.DescribeRegionsOutput, error)
}

// NewRegionCache creates a new empty DescribeRegionsData cache with lock.
func NewRegionCache() RegionCache {
	return &regionCache{
		data:  map[string]DescribeRegionsData{},
		mutex: sync.RWMutex{},
	}
}

// GetCachedDescribeRegions returns DescribeRegionsOutput from DescribeRegions AWS API call.
// It is cached to avoid AWS API calls on each reconcile loop.
func (c *regionCache) GetCachedDescribeRegions(ctx context.Context, cfg aws.Config) (*ec2.DescribeRegionsOutput, error) {
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return nil, err
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()
	regionData := c.data[creds.AccessKeyID]
	if regionData.describeRegionsOutput != nil && regionData.err == nil &&
		time.Since(regionData.lastUpdated) < awsRegionsCacheExpirationDuration {
		klog.V(3).Info("Using cached AWS region data")
		return regionData.describeRegionsOutput, nil
	}

	// Use a copy of the config to avoid mutating the original
	tempCfg := cfg.Copy()
	tempCfg.Region = "us-east-1"
	allRegions := true
	dryRun := false
	describeRegionsOutput, err := ec2.NewFromConfig(tempCfg).DescribeRegions(ctx, &ec2.DescribeRegionsInput{
		AllRegions: &allRegions,
		DryRun:     &dryRun,
	})
	if err != nil {
		regionData.err = err
		return nil, err
	}

	regionData.describeRegionsOutput = describeRegionsOutput
	regionData.lastUpdated = time.Now()
	c.data[creds.AccessKeyID] = regionData
	return describeRegionsOutput, nil
}

// validateRegion checks that region is in the DescribeRegions list and is opted in.
func validateRegion(describeRegionsOutput *ec2.DescribeRegionsOutput, region string) error {
	var regionData *types.Region
	for _, currentRegion := range describeRegionsOutput.Regions {
		if currentRegion.RegionName != nil && *currentRegion.RegionName == region {
			regionData = &currentRegion
			break
		}
	}

	if regionData == nil {
		return fmt.Errorf("region %s is not a valid region", region)
	}
	if regionData.OptInStatus != nil && *regionData.OptInStatus == "not-opted-in" {
		return fmt.Errorf("region %s is not opted in", region)
	}
	klog.V(3).Infof("AWS reports region %s is valid", region)
	return nil
}

// NewValidatedClient creates an AWS client with region validation.
// If credentialsFile is provided, it will be used for authentication.
// Otherwise, falls back to IRSA or default AWS credential chain.
func NewValidatedClient(region, credentialsFile string, regionCache RegionCache) (Client, error) {
	cfg, err := newAWSConfig(context.Background(), region, credentialsFile)
	if err != nil {
		return nil, err
	}

	// Validate region using AWS API
	klog.V(3).Infof("Validating region %s using AWS API", region)
	describeRegionsOutput, err := regionCache.GetCachedDescribeRegions(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("could not retrieve region data: %w", err)
	}

	err = validateRegion(describeRegionsOutput, region)
	if err != nil {
		return nil, err
	}

	return &awsClient{
		ec2Client: ec2.NewFromConfig(cfg),
	}, nil
}

func newAWSConfig(ctx context.Context, region, credentialsFile string) (aws.Config, error) {
	// Check for IRSA environment variables
	roleARN := os.Getenv("AWS_ROLE_ARN")
	tokenFile := os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE")

	// Build config options
	configOptions := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}

	// If credentials file is provided, use it
	if credentialsFile != "" {
		klog.V(3).Infof("Using AWS credentials from file: %s", credentialsFile)
		configOptions = append(configOptions, config.WithSharedConfigFiles([]string{credentialsFile}))
	} else if roleARN != "" && tokenFile != "" {
		klog.V(3).Infof("Using IRSA authentication with role: %s", roleARN)
		// AWS SDK v2 will automatically detect and use web identity credentials
		// from the environment variables - no explicit configuration needed
	} else {
		klog.V(3).Info("Using default AWS credential chain (environment variables, ~/.aws/credentials, EC2 metadata, etc.)")
	}

	// Create AWS config with the configured options
	cfg, err := config.LoadDefaultConfig(ctx, configOptions...)
	if err != nil {
		return aws.Config{}, err
	}

	return cfg, nil
}
