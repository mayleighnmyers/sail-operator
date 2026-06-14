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

	v1 "github.com/istio-ecosystem/sail-operator/api/v1"
	"github.com/istio-ecosystem/sail-operator/pkg/config"
	"github.com/istio-ecosystem/sail-operator/pkg/constants"
	"github.com/istio-ecosystem/sail-operator/pkg/scheme"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	ServiceMonitorNameSuffix = "-istiod"
	PodMonitorNameSuffix     = "-proxies"

	DefaultMonitoredByLabel = "monitored-by"
	DefaultMonitoredByValue = "coo-prometheus"
)

var MonitoringGV = schema.GroupVersion{Group: scheme.RhobsAPIGroup, Version: "v1"}

// Options configures monitor reconciliation for an IstioRevision.
type Options struct {
	Platform      config.Platform
	IstioName     string
	MonitorLabels map[string]string
	Owner         *metav1.OwnerReference
}

// DefaultOptions returns monitor options used by the interim Istio.spec.monitoring.enabled flow.
func DefaultOptions(platform config.Platform, istioName string) Options {
	return Options{
		Platform:  platform,
		IstioName: istioName,
		MonitorLabels: map[string]string{
			DefaultMonitoredByLabel: DefaultMonitoredByValue,
		},
	}
}

// ReconcileRevision creates or updates ServiceMonitor and PodMonitor resources for the revision.
func ReconcileRevision(ctx context.Context, c client.Client, rev *v1.IstioRevision, opts Options) error {
	log := logf.FromContext(ctx)

	if err := reconcileServiceMonitor(ctx, c, rev, opts); err != nil {
		return fmt.Errorf("failed to reconcile ServiceMonitor: %w", err)
	}

	nsList := &corev1.NamespaceList{}
	if err := c.List(ctx, nsList, client.MatchingLabels{
		constants.IstioInjectionLabel: constants.IstioInjectionEnabledValue,
	}); err != nil {
		return fmt.Errorf("failed to list namespaces: %w", err)
	}

	for _, ns := range nsList.Items {
		if ns.Name == rev.Spec.Namespace {
			log.V(2).Info("Skipping PodMonitor for control plane namespace", "namespace", ns.Name)
			continue
		}

		if err := reconcilePodMonitorInNamespace(ctx, c, rev, ns.Name, opts); err != nil {
			return fmt.Errorf("failed to reconcile PodMonitor in namespace %s: %w", ns.Name, err)
		}
	}

	log.Info("Monitoring resources reconciled successfully")
	return nil
}

func reconcileServiceMonitor(ctx context.Context, c client.Client, rev *v1.IstioRevision, opts Options) error {
	log := logf.FromContext(ctx)
	desired := BuildServiceMonitor(rev, opts)

	existing := &monitoringv1.ServiceMonitor{}
	existing.SetGroupVersionKind(MonitoringGV.WithKind("ServiceMonitor"))

	err := c.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Creating ServiceMonitor", "name", desired.GetName(), "namespace", desired.GetNamespace())
			return c.Create(ctx, desired)
		}
		return fmt.Errorf("failed to get ServiceMonitor: %w", err)
	}

	log.V(2).Info("Updating ServiceMonitor", "name", desired.GetName(), "namespace", desired.GetNamespace())
	desired.SetResourceVersion(existing.GetResourceVersion())
	return c.Update(ctx, desired)
}

func reconcilePodMonitorInNamespace(ctx context.Context, c client.Client, rev *v1.IstioRevision, namespace string, opts Options) error {
	log := logf.FromContext(ctx)
	desired := BuildPodMonitor(rev, namespace, opts)

	existing := &monitoringv1.PodMonitor{}
	existing.SetGroupVersionKind(MonitoringGV.WithKind("PodMonitor"))

	err := c.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Creating PodMonitor", "name", desired.GetName(), "namespace", namespace)
			return c.Create(ctx, desired)
		}
		return fmt.Errorf("failed to get PodMonitor: %w", err)
	}

	log.V(2).Info("Updating PodMonitor", "name", desired.GetName(), "namespace", namespace)
	desired.SetResourceVersion(existing.GetResourceVersion())
	return c.Update(ctx, desired)
}
