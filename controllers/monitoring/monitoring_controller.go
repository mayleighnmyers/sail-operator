// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package monitoring

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	v1 "github.com/istio-ecosystem/sail-operator/api/v1"
	"github.com/istio-ecosystem/sail-operator/pkg/config"
	"github.com/istio-ecosystem/sail-operator/pkg/constants"
	"github.com/istio-ecosystem/sail-operator/pkg/enqueuelogger"
	monitoringapigroup "github.com/istio-ecosystem/sail-operator/pkg/monitoring"
	"github.com/istio-ecosystem/sail-operator/pkg/monitoring/relabeling"
	"github.com/istio-ecosystem/sail-operator/pkg/reconciler"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	serviceMonitorNameSuffix = "-istiod"
	podMonitorNameSuffix     = "-proxies"

	// cooMonitoredByLabel is the label key used so a Prometheus stack selects these monitors.
	// cooMonitoredByValue is hardcoded for the interim Istio.spec.monitoring.enabled flow;
	// we will update this value once we determine where to source it (e.g. MonitoringStack ref).
	cooMonitoredByLabel = "monitored-by"
	cooMonitoredByValue = "coo-prometheus"
)

// Reconciler reconciles monitoring resources (ServiceMonitor, PodMonitor) for IstioRevision objects.
type Reconciler struct {
	client.Client
	Config     config.ReconcilerConfig
	Scheme     *runtime.Scheme
	RESTMapper monitoringapigroup.RESTMapper
}

// NewReconciler creates a new monitoring Reconciler.
func NewReconciler(cfg config.ReconcilerConfig, client client.Client, scheme *runtime.Scheme) *Reconciler {
	return &Reconciler{
		Config: cfg,
		Client: client,
		Scheme: scheme,
	}
}

// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=podmonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.rhobs,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.rhobs,resources=podmonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

// Reconcile creates or updates ServiceMonitor and PodMonitor resources for each IstioRevision.
func (r *Reconciler) Reconcile(ctx context.Context, rev *v1.IstioRevision) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if rev.DeletionTimestamp != nil {
		log.V(2).Info("IstioRevision is being deleted, skipping monitoring reconciliation")
		return ctrl.Result{}, nil
	}

	enabled, err := r.isMonitoringEnabled(ctx, rev)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check if monitoring is enabled: %w", err)
	}
	if !enabled {
		log.V(2).Info("Monitoring is not enabled in Istio CR, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Using monitoring API group", "group", r.monitoringGV().Group)

	if err := r.reconcileServiceMonitor(ctx, rev); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile ServiceMonitor: %w", err)
	}

	if err := r.reconcilePodMonitors(ctx, rev); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile PodMonitors: %w", err)
	}

	log.Info("Monitoring resources reconciled successfully")
	return ctrl.Result{}, nil
}

func (r *Reconciler) monitoringGV() schema.GroupVersion {
	return monitoringapigroup.DetectMonitoringAPIGroup(r.RESTMapper)
}

func (r *Reconciler) isMonitoringEnabled(ctx context.Context, rev *v1.IstioRevision) (bool, error) {
	for _, ownerRef := range rev.GetOwnerReferences() {
		if ownerRef.Kind == v1.IstioKind {
			istio := &v1.Istio{}
			if err := r.Client.Get(ctx, client.ObjectKey{Name: ownerRef.Name}, istio); err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, fmt.Errorf("failed to get Istio CR: %w", err)
			}
			return istio.Spec.Monitoring != nil && istio.Spec.Monitoring.Enabled, nil
		}
	}
	return false, nil
}

func (r *Reconciler) reconcileServiceMonitor(ctx context.Context, rev *v1.IstioRevision) error {
	log := logf.FromContext(ctx)
	desired := r.buildServiceMonitor(rev)

	existing := &monitoringv1.ServiceMonitor{}
	existing.SetGroupVersionKind(r.monitoringGV().WithKind("ServiceMonitor"))

	err := r.Client.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Creating ServiceMonitor", "name", desired.GetName(), "namespace", desired.GetNamespace())
			return r.Client.Create(ctx, desired)
		}
		return fmt.Errorf("failed to get ServiceMonitor: %w", err)
	}

	log.V(2).Info("Updating ServiceMonitor", "name", desired.GetName(), "namespace", desired.GetNamespace())
	desired.SetResourceVersion(existing.GetResourceVersion())
	return r.Client.Update(ctx, desired)
}

