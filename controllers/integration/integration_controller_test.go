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
	"testing"

	integrationv1alpha1 "github.com/istio-ecosystem/sail-operator/api/integration/v1alpha1"
	v1 "github.com/istio-ecosystem/sail-operator/api/v1"
	"github.com/istio-ecosystem/sail-operator/pkg/config"
	"github.com/istio-ecosystem/sail-operator/pkg/constants"
	monitoringpkg "github.com/istio-ecosystem/sail-operator/pkg/monitoring"
	"github.com/istio-ecosystem/sail-operator/pkg/scheme"
	. "github.com/onsi/gomega"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	integrationNamespace = "istio-system"
	integrationName      = "my-integration"
	istioName            = "default"
	revisionName         = "default"
	revisionNamespace    = "istio-system"
	appNamespace         = "bookinfo"
	stackNamespace       = "monitoring"
	stackName            = "my-stack"
)

func TestIntegrationReconcileCreatesMonitorsWithStackLabels(t *testing.T) {
	g := NewWithT(t)

	istio := &v1.Istio{
		ObjectMeta: metav1.ObjectMeta{Name: istioName},
		Status: v1.IstioStatus{
			ActiveRevisionName: revisionName,
		},
	}
	rev := &v1.IstioRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name: revisionName,
			UID:  types.UID("revision-uid"),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: v1.GroupVersion.String(),
					Kind:       v1.IstioKind,
					Name:       istioName,
				},
			},
		},
		Spec: v1.IstioRevisionSpec{
			Namespace: revisionNamespace,
		},
	}
	integration := newIntegration()
	stack := newMonitoringStack(stackNamespace, stackName, map[string]interface{}{
		"resourceSelector": map[string]interface{}{
			"matchLabels": map[string]interface{}{
				"monitoredby": "coo-monitoring-stack",
			},
		},
	})
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: appNamespace,
			Labels: map[string]string{
				constants.IstioInjectionLabel: constants.IstioInjectionEnabledValue,
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(istio, rev, integration, stack, ns).
		WithStatusSubresource(integration).
		Build()

	reconciler := NewReconciler(config.ReconcilerConfig{Platform: config.PlatformOpenShift}, cl, scheme.Scheme)
	_, err := reconciler.Reconcile(context.Background(), integration)
	g.Expect(err).NotTo(HaveOccurred())

	sm := &monitoringv1.ServiceMonitor{}
	sm.SetGroupVersionKind(monitoringpkg.MonitoringGV.WithKind("ServiceMonitor"))
	g.Expect(cl.Get(context.Background(), types.NamespacedName{
		Name:      revisionName + monitoringpkg.ServiceMonitorNameSuffix,
		Namespace: revisionNamespace,
	}, sm)).To(Succeed())
	g.Expect(sm.GetLabels()["monitoredby"]).To(Equal("coo-monitoring-stack"))

	pm := &monitoringv1.PodMonitor{}
	pm.SetGroupVersionKind(monitoringpkg.MonitoringGV.WithKind("PodMonitor"))
	g.Expect(cl.Get(context.Background(), types.NamespacedName{
		Name:      revisionName + monitoringpkg.PodMonitorNameSuffix,
		Namespace: appNamespace,
	}, pm)).To(Succeed())
	g.Expect(pm.GetLabels()["monitoredby"]).To(Equal("coo-monitoring-stack"))

	updated := &integrationv1alpha1.Integration{}
	g.Expect(cl.Get(context.Background(), types.NamespacedName{
		Name:      integrationName,
		Namespace: integrationNamespace,
	}, updated)).To(Succeed())
	g.Expect(updated.Status.State).To(Equal(integrationv1alpha1.IntegrationReasonHealthy))
	g.Expect(updated.Status.IstioRevision).To(Equal(revisionName))
}

func newIntegration() *integrationv1alpha1.Integration {
	return &integrationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      integrationName,
			Namespace: integrationNamespace,
			UID:       types.UID("integration-uid"),
		},
		Spec: integrationv1alpha1.IntegrationSpec{
			IstioRef: integrationv1alpha1.IstioReference{Name: istioName},
			Metrics: &integrationv1alpha1.MetricsIntegration{
				Type: integrationv1alpha1.MetricsIntegrationTypeClusterObservability,
				ClusterObservability: integrationv1alpha1.ClusterObservabilityMetrics{
					MonitoringStackRef: integrationv1alpha1.NamespacedObjectReference{
						Name:      stackName,
						Namespace: stackNamespace,
					},
				},
			},
		},
	}
}

func newMonitoringStack(namespace, name string, spec map[string]interface{}) *unstructured.Unstructured {
	stack := &unstructured.Unstructured{}
	stack.SetGroupVersionKind(monitoringpkg.MonitoringStackGVK)
	stack.SetNamespace(namespace)
	stack.SetName(name)
	_ = unstructured.SetNestedMap(stack.Object, spec, "spec")
	return stack
}
