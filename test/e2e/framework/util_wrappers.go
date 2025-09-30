//go:build e2e
// +build e2e

package framework

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"

	"github.com/blang/semver"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	e2eutil "github.com/openshift/hypershift/test/e2e/util"
)

// Ginkgo-compatible helper functions
// These are duplicates of e2eutil functions but using GinkgoT() instead of *testing.T
// This avoids the type incompatibility between ginkgo.FullGinkgoTInterface and *testing.T

// updateObject is a Ginkgo-compatible version of e2eutil.UpdateObject
func updateObject[T crclient.Object](ctx context.Context, client crclient.Client, original T, mutate func(obj T)) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, time.Minute*1, true, func(ctx context.Context) (done bool, err error) {
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(original), original); err != nil {
			GinkgoT().Logf("failed to retrieve object %s, will retry: %v", original.GetName(), err)
			return false, nil
		}

		obj := original.DeepCopyObject().(T)
		mutate(obj)

		if err := client.Patch(ctx, obj, crclient.MergeFrom(original)); err != nil {
			GinkgoT().Logf("failed to patch object %s, will retry: %v", original.GetName(), err)
			if errors.IsConflict(err) {
				return false, nil
			}
			return false, err
		}

		return true, nil
	})
}

// atLeast is a Ginkgo-compatible version of e2eutil.AtLeast
func atLeast(version semver.Version) {
	if e2eutil.IsLessThan(version) {
		Skip("Only tested in " + version.String() + " and later")
	}
}
