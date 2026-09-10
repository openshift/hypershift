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

package util

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// UpdateObject refreshes an object, applies mutate to a deep copy, and patches
// the result. Reads and resource-version conflicts are retried for up to one
// minute; other patch errors are returned immediately.
func UpdateObject[T crclient.Object](ctx context.Context, client crclient.Client, original T, mutate func(obj T)) error {
	key := crclient.ObjectKeyFromObject(original)
	var lastAttemptErr error
	var previousError string
	recordRetry := func(err error) {
		lastAttemptErr = err
		if err.Error() == previousError {
			return
		}
		previousError = err.Error()
		GinkgoWriter.Printf("UpdateObject %s/%s: %v; retrying\n", key.Namespace, key.Name, err)
	}

	err := wait.PollUntilContextTimeout(ctx, time.Second, time.Minute, true, func(ctx context.Context) (bool, error) {
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(original), original); err != nil {
			recordRetry(fmt.Errorf("failed to retrieve object: %w", err))
			return false, nil
		}

		obj := original.DeepCopyObject().(T)
		mutate(obj)

		if err := client.Patch(ctx, obj, crclient.MergeFrom(original)); err != nil {
			recordRetry(fmt.Errorf("failed to patch object: %w", err))
			if apierrors.IsConflict(err) {
				return false, nil
			}
			return false, err
		}

		return true, nil
	})
	if err != nil && lastAttemptErr != nil {
		return fmt.Errorf("failed to update object %s/%s: %w (polling error: %w)", key.Namespace, key.Name, lastAttemptErr, err)
	}
	return err
}
