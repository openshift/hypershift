package awsprivatelink

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/upsert"
)

const (
	defaultResync = 10 * time.Hour
)

type PrivateServiceObserver struct {
	client client.Client
	log    logr.Logger

	ControllerName   string
	ServiceNamespace string
	ServiceName      string
	HCPNamespace     string
	upsert.CreateOrUpdateProvider
}

func nameMapper(names []string) handler.MapFunc {
	nameSet := sets.NewString(names...)
	return func(obj client.Object) []reconcile.Request {
		if !nameSet.Has(obj.GetName()) {
			return nil
		}
		return []reconcile.Request{
			{
				NamespacedName: types.NamespacedName{
					Namespace: obj.GetNamespace(),
					Name:      obj.GetName(),
				},
			},
		}
	}
}

func namedResourceHandler(names ...string) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(nameMapper(names))
}

func ControllerName(name string) string {
	return fmt.Sprintf("%s-observer", name)
}

func (r *PrivateServiceObserver) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	r.log = ctrl.Log.WithName(r.ControllerName).WithValues("name", r.ServiceName, "namespace", r.ServiceNamespace)
	r.client = mgr.GetClient()
	var err error
	kubeClient, err := kubeclient.NewForConfig(mgr.GetConfig())
	if err != nil {
		return err
	}
	informerFactory := informers.NewSharedInformerFactoryWithOptions(kubeClient, defaultResync, informers.WithNamespace(r.ServiceNamespace))
	services := informerFactory.Core().V1().Services()
	c, err := controller.New(r.ControllerName, mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}
	if err := c.Watch(&source.Informer{Informer: services.Informer()}, namedResourceHandler(r.ServiceName)); err != nil {
		return err
	}
	mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		informerFactory.Start(ctx.Done())
		return nil
	}))
	return nil
}

func (r *PrivateServiceObserver) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.log.Info("reconcile start", "request", req)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
		},
	}
	if err := r.client.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		return ctrl.Result{}, err
	}
	if len(svc.Status.LoadBalancer.Ingress) == 0 {
		r.log.Info("load balancer not provisioned yet")
		return ctrl.Result{}, nil
	}
	awsEndpointService := &hyperv1.AWSEndpointService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.ServiceName,
			Namespace: r.HCPNamespace,
		},
	}
	lbName := strings.Split(strings.Split(svc.Status.LoadBalancer.Ingress[0].Hostname, ".")[0], "-")[0]
	if _, err := r.CreateOrUpdate(ctx, r.client, awsEndpointService, func() error {
		return reconcileAWSEndpointService(awsEndpointService, lbName)
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile AWS Endpoint Service: %w", err)
	}
	r.log.Info("reconcile complete", "request", req)
	return ctrl.Result{}, nil
}

func reconcileAWSEndpointService(awsEndpointService *hyperv1.AWSEndpointService, lbName string) error {
	awsEndpointService.Spec.NetworkLoadBalancerName = lbName
	return nil
}
