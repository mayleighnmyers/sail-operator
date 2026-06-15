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

package integration

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	integrationv1alpha1 "github.com/istio-ecosystem/sail-operator/api/integration/v1alpha1"
	v1 "github.com/istio-ecosystem/sail-operator/api/v1"
	"github.com/istio-ecosystem/sail-operator/pkg/config"
	"github.com/istio-ecosystem/sail-operator/pkg/constants"
	"github.com/istio-ecosystem/sail-operator/pkg/enqueuelogger"
	monitoringpkg "github.com/istio-ecosystem/sail-operator/pkg/monitoring"
	"github.com/istio-ecosystem/sail-operator/pkg/reconciler"
	"github.com/istio-ecosystem/sail-operator/pkg/revision"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"istio.io/istio/pkg/ptr"
)

// Reconciler reconciles Integration objects.
type Reconciler struct {
	client.Client
	Config     config.ReconcilerConfig
	Scheme     *runtime.Scheme
	RESTMapper monitoringpkg.RESTMapper
}

// NewReconciler creates a new Integration Reconciler.
func NewReconciler(cfg config.ReconcilerConfig, cl client.Client, scheme *runtime.Scheme) *Reconciler {
	return &Reconciler{
		Config: cfg,
		Client: cl,
		Scheme: scheme,
	}
}

// +kubebuilder:rbac:groups=integration.ossm,resources=integrations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=integration.ossm,resources=integrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=monitoring.rhobs,resources=monitoringstacks,verbs=get;list;watch
// +kubebuilder:rbac:groups=monitoring.rhobs,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.rhobs,resources=podmonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

// Reconcile reconciles an Integration resource.
func (r *Reconciler) Reconcile(ctx context.Context, integration *integrationv1alpha1.Integration) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("Integration", client.ObjectKeyFromObject(integration))

	rev, reconcileErr := r.doReconcile(ctx, integration)

	log.Info("Reconciliation done. Updating status.")
	statusErr := r.updateStatus(ctx, integration, rev, reconcileErr)

	return ctrl.Result{}, errors.Join(reconcileErr, statusErr)
}

func (r *Reconciler) doReconcile(ctx context.Context, integration *integrationv1alpha1.Integration) (*v1.IstioRevision, error) {
	log := logf.FromContext(ctx)

	if integration.Spec.Metrics == nil {
		return nil, reconciler.NewValidationError("spec.metrics is required for metrics integration")
	}
	if integration.Spec.Metrics.Type != integrationv1alpha1.MetricsIntegrationTypeClusterObservability {
		return nil, reconciler.NewValidationError(fmt.Sprintf("unsupported metrics type %q", integration.Spec.Metrics.Type))
	}

	log.Info("Resolving referenced IstioRevision")
	rev, err := revision.GetIstioRevisionFromTargetReference(ctx, r.Client, v1.TargetReference{
		Kind: v1.IstioKind,
		Name: integration.Spec.IstioRef.Name,
	})
	if err != nil {
		return nil, err
	}

	log.Info("Reading MonitoringStack selector labels")
	monitorLabels, err := monitoringpkg.LabelsFromMonitoringStack(
		ctx, r.Client, integration.Spec.Metrics.ClusterObservability.MonitoringStackRef,
	)
	if err != nil {
		return nil, err
	}

	ownerRef := metav1.OwnerReference{
		APIVersion:         integrationv1alpha1.GroupVersion.String(),
		Kind:               integrationv1alpha1.IntegrationKind,
		Name:               integration.Name,
		UID:                integration.UID,
		Controller:         ptr.Of(true),
		BlockOwnerDeletion: ptr.Of(true),
	}

	opts := monitoringpkg.Options{
		Platform:      r.Config.Platform,
		IstioName:     integration.Spec.IstioRef.Name,
		MonitorLabels: monitorLabels,
		Owner:         &ownerRef,
		RESTMapper:    r.RESTMapper,
		ForCOO:        true,
	}

	if err := monitoringpkg.ReconcileRevision(ctx, r.Client, rev, opts); err != nil {
		return nil, err
	}

	return rev, nil
}

