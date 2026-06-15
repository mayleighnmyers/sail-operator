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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const IntegrationKind = "Integration"

// IntegrationSpec defines the desired state of Integration.
type IntegrationSpec struct {
	// IstioRef references the Istio control plane to integrate with.
	//
	// +kubebuilder:validation:Required
	IstioRef IstioReference `json:"istioRef"`

	// Metrics configures integration with a metrics backend.
	//
	// +optional
	Metrics *MetricsIntegration `json:"metrics,omitempty"`

	// Tracing configures integration with a tracing backend.
	// Not reconciled in v1alpha1; reserved for future work.
	//
	// +optional
	Tracing *TracingIntegration `json:"tracing,omitempty"`
}

// IstioReference references a cluster-scoped Istio resource by name.
type IstioReference struct {
	// Name is the name of the Istio resource.
	//
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// NamespacedObjectReference references a namespaced Kubernetes object.
type NamespacedObjectReference struct {
	// Name is the name of the referenced object.
	//
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of the referenced object.
	//
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
}

// MetricsIntegrationType identifies the metrics integration backend.
type MetricsIntegrationType string

const (
	MetricsIntegrationTypeClusterObservability MetricsIntegrationType = "ClusterObservability"
)

// MetricsIntegration configures metrics collection integration.
type MetricsIntegration struct {
	// Type identifies the metrics integration backend.
	//
	// +kubebuilder:validation:Enum=ClusterObservability
	// +kubebuilder:validation:Required
	Type MetricsIntegrationType `json:"type"`

	// ClusterObservability configures integration with Cluster Observability Operator (COO).
	//
	// +kubebuilder:validation:Required
	ClusterObservability ClusterObservabilityMetrics `json:"clusterObservability"`
}

// ClusterObservabilityMetrics configures COO metrics integration.
type ClusterObservabilityMetrics struct {
	// MonitoringStackRef references the MonitoringStack that should scrape Istio metrics.
	//
	// +kubebuilder:validation:Required
	MonitoringStackRef NamespacedObjectReference `json:"monitoringStackRef"`
}

// TracingIntegrationType identifies the tracing integration backend.
type TracingIntegrationType string

const (
	TracingIntegrationTypeTempoStack TracingIntegrationType = "TempoStack"
)

// TracingIntegration configures tracing integration.
type TracingIntegration struct {
	// Type identifies the tracing integration backend.
	//
	// +kubebuilder:validation:Enum=TempoStack
	// +optional
	Type TracingIntegrationType `json:"type,omitempty"`

	// TempoStackRef references the TempoStack used for tracing.
	//
	// +optional
	TempoStackRef *NamespacedObjectReference `json:"tempoStackRef,omitempty"`
}

// IntegrationConditionType is the type of a status condition on Integration.
type IntegrationConditionType string

const (
	IntegrationConditionReconciled IntegrationConditionType = "Reconciled"
)

// IntegrationConditionReason is a reason for an Integration status condition.
type IntegrationConditionReason string

const (
	IntegrationReasonHealthy         IntegrationConditionReason = "Healthy"
	IntegrationReasonReconcileError  IntegrationConditionReason = "ReconcileError"
	IntegrationReasonReferenceNotFound IntegrationConditionReason = "RefNotFound"
	IntegrationReasonInvalidSpec     IntegrationConditionReason = "InvalidSpec"
)

// IntegrationStatus defines the observed state of Integration.
type IntegrationStatus struct {
	// ObservedGeneration is the most recent generation observed for this Integration object.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations of the Integration's state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// State reports the high-level reconciliation state.
	State IntegrationConditionReason `json:"state,omitempty"`

	// IstioRevision is the active IstioRevision reconciled for this integration.
	IstioRevision string `json:"istioRevision,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=int,categories=ossm
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.state",description="The current state of this object."
// +kubebuilder:printcolumn:name="Revision",type="string",JSONPath=".status.istioRevision",description="The IstioRevision reconciled for this integration."
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="The age of the object."

// Integration configures observability integrations for an Istio control plane.
type Integration struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata"`

	// +optional
	Spec IntegrationSpec `json:"spec"`

	// +optional
	Status IntegrationStatus `json:"status"`
}

// +kubebuilder:object:root=true

// IntegrationList contains a list of Integration resources.
type IntegrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Integration `json:"items"`
}
