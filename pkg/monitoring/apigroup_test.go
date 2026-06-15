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
	"testing"

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type stubRESTMapper struct {
	available map[schema.GroupVersion]bool
}

func (s stubRESTMapper) RESTMapping(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
	for _, version := range versions {
		if s.available[schema.GroupVersion{Group: gk.Group, Version: version}] {
			return &meta.RESTMapping{}, nil
		}
	}
	return nil, &meta.NoKindMatchError{GroupKind: gk}
}

func TestDetectMonitoringAPIGroup(t *testing.T) {
	coreosGV := GroupVersionForAPIGroup(CoreOSAPIGroup)
	rhobsGV := GroupVersionForAPIGroup(RhobsAPIGroup)

	tests := []struct {
		name     string
		mapper   stubRESTMapper
		expected schema.GroupVersion
	}{
		{
			name:     "defaults to coreos when neither group is registered",
			mapper:   stubRESTMapper{available: map[schema.GroupVersion]bool{}},
			expected: DefaultMonitoringGV,
		},
		{
			name: "prefers coreos when both groups are registered",
			mapper: stubRESTMapper{available: map[schema.GroupVersion]bool{
				coreosGV: true,
				rhobsGV:  true,
			}},
			expected: coreosGV,
		},
		{
			name: "uses rhobs when only rhobs is registered",
			mapper: stubRESTMapper{available: map[schema.GroupVersion]bool{
				rhobsGV: true,
			}},
			expected: rhobsGV,
		},
		{
			name: "uses coreos when only coreos is registered",
			mapper: stubRESTMapper{available: map[schema.GroupVersion]bool{
				coreosGV: true,
			}},
			expected: coreosGV,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(DetectMonitoringAPIGroup(tt.mapper)).To(Equal(tt.expected))
		})
	}
}

func TestMonitoringGVForCOO(t *testing.T) {
	g := NewWithT(t)

	rhobsGV := GroupVersionForAPIGroup(RhobsAPIGroup)
	g.Expect(MonitoringGVForCOO(stubRESTMapper{available: map[schema.GroupVersion]bool{
		rhobsGV: true,
	}})).To(Equal(rhobsGV))

	g.Expect(MonitoringGVForCOO(stubRESTMapper{available: map[schema.GroupVersion]bool{}})).To(Equal(DefaultMonitoringGV))
}