func (r *Reconciler) reconcilePodMonitors(ctx context.Context, rev *v1.IstioRevision) error {
	log := logf.FromContext(ctx)

	nsList := &corev1.NamespaceList{}
	if err := r.Client.List(ctx, nsList, client.MatchingLabels{
		constants.IstioInjectionLabel: constants.IstioInjectionEnabledValue,
	}); err != nil {
		return fmt.Errorf("failed to list namespaces: %w", err)
	}

	for _, ns := range nsList.Items {
		if ns.Name == rev.Spec.Namespace {
			log.V(2).Info("Skipping PodMonitor for control plane namespace", "namespace", ns.Name)
			continue
		}

		if err := r.reconcilePodMonitorInNamespace(ctx, rev, ns.Name); err != nil {
			return fmt.Errorf("failed to reconcile PodMonitor in namespace %s: %w", ns.Name, err)
		}
	}

	return nil
}

func (r *Reconciler) reconcilePodMonitorInNamespace(ctx context.Context, rev *v1.IstioRevision, namespace string) error {
	log := logf.FromContext(ctx)
	desired := r.buildPodMonitor(rev, namespace)

	existing := &monitoringv1.PodMonitor{}
	existing.SetGroupVersionKind(r.monitoringGV().WithKind("PodMonitor"))

	err := r.Client.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Creating PodMonitor", "name", desired.GetName(), "namespace", namespace)
			return r.Client.Create(ctx, desired)
		}
		return fmt.Errorf("failed to get PodMonitor: %w", err)
	}

	log.V(2).Info("Updating PodMonitor", "name", desired.GetName(), "namespace", namespace)
	desired.SetResourceVersion(existing.GetResourceVersion())
	return r.Client.Update(ctx, desired)
}

func (r *Reconciler) buildServiceMonitor(rev *v1.IstioRevision) *monitoringv1.ServiceMonitor {
	name := rev.Name + serviceMonitorNameSuffix
	namespace := rev.Spec.Namespace
	relabelCfg := relabeling.ForPlatform(r.Config.Platform, istioOwnerName(rev))

	sm := &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app":               "istiod",
				cooMonitoredByLabel: cooMonitoredByValue,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         v1.GroupVersion.String(),
					Kind:               v1.IstioRevisionKind,
					Name:               rev.Name,
					UID:                rev.UID,
					Controller:         ptr(true),
					BlockOwnerDeletion: ptr(true),
				},
			},
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			TargetLabels: []string{"app"},
			Selector: metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "istio",
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{"pilot"},
					},
				},
			},
			Endpoints: []monitoringv1.Endpoint{
				{
					Port:           "http-monitoring",
					Path:           "/metrics",
					Scheme:         ptr(monitoringv1.Scheme("http")),
					Interval:       monitoringv1.Duration("30s"),
					RelabelConfigs: relabelCfg.ServiceMonitorRelabelings,
				},
			},
		},
	}

	sm.SetGroupVersionKind(r.monitoringGV().WithKind("ServiceMonitor"))
	return sm
}

func (r *Reconciler) buildPodMonitor(rev *v1.IstioRevision, namespace string) *monitoringv1.PodMonitor {
	name := rev.Name + podMonitorNameSuffix
	relabelCfg := relabeling.ForPlatform(r.Config.Platform, istioOwnerName(rev))

	pm := &monitoringv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app":               "istio-proxy",
				cooMonitoredByLabel: cooMonitoredByValue,
			},
		},
		Spec: monitoringv1.PodMonitorSpec{
			Selector: metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "istio-prometheus-ignore",
						Operator: metav1.LabelSelectorOpDoesNotExist,
					},
				},
			},
			PodMetricsEndpoints: []monitoringv1.PodMetricsEndpoint{
				{
					Path:           "/stats/prometheus",
					Scheme:         ptr(monitoringv1.Scheme("http")),
					Interval:       monitoringv1.Duration("30s"),
					RelabelConfigs: relabelCfg.PodMonitorRelabelings,
				},
			},
		},
	}

	pm.SetGroupVersionKind(r.monitoringGV().WithKind("PodMonitor"))
	return pm
}

