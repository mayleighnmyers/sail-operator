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
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	v1 "github.com/istio-ecosystem/sail-operator/api/v1"
	"github.com/istio-ecosystem/sail-operator/api/v1alpha1"
	"github.com/istio-ecosystem/sail-operator/pkg/enqueuelogger"
	"github.com/istio-ecosystem/sail-operator/pkg/reconciler"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	targetKindKiali  = "Kiali"
	targetKindPerses = "Perses"
)

type duplicateTargetError struct {
	Message string
}

func (err duplicateTargetError) Error() string {
	return err.Message
}

func isDuplicateTargetError(err error) bool {
	var e duplicateTargetError
	return errors.As(err, &e)
}

type notImplementedError struct {
	Message string
}

func (err notImplementedError) Error() string {
	return err.Message
}

func isNotImplementedError(err error) bool {
	var e notImplementedError
	return errors.As(err, &e)
}

// +kubebuilder:rbac:groups=sailoperator.io,resources=metricsintegrations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sailoperator.io,resources=metricsintegrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sailoperator.io,resources=metricsintegrations/finalizers,verbs=update
// +kubebuilder:rbac:groups=sailoperator.io,resources=istios;istiorevisions,verbs=get;list;watch
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors;podmonitors,verbs=get;list;create;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *Reconciler) ReconcileMetricsIntegration(ctx context.Context, mi *v1alpha1.MetricsIntegration) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("MetricsIntegration", mi.Name)

	reconcileErr := r.doReconcileMetricsIntegration(ctx, mi)

	log.Info("Reconciliation done. Updating status.")
	statusErr := r.updateStatus(ctx, mi, reconcileErr)

	return ctrl.Result{}, errors.Join(reconcileErr, statusErr)
}

func (r *Reconciler) doReconcileMetricsIntegration(ctx context.Context, mi *v1alpha1.MetricsIntegration) error {
	if err := r.validate(mi); err != nil {
		return err
	}

	list := &v1alpha1.MetricsIntegrationList{}
	if err := r.Client.List(ctx, list); err != nil {
		return fmt.Errorf("failed to list MetricsIntegrations: %w", err)
	}
	if target := duplicateTargetOf(mi, list.Items); target != "" {
		return duplicateTargetError{Message: "a MetricsIntegration already exists for " + target}
	}

	switch mi.Spec.Type {
	case v1alpha1.MetricsTypeUserWorkloadMonitoring:
		return r.reconcileUserWorkloadMonitoring(ctx, mi)
	case v1alpha1.MetricsTypeClusterObservabilityOperator:
		return notImplementedError{Message: string(v1alpha1.MetricsTypeClusterObservabilityOperator) + " is not yet implemented"}
	default:
		return reconciler.NewValidationError("unsupported metrics integration type: " + string(mi.Spec.Type))
	}
}

func (r *Reconciler) validate(mi *v1alpha1.MetricsIntegration) error {
	if len(mi.Spec.TargetRefs) == 0 {
		return reconciler.NewValidationError("spec.targetRefs not set")
	}
	if mi.Spec.Type == "" {
		return reconciler.NewValidationError("spec.type not set")
	}
	for _, ref := range mi.Spec.TargetRefs {
		if ref.Kind == "" || ref.Name == "" {
			return reconciler.NewValidationError("spec.targetRefs kind and name not set")
		}
	}

	switch mi.Spec.Type {
	case v1alpha1.MetricsTypeUserWorkloadMonitoring:
		if mi.Spec.UserWorkloadMonitoring == nil {
			return reconciler.NewValidationError("spec.userWorkloadMonitoring is required when type is UserWorkloadMonitoring")
		}
		if mi.Spec.ClusterObservabilityOperator != nil {
			return reconciler.NewValidationError("spec.clusterObservabilityOperator must not be set when type is UserWorkloadMonitoring")
		}
	case v1alpha1.MetricsTypeClusterObservabilityOperator:
		if mi.Spec.ClusterObservabilityOperator == nil {
			return reconciler.NewValidationError("spec.clusterObservabilityOperator is required when type is ClusterObservabilityOperator")
		}
		if mi.Spec.UserWorkloadMonitoring != nil {
			return reconciler.NewValidationError("spec.userWorkloadMonitoring must not be set when type is ClusterObservabilityOperator")
		}
	}

	return nil
}

func (r *Reconciler) reconcileUserWorkloadMonitoring(ctx context.Context, mi *v1alpha1.MetricsIntegration) error {
	for _, ref := range mi.Spec.TargetRefs {
		switch ref.Kind {
		case v1.IstioKind:
			istio := &v1.Istio{}
			if err := r.Client.Get(ctx, client.ObjectKey{Name: ref.Name}, istio); err != nil {
				if apierrors.IsNotFound(err) {
					return reconciler.NewReferenceNotFoundError("referenced Istio resource does not exist", err)
				}
				return fmt.Errorf("failed to get referenced Istio resource: %w", err)
			}
			if err := r.doReconcile(ctx, istio); err != nil {
				return err
			}
		case targetKindKiali, targetKindPerses:
			// TODO: configure Kiali and Perses in a follow-up
			continue
		default:
			return reconciler.NewValidationError("unsupported target kind: " + ref.Kind)
		}
	}
	return nil
}

