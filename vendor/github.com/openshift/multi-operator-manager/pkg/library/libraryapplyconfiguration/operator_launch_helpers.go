package libraryapplyconfiguration

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"runtime/debug"
	"strings"
	"time"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/manifestclient"
	"github.com/openshift/library-go/pkg/operator/events"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic/dynamicinformer"
)

type OperatorStarter interface {
	RunOnce(ctx context.Context, input ApplyConfigurationInput) (*ApplyConfigurationRunResult, AllDesiredMutationsGetter, error)
	Start(ctx context.Context) error
}

type SimpleOperatorStarter struct {
	Informers                 []SimplifiedInformerFactory
	ControllerNamedRunOnceFns []NamedRunOnce
	// ControllerRunFns is useful during a transition to coalesce the operator launching flow.
	ControllerRunFns []RunFunc
}

var (
	_ OperatorStarter           = &SimpleOperatorStarter{}
	_ SimplifiedInformerFactory = generatedInformerFactory{}
	_ SimplifiedInformerFactory = dynamicInformerFactory{}
	_ SimplifiedInformerFactory = generatedNamespacedInformerFactory{}
)

func (a SimpleOperatorStarter) RunOnce(ctx context.Context, input ApplyConfigurationInput) (*ApplyConfigurationRunResult, AllDesiredMutationsGetter, error) {
	for _, informer := range a.Informers {
		informer.Start(ctx)
	}
	// wait for sync so that when NamedRunOnce is called the listers will be ready.
	// TODO add timeout
	for _, informer := range a.Informers {
		informer.WaitForCacheSync(ctx)
	}

	knownControllersSet := sets.NewString()
	duplicateControllerNames := []string{}
	for _, controllerRunner := range a.ControllerNamedRunOnceFns {
		if knownControllersSet.Has(controllerRunner.ControllerInstanceName()) {
			duplicateControllerNames = append(duplicateControllerNames, controllerRunner.ControllerInstanceName())
			continue
		}
		knownControllersSet.Insert(controllerRunner.ControllerInstanceName())
	}
	if len(duplicateControllerNames) > 0 {
		return nil, nil, fmt.Errorf("the following controllers were requested to run multiple times: %v", duplicateControllerNames)
	}

	if errs := validateControllersFromFlags(knownControllersSet, input.Controllers); len(errs) > 0 {
		return nil, nil, errors.Join(errs...)
	}

	allControllersRunResult := &ApplyConfigurationRunResult{}

	shuffleNamedRunOnce(a.ControllerNamedRunOnceFns)
	errs := []error{}
	for _, controllerRunner := range a.ControllerNamedRunOnceFns {
		func() {
			currControllerResult := ControllerRunResult{
				ControllerName: controllerRunner.ControllerInstanceName(),
				Status:         ControllerRunStatusUnknown,
			}
			defer func() {
				if r := recover(); r != nil {
					currControllerResult.Status = ControllerRunStatusPanicked
					currControllerResult.PanicStack = fmt.Sprintf("%s\n%s", r, string(debug.Stack()))
				}
				allControllersRunResult.ControllerResults = append(allControllersRunResult.ControllerResults, currControllerResult)
			}()

			if !isControllerEnabled(controllerRunner.ControllerInstanceName(), input.Controllers) {
				currControllerResult.Status = ControllerRunStatusSkipped
				return
			}
			localCtx, localCancel := context.WithTimeout(ctx, 1*time.Second)
			defer localCancel()

			localCtx = manifestclient.WithControllerInstanceNameFromContext(localCtx, controllerRunner.ControllerInstanceName())
			if err := controllerRunner.RunOnce(localCtx); err != nil {
				currControllerResult.Status = ControllerRunStatusFailed
				currControllerResult.Errors = append(currControllerResult.Errors, ErrorDetails{Message: err.Error()})
				errs = append(errs, fmt.Errorf("controller %q failed: %w", controllerRunner.ControllerInstanceName(), err))
			} else {
				currControllerResult.Status = ControllerRunStatusSucceeded
			}
		}()
	}

	// canonicalize
	CanonicalizeApplyConfigurationRunResult(allControllersRunResult)

	return allControllersRunResult, NewApplyConfigurationFromClient(input.MutationTrackingClient.GetMutations()), errors.Join(errs...)
}

func (a SimpleOperatorStarter) Start(ctx context.Context) error {
	for _, informer := range a.Informers {
		informer.Start(ctx)
	}

	for _, controllerRunFn := range a.ControllerRunFns {
		go controllerRunFn(ctx)
	}
	return nil
}

type SimplifiedInformerFactory interface {
	Start(ctx context.Context)
	WaitForCacheSync(ctx context.Context)
}

type NamedRunOnce interface {
	ControllerInstanceName() string
	RunOnce(context.Context) error
}

type namedRunOnce struct {
	controllerInstanceName string
	runOnce                RunOnceFunc
}

func NewNamedRunOnce(controllerInstanceName string, runOnce RunOnceFunc) *namedRunOnce {
	return &namedRunOnce{
		controllerInstanceName: controllerInstanceName,
		runOnce:                runOnce,
	}
}

