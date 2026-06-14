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

	integrationv1alpha1 "github.com/istio-ecosystem/sail-operator/api/integration/v1alpha1"
	"github.com/istio-ecosystem/sail-operator/pkg/reconciler"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const monitoringStackAPIGroup = "monitoring.rhobs"

var MonitoringStackGVK = schema.GroupVersionKind{
	Group:   monitoringStackAPIGroup,
	Version: "v1alpha1",
	Kind:    "MonitoringStack",
}

var monitoringStackGVK = MonitoringStackGVK

// LabelsFromMonitoringStack returns the matchLabels from a MonitoringStack's resourceSelector.
func LabelsFromMonitoringStack(ctx context.Context, c client.Client, ref integrationv1alpha1.NamespacedObjectReference) (map[string]string, error) {
	stack := &unstructured.Unstructured{}
	stack.SetGroupVersionKind(monitoringStackGVK)

	key := client.ObjectKey{Name: ref.Name, Namespace: ref.Namespace}
	if err := c.Get(ctx, key, stack); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, reconciler.NewValidationError(fmt.Sprintf("MonitoringStack %s/%s not found", ref.Namespace, ref.Name))
		}
		return nil, fmt.Errorf("failed to get MonitoringStack: %w", err)
	}

	matchLabels, found, err := unstructured.NestedStringMap(stack.Object, "spec", "resourceSelector", "matchLabels")
	if err != nil {
		return nil, fmt.Errorf("failed to read MonitoringStack resourceSelector.matchLabels: %w", err)
	}
	if !found || len(matchLabels) == 0 {
		return nil, reconciler.NewValidationError(
			fmt.Sprintf("MonitoringStack %s/%s has no spec.resourceSelector.matchLabels", ref.Namespace, ref.Name),
		)
	}

	labels := make(map[string]string, len(matchLabels))
	for key, value := range matchLabels {
		labels[key] = value
	}
	return labels, nil
}