// setupMetricsIntegrationController sets up the MetricsIntegration controller with the Manager.
func (r *Reconciler) setupMetricsIntegrationController(mgr ctrl.Manager) error {
	logger := mgr.GetLogger().WithName("ctrlr").WithName("metricsintegration")

	// mainObjectHandler handles the MetricsIntegration watch events
	mainObjectHandler := wrapMetricsIntegrationEventHandler(logger, &handler.EnqueueRequestForObject{})

	// istioHandler enqueues MetricsIntegrations that reference the Istio
	istioHandler := wrapMetricsIntegrationEventHandler(logger, handler.EnqueueRequestsFromMapFunc(r.mapIstioToMetricsIntegration))

	// revisionHandler enqueues MetricsIntegrations that reference the Istio that owns the IstioRevision
	revisionHandler := wrapMetricsIntegrationEventHandler(logger, handler.EnqueueRequestsFromMapFunc(r.mapIstioRevisionToMetricsIntegration))

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{
			LogConstructor: func(req *reconcile.Request) logr.Logger {
				log := logger
				if req != nil {
					log = log.WithValues("MetricsIntegration", req.Name)
				}
				return log
			},
			MaxConcurrentReconciles: r.Config.MaxConcurrentReconciles,
		}).
		Named("metricsintegration").
		// we use the Watches function instead of For(), so that we can wrap the handler so that events that cause the object to be enqueued are logged
		Watches(&v1alpha1.MetricsIntegration{}, mainObjectHandler).
		// Watch Istio so create/update/delete requeues MetricsIntegrations that reference it.
		Watches(&v1.Istio{}, istioHandler).
		// Watch IstioRevisions so create/update/delete requeues MetricsIntegrations that reference the parent Istio.
		Watches(&v1.IstioRevision{}, revisionHandler).
		Complete(reconciler.NewStandardReconciler[*v1alpha1.MetricsIntegration](r.Client, r.ReconcileMetricsIntegration))
}

func (r *Reconciler) determineStatus(mi *v1alpha1.MetricsIntegration, reconcileErr error) v1alpha1.MetricsIntegrationStatus {
	reconciledCondition := r.determineReconciledCondition(reconcileErr)

	status := *mi.Status.DeepCopy()
	status.ObservedGeneration = mi.Generation
	status.SetCondition(reconciledCondition)
	status.State = v1alpha1.MetricsIntegrationConditionReason(
		reconciler.DeriveState(v1.ConditionReason(v1alpha1.MetricsIntegrationReasonHealthy), reconciledCondition))
	return status
}

func (r *Reconciler) updateStatus(ctx context.Context, mi *v1alpha1.MetricsIntegration, reconcileErr error) error {
	status := r.determineStatus(mi, reconcileErr)
	return reconciler.UpdateStatus(ctx, r.Client, mi, mi.Status, status, nil)
}

func (r *Reconciler) determineReconciledCondition(err error) v1.StatusCondition {
	c := v1.StatusCondition{Type: v1.ConditionType(v1alpha1.MetricsIntegrationConditionReconciled)}
	if err == nil {
		c.Status = metav1.ConditionTrue
		c.Reason = v1.ConditionReason(v1alpha1.MetricsIntegrationConditionReconciled)
	} else {
		c.Status = metav1.ConditionFalse
		c.Message = err.Error()
		switch {
		case isDuplicateTargetError(err):
			c.Reason = v1.ConditionReason(v1alpha1.MetricsIntegrationReasonDuplicateTarget)
		case isNotImplementedError(err):
			c.Reason = v1.ConditionReason(v1alpha1.MetricsIntegrationReasonNotImplemented)
		case reconciler.IsReferenceNotFoundError(err):
			c.Reason = v1.ConditionReason(v1alpha1.MetricsIntegrationReasonReferenceNotFound)
		case reconciler.IsValidationError(err):
			c.Reason = v1.ConditionReason(v1alpha1.MetricsIntegrationReasonInvalidSpec)
		default:
			c.Reason = v1.ConditionReason(v1alpha1.MetricsIntegrationReasonReconcileError)
			c.Message = fmt.Sprintf("error reconciling resource: %v", err)
		}
	}
	return c
}

// mapIstioToMetricsIntegration returns reconcile requests for MetricsIntegrations that reference the Istio.
func (r *Reconciler) mapIstioToMetricsIntegration(ctx context.Context, obj client.Object) []reconcile.Request {
	log := logf.FromContext(ctx)
	istio, ok := obj.(*v1.Istio)
	if !ok {
		log.Error(nil, "unexpected object type", "type", fmt.Sprintf("%T", obj))
		return nil
	}
	return r.metricsIntegrationsTargetingIstio(ctx, istio.Name)
}

