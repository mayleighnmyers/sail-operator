//go:build e2e

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

package common

import (
	"context"
	"fmt"
	"path/filepath"

	v1 "github.com/istio-ecosystem/sail-operator/api/v1"
	"github.com/istio-ecosystem/sail-operator/pkg/kube"
	"github.com/istio-ecosystem/sail-operator/pkg/monitoring"
	"github.com/istio-ecosystem/sail-operator/pkg/test/project"
	. "github.com/istio-ecosystem/sail-operator/pkg/test/util/ginkgo"
	"github.com/istio-ecosystem/sail-operator/tests/e2e/util/kubectl"
	. "github.com/onsi/gomega"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	MonitoringStackNamespace = "monitoring"
	DefaultIntegrationName   = "e2e-integration"
	DefaultMonitoringStack   = "e2e-stack"
)

// MonitoringTestdataDir returns the path to KinD monitoring test fixtures.
func MonitoringTestdataDir() string {
	return filepath.Join(project.RootDir, "tests", "e2e", "setup", "testdata", "monitoring")
}

// InstallMonitoringCRDs installs minimal Prometheus Operator and MonitoringStack CRDs for e2e tests.
func InstallMonitoringCRDs(k kubectl.Kubectl) {
	dir := MonitoringTestdataDir()
	Expect(k.Apply(filepath.Join(dir, "coreos-crds.yaml"))).To(Succeed(), "failed to install monitoring.coreos.com CRDs")
	Expect(k.Apply(filepath.Join(dir, "monitoringstack-crd.yaml"))).To(Succeed(), "failed to install MonitoringStack CRD")
	Success("monitoring CRDs installed")
}

// CreateIstioWithMonitoring creates an Istio CR with spec.monitoring.enabled set to true.
func CreateIstioWithMonitoring(k kubectl.Kubectl, version string) {
	CreateIstio(k, version, "monitoring:\n  enabled: true")
}

// ActiveRevisionName returns the active IstioRevision name from a Ready Istio CR.
func ActiveRevisionName(ctx context.Context, cl client.Client, istioName string) string {
	istio := &v1.Istio{}
	Expect(cl.Get(ctx, kube.Key(istioName), istio)).To(Succeed())
	Expect(istio.Status.ActiveRevisionName).NotTo(BeEmpty(), "Istio active revision not set")
	return istio.Status.ActiveRevisionName
}

// AwaitServiceMonitor waits until a ServiceMonitor exists for the given revision.
func AwaitServiceMonitor(ctx context.Context, cl client.Client, revisionName, namespace string, gv schema.GroupVersion) *monitoringv1.ServiceMonitor {
	sm := &monitoringv1.ServiceMonitor{}
	key := kube.Key(revisionName+monitoring.ServiceMonitorNameSuffix, namespace)
	sm.SetGroupVersionKind(gv.WithKind("ServiceMonitor"))

	Eventually(func(g Gomega) {
		g.Expect(cl.Get(ctx, key, sm)).To(Succeed())
	}).Should(Succeed(), fmt.Sprintf("ServiceMonitor %q not found in namespace %q", key.Name, namespace))
	Success(fmt.Sprintf("ServiceMonitor %q exists in namespace %q", key.Name, namespace))
	return sm
}

// AwaitPodMonitor waits until a PodMonitor exists for the given revision in the namespace.
func AwaitPodMonitor(ctx context.Context, cl client.Client, revisionName, namespace string, gv schema.GroupVersion) *monitoringv1.PodMonitor {
	pm := &monitoringv1.PodMonitor{}
	key := kube.Key(revisionName+monitoring.PodMonitorNameSuffix, namespace)
	pm.SetGroupVersionKind(gv.WithKind("PodMonitor"))

	Eventually(func(g Gomega) {
		g.Expect(cl.Get(ctx, key, pm)).To(Succeed())
	}).Should(Succeed(), fmt.Sprintf("PodMonitor %q not found in namespace %q", key.Name, namespace))
	Success(fmt.Sprintf("PodMonitor %q exists in namespace %q", key.Name, namespace))
	return pm
}

// ExpectServiceMonitorAbsent asserts that no ServiceMonitor exists for the revision.
func ExpectServiceMonitorAbsent(ctx context.Context, cl client.Client, revisionName, namespace string, gv schema.GroupVersion) {
	sm := &monitoringv1.ServiceMonitor{}
	key := kube.Key(revisionName+monitoring.ServiceMonitorNameSuffix, namespace)
	sm.SetGroupVersionKind(gv.WithKind("ServiceMonitor"))

	Consistently(func(g Gomega) {
		err := cl.Get(ctx, key, sm)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "unexpected ServiceMonitor %q in namespace %q", key.Name, namespace)
	}).Should(Succeed())
	Success(fmt.Sprintf("ServiceMonitor %q is absent in namespace %q", key.Name, namespace))
}

// ExpectPodMonitorAbsent asserts that no PodMonitor exists for the revision in the namespace.
func ExpectPodMonitorAbsent(ctx context.Context, cl client.Client, revisionName, namespace string, gv schema.GroupVersion) {
	pm := &monitoringv1.PodMonitor{}
	key := kube.Key(revisionName+monitoring.PodMonitorNameSuffix, namespace)
	pm.SetGroupVersionKind(gv.WithKind("PodMonitor"))

	Consistently(func(g Gomega) {
		err := cl.Get(ctx, key, pm)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "unexpected PodMonitor %q in namespace %q", key.Name, namespace)
	}).Should(Succeed())
	Success(fmt.Sprintf("PodMonitor %q is absent in namespace %q", key.Name, namespace))
}
