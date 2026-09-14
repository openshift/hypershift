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

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// NodePoolPollOptions configures a v2 node-readiness wait.
type NodePoolPollOptions struct {
	collectionPredicates []e2eutil.Predicate[[]*corev1.Node]
	predicates           []e2eutil.Predicate[*corev1.Node]
	clientOpts           []crclient.ListOption
	suffix               string
}

// NodePoolPollOption configures a v2 node-readiness wait.
type NodePoolPollOption func(*NodePoolPollOptions)

// WithCollectionPredicates adds predicates evaluated against the complete node collection.
func WithCollectionPredicates(predicates ...e2eutil.Predicate[[]*corev1.Node]) NodePoolPollOption {
	return func(options *NodePoolPollOptions) {
		options.collectionPredicates = predicates
	}
}

// WithPredicates adds predicates evaluated against each node.
func WithPredicates(predicates ...e2eutil.Predicate[*corev1.Node]) NodePoolPollOption {
	return func(options *NodePoolPollOptions) {
		options.predicates = predicates
	}
}

// WithClientOptions adds controller-runtime list options to the node getter.
func WithClientOptions(clientOpts ...crclient.ListOption) NodePoolPollOption {
	return func(options *NodePoolPollOptions) {
		options.clientOpts = clientOpts
	}
}

// WithSuffix adds text to the node-readiness objective.
func WithSuffix(suffix string) NodePoolPollOption {
	return func(options *NodePoolPollOptions) {
		options.suffix = suffix
	}
}

// WaitForNReadyNodes waits for n Ready nodes using the v2 polling path.
func WaitForNReadyNodes(ctx context.Context, client crclient.Client, n int32, platform hyperv1.PlatformType) ([]corev1.Node, error) {
	return WaitForNReadyNodesWithOptions(ctx, client, n, platform, "")
}

// WaitForReadyNodesByNodePool waits for the expected number of Ready nodes belonging to a NodePool.
func WaitForReadyNodesByNodePool(ctx context.Context, client crclient.Client, np *hyperv1.NodePool, platform hyperv1.PlatformType, opts ...NodePoolPollOption) ([]corev1.Node, error) {
	return WaitForNReadyNodesWithOptions(ctx, client, *np.Spec.Replicas, platform,
		fmt.Sprintf("for NodePool %s/%s", np.Namespace, np.Name),
		append(opts, WithClientOptions(crclient.MatchingLabelsSelector{Selector: labels.SelectorFromSet(labels.Set{hyperv1.NodePoolLabel: np.Name})}))...,
	)
}

// WaitForReadyNodesByLabels waits for the expected number of Ready nodes matching nodeLabels.
func WaitForReadyNodesByLabels(ctx context.Context, client crclient.Client, platform hyperv1.PlatformType, replicas int32, nodeLabels map[string]string) ([]corev1.Node, error) {
	return WaitForNReadyNodesWithOptions(ctx, client, replicas, platform, "",
		WithClientOptions(crclient.MatchingLabelsSelector{Selector: labels.SelectorFromSet(labels.Set(nodeLabels))}),
	)
}

// WaitForNodePoolConfigUpdateComplete waits for a NodePool config update to start and finish.
func WaitForNodePoolConfigUpdateComplete(ctx context.Context, client crclient.Client, np *hyperv1.NodePool) error {
	return WaitForNodePoolConfigUpdateCompleteWithPlatform(ctx, client, np, hyperv1.NonePlatform)
}

// WaitForNodePoolConfigUpdateCompleteWithPlatform waits for a NodePool config update to start and finish,
// preserving the platform-specific timeout used by the v1 helper.
func WaitForNodePoolConfigUpdateCompleteWithPlatform(ctx context.Context, client crclient.Client, np *hyperv1.NodePool, platform hyperv1.PlatformType) error {
	configUpdateTimeout := 25 * time.Minute
	switch platform {
	case hyperv1.AzurePlatform, hyperv1.KubevirtPlatform:
		configUpdateTimeout = 45 * time.Minute
	}

	if err := EventuallyObject(ctx, fmt.Sprintf("NodePool %s/%s to start config update", np.Namespace, np.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			nodePool := &hyperv1.NodePool{}
			err := client.Get(ctx, crclient.ObjectKeyFromObject(np), nodePool)
			return nodePool, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
				Type:   hyperv1.NodePoolUpdatingConfigConditionType,
				Status: metav1.ConditionTrue,
			}),
		},
		WithTimeout(5*time.Minute),
		WithInterval(15*time.Second),
	); err != nil {
		return err
	}

	return EventuallyObject(ctx, fmt.Sprintf("NodePool %s/%s to finish config update", np.Namespace, np.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			nodePool := &hyperv1.NodePool{}
			err := client.Get(ctx, crclient.ObjectKeyFromObject(np), nodePool)
			return nodePool, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
				Type:   hyperv1.NodePoolUpdatingConfigConditionType,
				Status: metav1.ConditionFalse,
			}),
		},
		WithTimeout(configUpdateTimeout),
		WithInterval(20*time.Second),
	)
}

// WaitForNReadyNodesWithOptions waits for n Ready nodes using the supplied list and predicate options.
func WaitForNReadyNodesWithOptions(ctx context.Context, client crclient.Client, n int32, platform hyperv1.PlatformType, suffix string, opts ...NodePoolPollOption) ([]corev1.Node, error) {
	options := &NodePoolPollOptions{}
	for _, opt := range opts {
		opt(options)
	}

	waitTimeout := 45 * time.Minute
	if platform == hyperv1.PowerVSPlatform {
		waitTimeout = 60 * time.Minute
	}

	nodes := &corev1.NodeList{}
	if suffix != "" {
		suffix = " " + suffix
	}
	if options.suffix != "" {
		suffix += " " + options.suffix
	}

	err := EventuallyObjects(ctx, fmt.Sprintf("%d nodes to become ready%s", n, suffix),
		func(ctx context.Context) ([]*corev1.Node, error) {
			err := client.List(ctx, nodes, options.clientOpts...)
			items := make([]*corev1.Node, len(nodes.Items))
			for i := range nodes.Items {
				items[i] = &nodes.Items[i]
			}
			return items, err
		},
		append([]e2eutil.Predicate[[]*corev1.Node]{
			func(nodes []*corev1.Node) (bool, string, error) {
				want, got := int(n), len(nodes)
				return want == got, fmt.Sprintf("expected %d nodes, got %d", want, got), nil
			},
		}, options.collectionPredicates...),
		append([]e2eutil.Predicate[*corev1.Node]{
			e2eutil.ConditionPredicate[*corev1.Node](e2eutil.Condition{
				Type:   string(corev1.NodeReady),
				Status: metav1.ConditionTrue,
			}),
		}, options.predicates...),
		WithTimeout(waitTimeout),
		WithInterval(3*time.Second),
	)
	return nodes.Items, err
}