func (r *namedRunOnce) RunOnce(ctx context.Context) error {
	return r.runOnce(ctx)
}

func (r *namedRunOnce) ControllerInstanceName() string {
	return r.controllerInstanceName
}

type RunOnceFunc func(ctx context.Context) error

type RunFunc func(ctx context.Context)

type GeneratedInformerFactory interface {
	Start(stopCh <-chan struct{})
	WaitForCacheSync(stopCh <-chan struct{}) map[reflect.Type]bool
}

func GeneratedInformerFactoryAdapter(in GeneratedInformerFactory) SimplifiedInformerFactory {
	return generatedInformerFactory{delegate: in}
}

func DynamicInformerFactoryAdapter(in dynamicinformer.DynamicSharedInformerFactory) SimplifiedInformerFactory {
	return dynamicInformerFactory{delegate: in}
}

func GeneratedNamespacedInformerFactoryAdapter(in GeneratedNamespacedInformerFactory) SimplifiedInformerFactory {
	return generatedNamespacedInformerFactory{delegate: in}
}

func AdaptRunFn(fn func(ctx context.Context, workers int)) RunFunc {
	return func(ctx context.Context) {
		fn(ctx, 1)
	}
}

func AdaptSyncFn(eventRecorder events.Recorder, controllerName string, originalRunOnce func(ctx context.Context, syncCtx factory.SyncContext) error) NamedRunOnce {
	return NewNamedRunOnce(controllerName, func(ctx context.Context) error {
		syncCtx := factory.NewSyncContext("run-once-sync-context", eventRecorder)
		return originalRunOnce(ctx, syncCtx)
	})
}

type Syncer interface {
	Sync(ctx context.Context, controllerContext factory.SyncContext) error
}

type ControllerWithInstanceName interface {
	ControllerInstanceName() string
}

func AdaptNamedController(eventRecorder events.Recorder, controller Syncer) NamedRunOnce {
	controllerWithInstanceName, ok := controller.(ControllerWithInstanceName)
	if !ok {
		panic(fmt.Sprintf("%T doesn't expose ControllerInstanceName() method which is required", controller))
	}
	controllerInstanceName := controllerWithInstanceName.ControllerInstanceName()
	if len(controllerInstanceName) == 0 {
		panic(fmt.Sprintf("%T cannot return an empty ControllerInstanceName", controller))
	}

	return NewNamedRunOnce(controllerInstanceName, func(ctx context.Context) error {
		syncCtx := factory.NewSyncContext("run-named-once-sync-context", eventRecorder)
		return controller.Sync(ctx, syncCtx)
	})
}

type generatedInformerFactory struct {
	delegate GeneratedInformerFactory
}

func (g generatedInformerFactory) Start(ctx context.Context) {
	g.delegate.Start(ctx.Done())
}

func (g generatedInformerFactory) WaitForCacheSync(ctx context.Context) {
	g.delegate.WaitForCacheSync(ctx.Done())
}

type dynamicInformerFactory struct {
	delegate dynamicinformer.DynamicSharedInformerFactory
}

func (g dynamicInformerFactory) Start(ctx context.Context) {
	g.delegate.Start(ctx.Done())
}

func (g dynamicInformerFactory) WaitForCacheSync(ctx context.Context) {
	g.delegate.WaitForCacheSync(ctx.Done())
}

type GeneratedNamespacedInformerFactory interface {
	Start(stopCh <-chan struct{})
	WaitForCacheSync(stopCh <-chan struct{}) map[string]map[reflect.Type]bool
}

type generatedNamespacedInformerFactory struct {
	delegate GeneratedNamespacedInformerFactory
}

func (g generatedNamespacedInformerFactory) Start(ctx context.Context) {
	g.delegate.Start(ctx.Done())
}

func (g generatedNamespacedInformerFactory) WaitForCacheSync(ctx context.Context) {
	g.delegate.WaitForCacheSync(ctx.Done())
}

func shuffleNamedRunOnce(controllers []NamedRunOnce) {
	rand.Shuffle(len(controllers), func(i, j int) {
		controllers[i], controllers[j] = controllers[j], controllers[i]
	})
}

func isControllerEnabled(name string, controllers []string) bool {
	hasStar := false
	for _, ctrl := range controllers {
		if ctrl == name {
			return true
		}
		if ctrl == "-"+name {
			return false
		}
		if ctrl == "*" {
			hasStar = true
		}
	}

	return hasStar
}

func validateControllersFromFlags(allKnownControllersSet sets.String, controllersToRunFromFlags []string) []error {
	var errs []error
	for _, initialName := range controllersToRunFromFlags {
		if initialName == "*" {
			continue
		}
		initialNameWithoutPrefix := strings.TrimPrefix(initialName, "-")
		controllerName := initialNameWithoutPrefix
		if !allKnownControllersSet.Has(controllerName) {
			errs = append(errs, fmt.Errorf("%q is not in the list of known controllers", initialNameWithoutPrefix))
		}
	}

	return errs
}
