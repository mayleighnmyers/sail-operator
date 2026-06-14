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
	v1 "github.com/istio-ecosystem/sail-operator/api/v1"
	"github.com/istio-ecosystem/sail-operator/pkg/monitoring/relabeling"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BuildServiceMonitor constructs the ServiceMonitor for monitoring istiod.
func BuildServiceMonitor(rev *v1.IstioRevision, opts Options) *monitoringv1.ServiceMonitor {
	name := rev.Name + ServiceMonitorNameSuffix
	namespace := rev.Spec.Namespace
	relabelCfg := relabeling.ForPlatform(opts.Platform, opts.IstioName)

	labels := map[string]string{
		"app": "istiod",
	}
	for key, value := range opts.MonitorLabels {
		labels[key] = value
	}

	ownerReferences := serviceMonitorOwnerReferences(rev, opts)

	sm := &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerReferences,
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

	sm.SetGroupVersionKind(MonitoringGV.WithKind("ServiceMonitor"))
	return sm
}

// BuildPodMonitor constructs the PodMonitor for monitoring istio-proxy sidecars.
func BuildPodMonitor(rev *v1.IstioRevision, namespace string, opts Options) *monitoringv1.PodMonitor {
	name := rev.Name + PodMonitorNameSuffix
	relabelCfg := relabeling.ForPlatform(opts.Platform, opts.IstioName)

	labels := map[string]string{
		"app": "istio-proxy",
	}
	for key, value := range opts.MonitorLabels {
		labels[key] = value
	}

	pm := &monitoringv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
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

	pm.SetGroupVersionKind(MonitoringGV.WithKind("PodMonitor"))
	return pm
}

func serviceMonitorOwnerReferences(rev *v1.IstioRevision, opts Options) []metav1.OwnerReference {
	if opts.Owner != nil {
		return []metav1.OwnerReference{*opts.Owner}
	}

	return []metav1.OwnerReference{
		{
			APIVersion:         v1.GroupVersion.String(),
			Kind:               v1.IstioRevisionKind,
			Name:               rev.Name,
			UID:                rev.UID,
			Controller:         ptr(true),
			BlockOwnerDeletion: ptr(true),
		},
	}
}

func ptr[T any](v T) *T {
	return &v
}
