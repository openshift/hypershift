/*
Copyright 2025 The Kubernetes Authors.
Adapted for HyperShift — original source:
https://github.com/kubernetes-sigs/cluster-api/blob/v1.12.7/controllers/crdmigrator/crd_migrator.go

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

package capicrdmigrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/pkg/errors"
)

const (
	// CRDMigrationObservedGenerationAnnotation indicates on a CRD for which generation CRD migration is completed.
	CRDMigrationObservedGenerationAnnotation = "crd-migration.cluster.x-k8s.io/observed-generation"
)

// Phase is a phase of the CRD migration.
type Phase string

var (
	// StorageVersionMigrationPhase re-writes all CRs so they are stored in the current storageVersion in etcd.
	StorageVersionMigrationPhase Phase = "StorageVersionMigration"

	// CleanupManagedFieldsPhase removes managedFields referencing API versions that are no longer served.
	CleanupManagedFieldsPhase Phase = "CleanupManagedFields"
)

// CRDMigrator migrates CRDs.
type CRDMigrator struct {
	Client    client.Client
	APIReader client.Reader
	Namespace string

	SkipCRDMigrationPhases  []Phase
	crdMigrationPhasesToRun sets.Set[Phase]

	Config          map[client.Object]ByObjectConfig
	configByCRDName map[string]ByObjectConfig

	migrationCache  *ttlCache
	migrationStatus *StatusReporter
}

// ByObjectConfig contains object-specific config for the CRD migration.
type ByObjectConfig struct {
	UseCache                            bool
	UseStatusForStorageVersionMigration bool
}

func (r *CRDMigrator) SetupWithManager(ctx context.Context, mgr ctrl.Manager, controllerOptions controller.Options) error {
	if err := r.setup(mgr.GetScheme()); err != nil {
		return err
	}

	if len(r.crdMigrationPhasesToRun) == 0 {
		return nil
	}

	err := ctrl.NewControllerManagedBy(mgr).
		For(&apiextensionsv1.CustomResourceDefinition{},
			builder.OnlyMetadata,
			builder.WithPredicates(
				predicate.GenerationChangedPredicate{},
			),
		).
		Named("crdmigrator").
		WithOptions(controllerOptions).
		Complete(r)
	if err != nil {
		return errors.Wrap(err, "failed setting up with a controller manager")
	}

	return nil
}

func (r *CRDMigrator) setup(scheme *runtime.Scheme) error {
	if r.Client == nil || r.APIReader == nil || len(r.Config) == 0 {
		return errors.New("Client and APIReader must not be nil and Config must not be empty")
	}

	r.crdMigrationPhasesToRun = sets.Set[Phase]{}.Insert(StorageVersionMigrationPhase, CleanupManagedFieldsPhase)
	for _, skipPhase := range r.SkipCRDMigrationPhases {
		switch skipPhase {
		case StorageVersionMigrationPhase:
			r.crdMigrationPhasesToRun.Delete(StorageVersionMigrationPhase)
		case CleanupManagedFieldsPhase:
			r.crdMigrationPhasesToRun.Delete(CleanupManagedFieldsPhase)
		default:
			return errors.Errorf("invalid phase %s specified in SkipCRDMigrationPhases", skipPhase)
		}
	}

	r.configByCRDName = map[string]ByObjectConfig{}
	for obj, cfg := range r.Config {
		gvk, err := apiutil.GVKForObject(obj, scheme)
		if err != nil {
			return errors.Wrap(err, "failed to get GVK for object")
		}

		r.configByCRDName[calculateCRDName(gvk.Group, gvk.Kind)] = cfg
	}

	r.migrationCache = newTTLCache(1 * time.Hour)
	r.migrationStatus = NewStatusReporter(r.Client, r.Namespace, r.configByCRDName)
	return nil
}

// calculateCRDName returns the CRD name for a given group and kind.
func calculateCRDName(group, kind string) string {
	return strings.ToLower(flect(kind)) + "." + group
}

// flect is a minimal pluralizer for CAPI kind names.
func flect(kind string) string {
	if strings.HasSuffix(strings.ToLower(kind), "s") {
		return kind + "es"
	}
	return kind + "s"
}

func (r *CRDMigrator) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	migrationConfig, ok := r.configByCRDName[req.Name]
	if !ok {
		return ctrl.Result{}, nil
	}

	crdPartial := &metav1.PartialObjectMetadata{}
	crdPartial.SetGroupVersionKind(apiextensionsv1.SchemeGroupVersion.WithKind("CustomResourceDefinition"))
	if err := r.Client.Get(ctx, req.NamespacedName, crdPartial); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	currentGeneration := strconv.FormatInt(crdPartial.GetGeneration(), 10)
	if observedGeneration, ok := crdPartial.Annotations[CRDMigrationObservedGenerationAnnotation]; ok &&
		currentGeneration == observedGeneration {
		r.migrationStatus.SetCRDMigrated(req.Name)
		if err := r.migrationStatus.Reconcile(ctx, nil); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "Failed to report migration status")
		}
		return ctrl.Result{}, nil
	}

	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, crd); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	currentGeneration = strconv.FormatInt(crd.GetGeneration(), 10)

	storageVersion, err := storageVersionForCRD(crd)
	if err != nil {
		return ctrl.Result{}, err
	}

	defer func() {
		if reterr == nil {
			originalCRD := crd.DeepCopy()
			if crd.Annotations == nil {
				crd.Annotations = map[string]string{}
			}
			crd.Annotations[CRDMigrationObservedGenerationAnnotation] = currentGeneration
			if err := r.Client.Patch(ctx, crd, client.MergeFrom(originalCRD)); err != nil {
				reterr = kerrors.NewAggregate([]error{reterr, errors.Wrapf(err, "failed to patch CustomResourceDefinition %s", crd.Name)})
			} else {
				r.migrationStatus.SetCRDMigrated(req.Name)
			}
		}

		if err := r.migrationStatus.Reconcile(ctx, reterr); err != nil {
			log := ctrl.LoggerFrom(ctx)
			log.Error(err, "Failed to report migration status")
		}
	}()

	var customResourceObjects []client.Object
	if r.crdMigrationPhasesToRun.Has(StorageVersionMigrationPhase) && storageVersionMigrationRequired(crd, storageVersion) ||
		r.crdMigrationPhasesToRun.Has(CleanupManagedFieldsPhase) {
		var err error
		customResourceObjects, err = r.listCustomResources(ctx, crd, migrationConfig, storageVersion)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	if r.crdMigrationPhasesToRun.Has(StorageVersionMigrationPhase) && storageVersionMigrationRequired(crd, storageVersion) {
		if err := r.reconcileStorageVersionMigration(ctx, crd, migrationConfig, customResourceObjects, storageVersion); err != nil {
			return ctrl.Result{}, err
		}

		originalCRD := crd.DeepCopy()
		crd.Status.StoredVersions = []string{storageVersion}
		if err := r.Client.Status().Patch(ctx, crd, client.MergeFromWithOptions(originalCRD, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, errors.Wrapf(err, "failed to patch CustomResourceDefinition %s", crd.Name)
		}
	}

	if r.crdMigrationPhasesToRun.Has(CleanupManagedFieldsPhase) {
		if err := r.reconcileCleanupManagedFields(ctx, crd, customResourceObjects, migrationConfig, storageVersion); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func storageVersionForCRD(crd *apiextensionsv1.CustomResourceDefinition) (string, error) {
	for _, v := range crd.Spec.Versions {
		if v.Storage {
			return v.Name, nil
		}
	}
	return "", errors.Errorf("could not find storage version for CustomResourceDefinition %s", crd.Name)
}

func storageVersionMigrationRequired(crd *apiextensionsv1.CustomResourceDefinition, storageVersion string) bool {
	if len(crd.Status.StoredVersions) == 1 && crd.Status.StoredVersions[0] == storageVersion {
		return false
	}
	return true
}

func (r *CRDMigrator) listCustomResources(ctx context.Context, crd *apiextensionsv1.CustomResourceDefinition, migrationConfig ByObjectConfig, storageVersion string) ([]client.Object, error) {
	var objs []client.Object

	listGVK := schema.GroupVersionKind{
		Group:   crd.Spec.Group,
		Version: storageVersion,
		Kind:    crd.Spec.Names.ListKind,
	}

	if migrationConfig.UseCache {
		object, err := r.Client.Scheme().New(listGVK)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to list %s: failed to create %s object", crd.Spec.Names.Kind, crd.Spec.Names.ListKind)
		}
		objectList, ok := object.(client.ObjectList)
		if !ok {
			return nil, errors.Errorf("failed to list %s: %s object is not an ObjectList", crd.Spec.Names.Kind, crd.Spec.Names.ListKind)
		}
		objects, err := listObjectsFromCachedClient(ctx, r.Client, objectList)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to list %s via cached client", crd.Spec.Names.Kind)
		}
		objs = append(objs, objects...)
	} else {
		objectList := &metav1.PartialObjectMetadataList{}
		objectList.SetGroupVersionKind(listGVK)
		objects, err := listObjectsFromAPIReader(ctx, r.APIReader, objectList)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to list %s via live client", crd.Spec.Names.Kind)
		}
		objs = append(objs, objects...)
	}

	return objs, nil
}

func listObjectsFromCachedClient(ctx context.Context, c client.Client, objectList client.ObjectList) ([]client.Object, error) {
	objs := []client.Object{}

	if err := c.List(ctx, objectList); err != nil {
		return nil, err
	}

	objectListItems, err := meta.ExtractList(objectList)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to extract list items")
	}
	for _, obj := range objectListItems {
		objs = append(objs, obj.(client.Object))
	}

	return objs, nil
}

func listObjectsFromAPIReader(ctx context.Context, c client.Reader, objectList client.ObjectList) ([]client.Object, error) {
	objs := []client.Object{}

	for {
		listOpts := []client.ListOption{
			client.Continue(objectList.GetContinue()),
			client.Limit(500),
		}
		if err := c.List(ctx, objectList, listOpts...); err != nil {
			return nil, err
		}

		objectListItems, err := meta.ExtractList(objectList)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to extract list items")
		}
		for _, obj := range objectListItems {
			objs = append(objs, obj.(client.Object))
		}

		if objectList.GetContinue() == "" {
			break
		}
	}

	return objs, nil
}

func (r *CRDMigrator) reconcileStorageVersionMigration(ctx context.Context, crd *apiextensionsv1.CustomResourceDefinition, migrationConfig ByObjectConfig, customResourceObjects []client.Object, storageVersion string) error {
	if len(customResourceObjects) == 0 {
		return nil
	}

	log := ctrl.LoggerFrom(ctx)
	log.Info(fmt.Sprintf("Running storage version migration to apiVersion %s (for %d objects)", storageVersion, len(customResourceObjects)))

	gvk := schema.GroupVersionKind{
		Group:   crd.Spec.Group,
		Version: storageVersion,
		Kind:    crd.Spec.Names.Kind,
	}

	errs := []error{}
	for _, obj := range customResourceObjects {
		cacheKey := fmt.Sprintf("%s %s/%s %d", gvk.Kind, obj.GetNamespace(), obj.GetName(), crd.Generation)

		if r.migrationCache.has(cacheKey) {
			r.migrationCache.add(cacheKey)
			continue
		}

		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(gvk)
		u.SetNamespace(obj.GetNamespace())
		u.SetName(obj.GetName())
		u.SetUID(obj.GetUID())
		u.SetResourceVersion(obj.GetResourceVersion())

		log.V(4).Info("Migrating to new storage version", gvk.Kind, klog.KObj(u))
		var err error
		if migrationConfig.UseStatusForStorageVersionMigration {
			err = r.Client.Status().Patch(ctx, u, client.Apply, client.FieldOwner("crdmigrator"))
		} else {
			err = r.Client.Apply(ctx, client.ApplyConfigurationFromUnstructured(u), client.FieldOwner("crdmigrator"))
		}
		if err != nil && !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
			errs = append(errs, errors.Wrap(err, klog.KObj(u).String()))
			continue
		}

		r.migrationCache.add(cacheKey)
	}

	if len(errs) > 0 {
		return errors.Wrapf(kerrors.NewAggregate(errs), "failed to migrate storage version of %s objects", gvk.Kind)
	}

	return nil
}

func (r *CRDMigrator) reconcileCleanupManagedFields(ctx context.Context, crd *apiextensionsv1.CustomResourceDefinition, customResourceObjects []client.Object, migrationConfig ByObjectConfig, storageVersion string) error {
	if len(customResourceObjects) == 0 {
		return nil
	}

	log := ctrl.LoggerFrom(ctx)
	log.Info(fmt.Sprintf("Running managedField cleanup (for %d objects)", len(customResourceObjects)))

	servedGroupVersions := sets.Set[string]{}
	for _, v := range crd.Spec.Versions {
		if v.Served {
			servedGroupVersions.Insert(fmt.Sprintf("%s/%s", crd.Spec.Group, v.Name))
		}
	}

	errs := []error{}
	for _, obj := range customResourceObjects {
		if len(obj.GetManagedFields()) == 0 {
			continue
		}

		var getErr error
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			getErr = nil

			managedFields, removed := removeManagedFieldsWithNotServedGroupVersion(obj, servedGroupVersions)
			if !removed {
				return nil
			}

			if len(managedFields) == 0 {
				fieldV1Map := map[string]interface{}{
					"f:metadata": map[string]interface{}{
						"f:name": map[string]interface{}{},
					},
				}
				fieldV1, err := json.Marshal(fieldV1Map)
				if err != nil {
					return errors.Wrap(err, "failed to create seeding managedField entry")
				}
				managedFields = append(managedFields, metav1.ManagedFieldsEntry{
					Manager:    obj.GetManagedFields()[0].Manager,
					Operation:  obj.GetManagedFields()[0].Operation,
					APIVersion: schema.GroupVersion{Group: crd.Spec.Group, Version: storageVersion}.String(),
					Time:       ptr.To(metav1.Now()),
					FieldsType: "FieldsV1",
					FieldsV1:   &metav1.FieldsV1{Raw: fieldV1},
				})
			}

			jsonPatch := []map[string]interface{}{
				{
					"op":    "replace",
					"path":  "/metadata/managedFields",
					"value": managedFields,
				},
				{
					"op":    "replace",
					"path":  "/metadata/resourceVersion",
					"value": obj.GetResourceVersion(),
				},
			}
			patch, err := json.Marshal(jsonPatch)
			if err != nil {
				return errors.Wrap(err, "failed to marshal patch")
			}

			log.V(4).Info("Cleaning up managedFields", crd.Spec.Names.Kind, klog.KObj(obj))
			err = r.Client.Patch(ctx, obj, client.RawPatch(types.JSONPatchType, patch))
			if err == nil || apierrors.IsNotFound(err) {
				return nil
			}

			if apierrors.IsConflict(err) {
				if migrationConfig.UseCache {
					getErr = r.Client.Get(ctx, client.ObjectKeyFromObject(obj), obj)
				} else {
					getErr = r.APIReader.Get(ctx, client.ObjectKeyFromObject(obj), obj)
				}
			}
			return err
		}); err != nil {
			errs = append(errs, errors.Wrap(kerrors.NewAggregate([]error{err, getErr}), klog.KObj(obj).String()))
			continue
		}
	}

	if len(errs) > 0 {
		return errors.Wrapf(kerrors.NewAggregate(errs), "failed to cleanup managedFields of %s objects", crd.Spec.Names.Kind)
	}

	return nil
}

func removeManagedFieldsWithNotServedGroupVersion(obj client.Object, servedGroupVersions sets.Set[string]) ([]metav1.ManagedFieldsEntry, bool) {
	removedManagedFields := false
	managedFields := []metav1.ManagedFieldsEntry{}
	for _, managedField := range obj.GetManagedFields() {
		if servedGroupVersions.Has(managedField.APIVersion) {
			managedFields = append(managedFields, managedField)
			continue
		}
		removedManagedFields = true
	}
	return managedFields, removedManagedFields
}

// ttlCache is a simple TTL-based cache replacing CAPI's util/cache.Cache.
type ttlCache struct {
	mu      sync.Mutex
	entries map[string]time.Time
	ttl     time.Duration
}

func newTTLCache(ttl time.Duration) *ttlCache {
	return &ttlCache{
		entries: make(map[string]time.Time),
		ttl:     ttl,
	}
}

func (c *ttlCache) add(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = time.Now()
}

func (c *ttlCache) has(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.entries[key]
	if !ok {
		return false
	}
	if time.Since(t) > c.ttl {
		delete(c.entries, key)
		return false
	}
	return true
}