func (r *Reconciler) updateStatus(
	ctx context.Context,
	integration *integrationv1alpha1.Integration,
	rev *v1.IstioRevision,
	reconcileErr error,
) error {
	newStatus := integration.Status
	newStatus.ObservedGeneration = integration.Generation

	if rev != nil {
		newStatus.IstioRevision = rev.Name
	} else {
		newStatus.IstioRevision = ""
	}

	condition := metav1.Condition{
		Type:               string(integrationv1alpha1.IntegrationConditionReconciled),
		ObservedGeneration: integration.Generation,
	}

	switch {
	case reconcileErr == nil:
		condition.Status = metav1.ConditionTrue
		condition.Reason = string(integrationv1alpha1.IntegrationReasonHealthy)
		condition.Message = "Metrics integration reconciled successfully"
		newStatus.State = integrationv1alpha1.IntegrationReasonHealthy
	case reconciler.IsValidationError(reconcileErr):
		condition.Status = metav1.ConditionFalse
		condition.Reason = string(integrationv1alpha1.IntegrationReasonInvalidSpec)
		condition.Message = reconcileErr.Error()
		newStatus.State = integrationv1alpha1.IntegrationReasonInvalidSpec
	case reconciler.IsTransientError(reconcileErr):
		condition.Status = metav1.ConditionFalse
		condition.Reason = string(integrationv1alpha1.IntegrationReasonReferenceNotFound)
		condition.Message = reconcileErr.Error()
		newStatus.State = integrationv1alpha1.IntegrationReasonReferenceNotFound
	default:
		condition.Status = metav1.ConditionFalse
		condition.Reason = string(integrationv1alpha1.IntegrationReasonReconcileError)
		condition.Message = reconcileErr.Error()
		newStatus.State = integrationv1alpha1.IntegrationReasonReconcileError
	}

	setIntegrationCondition(&newStatus.Conditions, condition)

	return reconciler.UpdateStatus(ctx, r.Client, integration, integration.Status, newStatus, nil)
}

func setIntegrationCondition(conditions *[]metav1.Condition, condition metav1.Condition) {
	if conditions == nil {
		return
	}

	for i, existing := range *conditions {
		if existing.Type == condition.Type {
			if existing.Status == condition.Status && existing.Reason == condition.Reason && existing.Message == condition.Message {
				return
			}
			condition.LastTransitionTime = metav1.Now()
			(*conditions)[i] = condition
			return
		}
	}

	condition.LastTransitionTime = metav1.Now()
	*conditions = append(*conditions, condition)
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.RESTMapper = mgr.GetRESTMapper()

	logger := mgr.GetLogger().WithName("ctrlr").WithName("integration")

	istioHandler := wrapEventHandler(logger, handler.EnqueueRequestsFromMapFunc(r.mapIstioToIntegrations))
	namespaceHandler := wrapEventHandler(logger, handler.EnqueueRequestsFromMapFunc(r.mapNamespaceToIntegrations))

	return ctrl.NewControllerManagedBy(mgr).
		For(&integrationv1alpha1.Integration{}).
		WithOptions(controller.Options{
			LogConstructor: func(req *reconcile.Request) logr.Logger {
				log := logger
				if req != nil {
					log = log.WithValues("Integration", req.Namespace+"/"+req.Name)
				}
				return log
			},
			MaxConcurrentReconciles: r.Config.MaxConcurrentReconciles,
		}).
		Named("integration").
		Watches(&v1.Istio{}, istioHandler).
		Watches(&corev1.Namespace{}, namespaceHandler, builder.WithPredicates(injectionEnabledPredicate())).
		Complete(reconciler.NewStandardReconciler[*integrationv1alpha1.Integration](r.Client, r.Reconcile))
}

func (r *Reconciler) mapIstioToIntegrations(ctx context.Context, obj client.Object) []reconcile.Request {
	log := logf.FromContext(ctx)
	istio, ok := obj.(*v1.Istio)
	if !ok {
		log.Error(nil, "unexpected object type", "type", fmt.Sprintf("%T", obj))
		return nil
	}

	return r.listIntegrationsForIstio(ctx, istio.Name)
}

func (r *Reconciler) mapNamespaceToIntegrations(ctx context.Context, obj client.Object) []reconcile.Request {
	log := logf.FromContext(ctx)
	if _, ok := obj.(*corev1.Namespace); !ok {
		log.Error(nil, "unexpected object type", "type", fmt.Sprintf("%T", obj))
		return nil
	}

	return r.listAllIntegrations(ctx)
}

func (r *Reconciler) listIntegrationsForIstio(ctx context.Context, istioName string) []reconcile.Request {
	integrationList := &integrationv1alpha1.IntegrationList{}
	if err := r.Client.List(ctx, integrationList); err != nil {
		logf.FromContext(ctx).Error(err, "failed to list Integrations")
		return nil
	}

	requests := make([]reconcile.Request, 0)
	for _, integration := range integrationList.Items {
		if integration.Spec.IstioRef.Name == istioName {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      integration.Name,
					Namespace: integration.Namespace,
				},
			})
		}
	}
	return requests
}

func (r *Reconciler) listAllIntegrations(ctx context.Context) []reconcile.Request {
	integrationList := &integrationv1alpha1.IntegrationList{}
	if err := r.Client.List(ctx, integrationList); err != nil {
		logf.FromContext(ctx).Error(err, "failed to list Integrations")
		return nil
	}

	requests := make([]reconcile.Request, 0, len(integrationList.Items))
	for _, integration := range integrationList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      integration.Name,
				Namespace: integration.Namespace,
			},
		})
	}
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
	return enqueuelogger.WrapIfNecessary(integrationv1alpha1.IntegrationKind, logger, h)
}
