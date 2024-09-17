package upsert

import (
	"fmt"
	"sync"

	"go.uber.org/zap/zapcore"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func newUpdateLoopDetector() *updateLoopDetector {
	return &updateLoopDetector{
		hasNoOpUpdate:    sets.Set[string]{},
		updateEventCount: map[string]int{},
		log: zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
			o.EncodeTime = zapcore.RFC3339TimeEncoder
		})),
	}
}

// LoopDetectorWarningMessage is logged whenever we detect multiple updates of the same object
// without observering a no-op update.
const LoopDetectorWarningMessage = "WARNING: Object got updated more than one time without a no-op update, this indicates hypershift incorrectly reverting defaulted values"

// If an object got updated more than once a no-op update, we assume it is a bug in our
// code. This is a heuristic that currently happens to work out but might need adjustment
// in the future.
// Once we did a no-op update, we will ignore the object because we assume that if we have
// a bug in the defaulting, we will end up always updating.
func updateLoopThreshold(o runtime.Object) int {
	// Give some leeway, if we actually revert defaults we will do a lot more than this
	return 10
}

type updateLoopDetector struct {
	hasNoOpUpdate    sets.Set[string]
	lock             sync.RWMutex
	updateEventCount map[string]int
	log              logr.Logger
}

func (*updateLoopDetector) keyFor(obj runtime.Object, key crclient.ObjectKey) string {
	return fmt.Sprintf("%T %s", obj, key.String())
}

func (uld *updateLoopDetector) recordNoOpUpdate(obj crclient.Object, key crclient.ObjectKey) {
	uld.lock.Lock()
	defer uld.lock.Unlock()
	uld.hasNoOpUpdate.Insert(uld.keyFor(obj, key))
}

func (uld *updateLoopDetector) recordActualUpdate(original, modified runtime.Object, key crclient.ObjectKey) {
	// We have multiple controllers acting on these and they have no defaulting, which incorrectly triggers the
	// detector. Just skip them.
	if _, isAWSEndpointService := original.(*hyperv1.AWSEndpointService); isAWSEndpointService {
		return
	}
	cacheKey := uld.keyFor(original, key)
	uld.lock.RLock()
	hasNoOpUpdate := uld.hasNoOpUpdate.Has(cacheKey)
	uld.lock.RUnlock()

	if hasNoOpUpdate {
		return
	}

	uld.lock.Lock()
	uld.updateEventCount[cacheKey]++
	updateEventCount := uld.updateEventCount[cacheKey]
	uld.lock.Unlock()

	if updateEventCount < updateLoopThreshold(original) {
		return
	}

	diff := cmp.Diff(original, modified)
	semanticDeepEqual := equality.Semantic.DeepEqual(original, modified)
	uld.log.Info(LoopDetectorWarningMessage, "type", fmt.Sprintf("%T", modified), "name", key.String(), "diff", diff, "semanticDeepEqual", semanticDeepEqual, "updateCount", updateEventCount)
}
