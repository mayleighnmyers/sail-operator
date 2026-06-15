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
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// CoreOSAPIGroup is the Prometheus Operator API group used by OpenShift user-workload monitoring
	// and upstream Prometheus Operator installs. This is the default when both groups are available.
	CoreOSAPIGroup = "monitoring.coreos.com"

	// RhobsAPIGroup is the API group used by Cluster Observability Operator (COO).
	RhobsAPIGroup = "monitoring.rhobs"

	MonitoringAPIVersion = "v1"
)

// DefaultMonitoringGV is used when no Prometheus Operator monitoring API group is detected.
var DefaultMonitoringGV = schema.GroupVersion{Group: CoreOSAPIGroup, Version: MonitoringAPIVersion}

// GroupVersionForAPIGroup returns the GroupVersion for ServiceMonitor/PodMonitor resources.
func GroupVersionForAPIGroup(group string) schema.GroupVersion {
	return schema.GroupVersion{Group: group, Version: MonitoringAPIVersion}
}

// DetectMonitoringAPIGroup selects the monitoring API group installed in the cluster.
// monitoring.coreos.com is preferred when both groups are available. If only monitoring.rhobs
// is available (for example COO-only clusters), that group is used instead.
func DetectMonitoringAPIGroup(mapper RESTMapper) schema.GroupVersion {
	coreosGV := GroupVersionForAPIGroup(CoreOSAPIGroup)
	rhobsGV := GroupVersionForAPIGroup(RhobsAPIGroup)

	coreosAvailable := hasServiceMonitorResource(mapper, coreosGV)
	rhobsAvailable := hasServiceMonitorResource(mapper, rhobsGV)

	switch {
	case coreosAvailable:
		return coreosGV
	case rhobsAvailable:
		return rhobsGV
	default:
		return DefaultMonitoringGV
	}
}

// MonitoringGVForCOO returns the monitoring API group for Cluster Observability integration.
// COO MonitoringStack resources require monitoring.rhobs.
func MonitoringGVForCOO(mapper RESTMapper) schema.GroupVersion {
	rhobsGV := GroupVersionForAPIGroup(RhobsAPIGroup)
	if hasServiceMonitorResource(mapper, rhobsGV) {
		return rhobsGV
	}
	return DefaultMonitoringGV
}

// RESTMapper supports discovery of installed monitoring API groups.
type RESTMapper interface {
	RESTMapping(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error)
}

func hasServiceMonitorResource(mapper RESTMapper, gv schema.GroupVersion) bool {
	if mapper == nil {
		return false
	}

	_, err := mapper.RESTMapping(gv.WithKind("ServiceMonitor").GroupKind(), gv.Version)
	return err == nil
}

func resolveMonitoringGV(mapper RESTMapper, configured schema.GroupVersion, forCOO bool) schema.GroupVersion {
	if configured != (schema.GroupVersion{}) {
		return configured
	}
	if forCOO {
		return MonitoringGVForCOO(mapper)
	}
	return DetectMonitoringAPIGroup(mapper)
}