// mapIstioRevisionToMetricsIntegration returns reconcile requests for MetricsIntegrations that
// reference the Istio that owns the IstioRevision.
func (r *Reconciler) mapIstioRevisionToMetricsIntegration(ctx context.Context, obj client.Object) []reconcile.Request {
	log := logf.FromContext(ctx)
	rev, ok := obj.(*v1.IstioRevision)
	if !ok {
		log.Error(nil, "unexpected object type", "type", fmt.Sprintf("%T", obj))
		return nil
	}
	istioName := ownerIstioName(rev)
	if istioName == "" {
		return nil
	}
	return r.metricsIntegrationsTargetingIstio(ctx, istioName)
}

func (r *Reconciler) metricsIntegrationsTargetingIstio(ctx context.Context, istioName string) []reconcile.Request {
	log := logf.FromContext(ctx)
	list := &v1alpha1.MetricsIntegrationList{}
	if err := r.Client.List(ctx, list); err != nil {
		if !apimeta.IsNoMatchError(err) {
			log.Error(err, "failed to list MetricsIntegrations", "Istio", istioName)
		}
		return nil
	}

	var requests []reconcile.Request
	for i := range list.Items {
		mi := &list.Items[i]
		if hasIstioTarget(mi, istioName) {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Name: mi.Name}})
		}
	}
	return requests
}

// mapMetricsIntegrationToIstio returns reconcile requests for Istio CRs referenced by the MetricsIntegration.
func (r *Reconciler) mapMetricsIntegrationToIstio(ctx context.Context, obj client.Object) []reconcile.Request {
	log := logf.FromContext(ctx)
	mi, ok := obj.(*v1alpha1.MetricsIntegration)
	if !ok {
		log.Error(nil, "unexpected object type", "type", fmt.Sprintf("%T", obj))
		return nil
	}

	var requests []reconcile.Request
	seen := map[string]struct{}{}
	for _, ref := range mi.Spec.TargetRefs {
		if ref.Kind != v1.IstioKind {
			continue
		}
		if _, exists := seen[ref.Name]; exists {
			continue
		}
		seen[ref.Name] = struct{}{}
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Name: ref.Name}})
	}
	return requests
}

func (r *Reconciler) hasUserWorkloadMetricsIntegration(ctx context.Context, istioName string) (bool, error) {
	list := &v1alpha1.MetricsIntegrationList{}
	if err := r.Client.List(ctx, list); err != nil {
		if apimeta.IsNoMatchError(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to list MetricsIntegrations: %w", err)
	}

	for i := range list.Items {
		mi := &list.Items[i]
		if !mi.DeletionTimestamp.IsZero() {
			continue
		}
		if mi.Spec.Type != v1alpha1.MetricsTypeUserWorkloadMonitoring {
			continue
		}
		if duplicateTargetOf(mi, list.Items) != "" {
			continue
		}
		if hasIstioTarget(mi, istioName) {
			return true, nil
		}
	}
	return false, nil
}

func hasIstioTarget(mi *v1alpha1.MetricsIntegration, istioName string) bool {
	for _, ref := range mi.Spec.TargetRefs {
		if ref.Kind == v1.IstioKind && ref.Name == istioName {
			return true
		}
	}
	return false
}

func hasTarget(mi *v1alpha1.MetricsIntegration, ref v1alpha1.TargetReference) bool {
	for _, other := range mi.Spec.TargetRefs {
		if targetKey(other) == targetKey(ref) {
			return true
		}
	}
	return false
}

func targetKey(ref v1alpha1.TargetReference) string {
	return ref.Kind + "/" + ref.Namespace + "/" + ref.Name
}

func isLaterCreated(a, b *v1alpha1.MetricsIntegration) bool {
	at, bt := a.CreationTimestamp, b.CreationTimestamp
	if !at.Equal(&bt) {
		return at.After(bt.Time)
	}
	return a.Name > b.Name
}

func duplicateTargetOf(mi *v1alpha1.MetricsIntegration, all []v1alpha1.MetricsIntegration) string {
	for _, ref := range mi.Spec.TargetRefs {
		for i := range all {
			other := &all[i]
			if other.Name == mi.Name || !other.DeletionTimestamp.IsZero() {
				continue
			}
			if !hasTarget(other, ref) {
				continue
			}
			if isLaterCreated(mi, other) {
				if ref.Namespace != "" {
					return fmt.Sprintf("%s %s/%s (referenced by %s)", ref.Kind, ref.Namespace, ref.Name, other.Name)
				}
				return fmt.Sprintf("%s %s (referenced by %s)", ref.Kind, ref.Name, other.Name)
			}
		}
	}
	return ""
}

func wrapMetricsIntegrationEventHandler(logger logr.Logger, handler handler.EventHandler) handler.EventHandler {
	return enqueuelogger.WrapIfNecessary(v1alpha1.MetricsIntegrationKind, logger, handler)
}