func istioOwnerName(rev *v1.IstioRevision) string {
	for _, ownerRef := range rev.GetOwnerReferences() {
		if ownerRef.Kind == v1.IstioKind {
			return ownerRef.Name
		}
	}
	return rev.Name
}

func ptr[T any](v T) *T {
	return &v
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.RESTMapper = mgr.GetRESTMapper()

	logger := mgr.GetLogger().WithName("ctrlr").WithName("monitoring")

	namespaceHandler := wrapEventHandler(logger, handler.EnqueueRequestsFromMapFunc(r.mapNamespaceToReconcileRequest))
	istioHandler := wrapEventHandler(logger, handler.EnqueueRequestsFromMapFunc(r.mapIstioToReconcileRequest))

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{
			LogConstructor: func(req *reconcile.Request) logr.Logger {
				log := logger
				if req != nil {
					log = log.WithValues("IstioRevision", req.Name)
				}
				return log
			},
			MaxConcurrentReconciles: r.Config.MaxConcurrentReconciles,
		}).
		Named("monitoring").
		Watches(&v1.IstioRevision{}, wrapEventHandler(logger, &handler.EnqueueRequestForObject{})).
		Watches(&v1.Istio{}, istioHandler).
		Watches(&corev1.Namespace{}, namespaceHandler, builder.WithPredicates(injectionEnabledPredicate())).
		Complete(reconciler.NewStandardReconciler[*v1.IstioRevision](r.Client, r.Reconcile))
}

func (r *Reconciler) mapNamespaceToReconcileRequest(ctx context.Context, obj client.Object) []reconcile.Request {
	log := logf.FromContext(ctx)
	ns, ok := obj.(*corev1.Namespace)
	if !ok {
		log.Error(nil, "unexpected object type", "type", fmt.Sprintf("%T", obj))
		return nil
	}

	revList := &v1.IstioRevisionList{}
	if err := r.Client.List(ctx, revList); err != nil {
		log.Error(err, "failed to list IstioRevisions")
		return nil
	}

	requests := make([]reconcile.Request, 0, len(revList.Items))
	for _, rev := range revList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(&rev),
		})
	}

	log.V(2).Info("Namespace with injection label changed, queuing IstioRevisions for reconciliation",
		"namespace", ns.Name, "revisionCount", len(requests))
	return requests
}

func (r *Reconciler) mapIstioToReconcileRequest(ctx context.Context, obj client.Object) []reconcile.Request {
	log := logf.FromContext(ctx)
	istio, ok := obj.(*v1.Istio)
	if !ok {
		log.Error(nil, "unexpected object type", "type", fmt.Sprintf("%T", obj))
		return nil
	}

	revList := &v1.IstioRevisionList{}
	if err := r.Client.List(ctx, revList); err != nil {
		log.Error(err, "failed to list IstioRevisions")
		return nil
	}

	requests := make([]reconcile.Request, 0)
	for _, rev := range revList.Items {
		for _, ownerRef := range rev.GetOwnerReferences() {
			if ownerRef.Kind == v1.IstioKind && ownerRef.Name == istio.Name {
				requests = append(requests, reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(&rev),
				})
				break
			}
		}
	}

	log.V(2).Info("Istio CR changed, queuing owned IstioRevisions for reconciliation",
		"istio", istio.Name, "revisionCount", len(requests))
	return requests
}

func injectionEnabledPredicate() predicate.Funcs {
	hasInjectionEnabled := func(obj client.Object) bool {
		if obj == nil {
			return false
		}
		labels := obj.GetLabels()
		return labels != nil && labels[constants.IstioInjectionLabel] == constants.IstioInjectionEnabledValue
	}

	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return hasInjectionEnabled(e.Object)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return hasInjectionEnabled(e.ObjectOld) != hasInjectionEnabled(e.ObjectNew)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return hasInjectionEnabled(e.Object)
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return hasInjectionEnabled(e.Object)
		},
	}
}

func wrapEventHandler(logger logr.Logger, h handler.EventHandler) handler.EventHandler {
	return enqueuelogger.WrapIfNecessary(v1.IstioRevisionKind, logger, h)
}
