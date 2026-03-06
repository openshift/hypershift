//go:build e2ev2

/*
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

package internal

import (
	"context"
	"sync"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestGetAWSRegion_WhenHostedClusterIsAWS_ItShouldReturnRegion(t *testing.T) {
	tc := &TestContext{
		Context: context.Background(),
		hostedCluster: &hyperv1.HostedCluster{
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{
					AWS: &hyperv1.AWSPlatformSpec{
						Region: "us-east-1",
					},
				},
			},
		},
	}

	region, err := tc.GetAWSRegion()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if region != "us-east-1" {
		t.Errorf("expected region %q, got %q", "us-east-1", region)
	}
}

func TestGetAWSRegion_WhenHostedClusterIsNotAWS_ItShouldReturnError(t *testing.T) {
	tc := &TestContext{
		Context: context.Background(),
		hostedCluster: &hyperv1.HostedCluster{
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{},
			},
		},
	}

	_, err := tc.GetAWSRegion()
	if err == nil {
		t.Error("expected error for non-AWS platform, but got nil")
	}
}

func TestGetAWSRegion_WhenRegionIsCached_ItShouldReturnCachedValue(t *testing.T) {
	tc := &TestContext{
		Context:   context.Background(),
		awsRegion: "eu-west-1",
	}

	region, err := tc.GetAWSRegion()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if region != "eu-west-1" {
		t.Errorf("expected cached region %q, got %q", "eu-west-1", region)
	}
}

func TestGetAWSRegion_WhenHostedClusterIsNil_ItShouldReturnError(t *testing.T) {
	tc := &TestContext{
		Context: context.Background(),
	}

	_, err := tc.GetAWSRegion()
	if err == nil {
		t.Error("expected error when HostedCluster is nil, but got nil")
	}
}

func TestGetAWSRegion_WhenCalledConcurrently_ItShouldReturnConsistentRegion(t *testing.T) {
	tc := &TestContext{
		Context: context.Background(),
		hostedCluster: &hyperv1.HostedCluster{
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{
					AWS: &hyperv1.AWSPlatformSpec{
						Region: "us-west-2",
					},
				},
			},
		},
	}

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	regions := make([]string, goroutines)
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			regions[idx], errs[idx] = tc.GetAWSRegion()
		}(i)
	}
	wg.Wait()

	for i := 0; i < goroutines; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: unexpected error: %v", i, errs[i])
		}
		if regions[i] != "us-west-2" {
			t.Errorf("goroutine %d: expected region %q, got %q", i, "us-west-2", regions[i])
		}
	}
}

func TestRequireAWSCredentials_WhenNotSet_ItShouldReturnError(t *testing.T) {
	tc := &TestContext{
		Context: context.Background(),
	}

	err := tc.requireAWSCredentials()
	if err == nil {
		t.Error("expected error when AWS credentials file is not set, but got nil")
	}
}

func TestRequireAWSCredentials_WhenSet_ItShouldNotReturnError(t *testing.T) {
	tc := &TestContext{
		Context:            context.Background(),
		awsCredentialsFile: "/tmp/fake-creds",
	}

	err := tc.requireAWSCredentials()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
