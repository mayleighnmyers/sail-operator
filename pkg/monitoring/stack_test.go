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
	"testing"

	integrationv1alpha1 "github.com/istio-ecosystem/sail-operator/api/integration/v1alpha1"
	"github.com/istio-ecosystem/sail-operator/pkg/reconciler"
	"github.com/istio-ecosystem/sail-operator/pkg/scheme"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestLabelsFromMonitoringStack(t *testing.T) {
	tests := []struct {
		name        string
		stack       *unstructured.Unstructured
		ref         integrationv1alpha1.NamespacedObjectReference
		expectErr   bool
		expectValid bool
		expected    map[string]string
	}{
		{
			name: "returns matchLabels from resourceSelector",
			stack: newMonitoringStack("monitoring", "my-stack", map[string]interface{}{
				"resourceSelector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"monitoredby": "coo-monitoring-stack",
					},
				},
			}),
			ref: integrationv1alpha1.NamespacedObjectReference{
				Name:      "my-stack",
				Namespace: "monitoring",
			},
			expected: map[string]string{
				"monitoredby": "coo-monitoring-stack",
			},
		},
		{
			name: "missing stack",
			ref: integrationv1alpha1.NamespacedObjectReference{
				Name:      "missing",
				Namespace: "monitoring",
			},
			expectErr:   true,
			expectValid: true,
		},
		{
			name: "missing matchLabels",
			stack: newMonitoringStack("monitoring", "empty-stack", map[string]interface{}{
				"resourceSelector": map[string]interface{}{},
			}),
			ref: integrationv1alpha1.NamespacedObjectReference{
				Name:      "empty-stack",
				Namespace: "monitoring",
			},
			expectErr:   true,
			expectValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			builder := fake.NewClientBuilder().WithScheme(scheme.Scheme)
			if tt.stack != nil {
				builder = builder.WithObjects(tt.stack)
			}
			cl := builder.Build()

			labels, err := LabelsFromMonitoringStack(context.Background(), cl, tt.ref)
			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
				if tt.expectValid {
					g.Expect(reconciler.IsValidationError(err)).To(BeTrue())
				}
				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(labels).To(Equal(tt.expected))
		})
	}
}

func newMonitoringStack(namespace, name string, spec map[string]interface{}) *unstructured.Unstructured {
	stack := &unstructured.Unstructured{}
	stack.SetGroupVersionKind(monitoringStackGVK)
	stack.SetNamespace(namespace)
	stack.SetName(name)
	_ = unstructured.SetNestedMap(stack.Object, spec, "spec")
	return stack
}
