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

package observability

import (
	"fmt"
	"time"

	integrationv1alpha1 "github.com/istio-ecosystem/sail-operator/api/integration/v1alpha1"
	v1 "github.com/istio-ecosystem/sail-operator/api/v1"
	"github.com/istio-ecosystem/sail-operator/pkg/env"
	"github.com/istio-ecosystem/sail-operator/pkg/istioversion"
	"github.com/istio-ecosystem/sail-operator/pkg/kube"
	"github.com/istio-ecosystem/sail-operator/pkg/monitoring"
	. "github.com/istio-ecosystem/sail-operator/pkg/test/util/ginkgo"
	"github.com/istio-ecosystem/sail-operator/tests/e2e/util/cleaner"
	"github.com/istio-ecosystem/sail-operator/tests/e2e/util/common"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	stackLabelKey   = "monitoredby"
	stackLabelValue = "coo-monitoring-stack"
)

var monitoringGV = monitoring.DefaultMonitoringGV

var _ = Describe("Observability", Label("slow"), func() {
	SetDefaultEventuallyTimeout(time.Duration(env.GetInt("DEFAULT_TEST_TIMEOUT", 180)) * time.Second)
	SetDefaultEventuallyPollingInterval(time.Second)

	versions := istioversion.GetLatestPatchVersions()
	if len(versions) == 0 {
		Fail("no Istio versions configured")
	}
	version := versions[0]

	// Monitoring and Integration are tested in separate Describes with independent
	// cluster setup and cleanup so the two controllers never reconcile the same monitors.
	Describe("Istio.spec.monitoring.enabled", Label("monitoring"), Ordered, func() {
		var revisionName string
		clr := cleaner.New(cl)

		BeforeAll(func(ctx SpecContext) {
			clr.Record(ctx)
			common.InstallMonitoringCRDs(k)
			Expect(k.CreateNamespace(controlPlaneNamespace)).To(Succeed())
			Expect(k.CreateNamespace(istioCniNamespace)).To(Succeed())
			Expect(k.CreateNamespace(sampleNamespace)).To(Succeed())

			common.CreateIstioCNI(k, version.Name)
			common.AwaitCniDaemonSet(ctx, k, cl)
		})

		When("monitoring is disabled on the Istio CR", func() {
			BeforeAll(func() {
				common.CreateIstio(k, version.Name)
			})

			It("waits for the Istio CR to become Ready", func(ctx SpecContext) {
				common.AwaitCondition(ctx, v1.IstioConditionReady, kube.Key(istioName), &v1.Istio{}, k, cl)
				revisionName = common.ActiveRevisionName(ctx, cl, istioName)
			})

			It("does not create ServiceMonitor resources", func(ctx SpecContext) {
				common.ExpectServiceMonitorAbsent(ctx, cl, revisionName, controlPlaneNamespace, monitoringGV)
			})

			It("does not create PodMonitor resources in workload namespaces", func(ctx SpecContext) {
				Expect(k.Label("namespace", sampleNamespace, "istio-injection", "enabled")).To(Succeed())
				common.ExpectPodMonitorAbsent(ctx, cl, revisionName, sampleNamespace, monitoringGV)
			})
		})

		When("monitoring is enabled on the Istio CR", func() {
			BeforeAll(func() {
				Expect(k.Patch("istio", istioName, "merge", `{"spec":{"monitoring":{"enabled":true}}}`)).
					To(Succeed(), "failed to enable monitoring on Istio CR")
				Success("monitoring enabled on Istio CR")
			})

			It("creates a ServiceMonitor in the control plane namespace", func(ctx SpecContext) {
				sm := common.AwaitServiceMonitor(ctx, cl, revisionName, controlPlaneNamespace, monitoringGV)
				Expect(sm.Spec.Endpoints).NotTo(BeEmpty())
				Expect(sm.Spec.Endpoints[0].Port).To(Equal("http-monitoring"))
			})

			It("uses monitoring.coreos.com as the default API group", func(ctx SpecContext) {
				sm := common.AwaitServiceMonitor(ctx, cl, revisionName, controlPlaneNamespace, monitoringGV)
				Expect(sm.GetObjectKind().GroupVersionKind().Group).To(Equal(monitoring.CoreOSAPIGroup))
			})

			It("applies the interim monitored-by label on ServiceMonitor", func(ctx SpecContext) {
				sm := common.AwaitServiceMonitor(ctx, cl, revisionName, controlPlaneNamespace, monitoringGV)
				Expect(sm.Labels[monitoring.DefaultMonitoredByLabel]).To(Equal(monitoring.DefaultMonitoredByValue))
			})
		})

		When("a workload namespace has sidecar injection enabled", func() {
			BeforeAll(func(ctx SpecContext) {
				Expect(k.WithNamespace(sampleNamespace).ApplyKustomize("helloworld", "version=v1")).To(Succeed())
				Eventually(common.CheckSamplePodsReady).WithArguments(ctx, cl).Should(Succeed())
				Success("sample workload deployed")
			})

			It("creates a PodMonitor in the workload namespace", func(ctx SpecContext) {
				pm := common.AwaitPodMonitor(ctx, cl, revisionName, sampleNamespace, monitoringGV)
				Expect(pm.Labels[monitoring.DefaultMonitoredByLabel]).To(Equal(monitoring.DefaultMonitoredByValue))
				Expect(pm.Spec.PodMetricsEndpoints).NotTo(BeEmpty())
			})

			It("does not create a PodMonitor in the control plane namespace", func(ctx SpecContext) {
				common.ExpectPodMonitorAbsent(ctx, cl, revisionName, controlPlaneNamespace, monitoringGV)
			})
		})

		AfterAll(func(ctx SpecContext) {
			if CurrentSpecReport().Failed() && keepOnFailure {
				return
			}
			clr.Cleanup(ctx)
		})
	})

	Describe("Integration CR metrics", Label("integration"), Ordered, func() {
		var revisionName string
		clr := cleaner.New(cl)

		BeforeAll(func(ctx SpecContext) {
			clr.Record(ctx)
			common.InstallMonitoringCRDs(k)
			Expect(k.CreateNamespace(controlPlaneNamespace)).To(Succeed())
			Expect(k.CreateNamespace(istioCniNamespace)).To(Succeed())
			Expect(k.CreateNamespace(sampleNamespace)).To(Succeed())
			Expect(k.CreateNamespace(common.MonitoringStackNamespace)).To(Succeed())

			common.CreateIstioCNI(k, version.Name)
			common.AwaitCniDaemonSet(ctx, k, cl)

			// Istio is created without spec.monitoring so only the Integration controller manages monitors.
			common.CreateIstio(k, version.Name)
			common.AwaitCondition(ctx, v1.IstioConditionReady, kube.Key(istioName), &v1.Istio{}, k, cl)
			revisionName = common.ActiveRevisionName(ctx, cl, istioName)

			Expect(k.Label("namespace", sampleNamespace, "istio-injection", "enabled")).To(Succeed())
			Expect(k.WithNamespace(sampleNamespace).ApplyKustomize("helloworld", "version=v1")).To(Succeed())
			Eventually(common.CheckSamplePodsReady).WithArguments(ctx, cl).Should(Succeed())
		})

		It("does not enable Istio.spec.monitoring", func(ctx SpecContext) {
			istio := &v1.Istio{}
			Expect(cl.Get(ctx, kube.Key(istioName), istio)).To(Succeed())
			Expect(istio.Spec.Monitoring).To(Or(BeNil(), HaveField("Enabled", BeFalse())))
			common.ExpectServiceMonitorAbsent(ctx, cl, revisionName, controlPlaneNamespace, monitoringGV)
			common.ExpectPodMonitorAbsent(ctx, cl, revisionName, sampleNamespace, monitoringGV)
		})

		When("an Integration CR references a missing MonitoringStack", func() {
			const missingIntegrationName = "e2e-integration-missing-stack"

			BeforeAll(func() {
				yaml := fmt.Sprintf(`
apiVersion: integration.ossm/v1alpha1
kind: Integration
metadata:
  name: %s
  namespace: %s
spec:
  istioRef:
    name: %s
  metrics:
    type: ClusterObservability
    clusterObservability:
      monitoringStackRef:
        name: missing-stack
        namespace: %s
`, missingIntegrationName, controlPlaneNamespace, istioName, common.MonitoringStackNamespace)
				Expect(k.ApplyString(yaml)).To(Succeed())
			})

			It("reports an invalid spec status", func(ctx SpecContext) {
				integration := &integrationv1alpha1.Integration{}
				Eventually(func(g Gomega) {
					g.Expect(cl.Get(ctx, kube.Key(missingIntegrationName, controlPlaneNamespace), integration)).To(Succeed())
					g.Expect(integration.Status.State).To(Equal(integrationv1alpha1.IntegrationReasonInvalidSpec))
				}).Should(Succeed())
				Success("Integration status reflects missing MonitoringStack")
			})

			It("does not create monitor resources", func(ctx SpecContext) {
				common.ExpectServiceMonitorAbsent(ctx, cl, revisionName, controlPlaneNamespace, monitoringGV)
				common.ExpectPodMonitorAbsent(ctx, cl, revisionName, sampleNamespace, monitoringGV)
			})
		})

		When("an Integration CR references a valid MonitoringStack", func() {
			BeforeAll(func() {
				stackYAML := fmt.Sprintf(`
apiVersion: monitoring.rhobs/v1alpha1
kind: MonitoringStack
metadata:
  name: %s
  namespace: %s
spec:
  resourceSelector:
    matchLabels:
      %s: %s
`, common.DefaultMonitoringStack, common.MonitoringStackNamespace, stackLabelKey, stackLabelValue)
				Expect(k.ApplyString(stackYAML)).To(Succeed())

				integrationYAML := fmt.Sprintf(`
apiVersion: integration.ossm/v1alpha1
kind: Integration
metadata:
  name: %s
  namespace: %s
spec:
  istioRef:
    name: %s
  metrics:
    type: ClusterObservability
    clusterObservability:
      monitoringStackRef:
        name: %s
        namespace: %s
`, common.DefaultIntegrationName, controlPlaneNamespace, istioName,
					common.DefaultMonitoringStack, common.MonitoringStackNamespace)
				Expect(k.ApplyString(integrationYAML)).To(Succeed())
				Success("MonitoringStack and Integration created")
			})

			It("reports a healthy Integration status", func(ctx SpecContext) {
				integration := &integrationv1alpha1.Integration{}
				Eventually(func(g Gomega) {
					g.Expect(cl.Get(ctx, kube.Key(common.DefaultIntegrationName, controlPlaneNamespace), integration)).To(Succeed())
					g.Expect(integration.Status.State).To(Equal(integrationv1alpha1.IntegrationReasonHealthy))
					g.Expect(integration.Status.IstioRevision).To(Equal(revisionName))
				}).Should(Succeed())
				Success("Integration reconciled successfully")
			})

			It("creates monitors with MonitoringStack selector labels", func(ctx SpecContext) {
				sm := common.AwaitServiceMonitor(ctx, cl, revisionName, controlPlaneNamespace, monitoringGV)
				Expect(sm.Labels[stackLabelKey]).To(Equal(stackLabelValue))
				Expect(sm.Labels).NotTo(HaveKeyWithValue(monitoring.DefaultMonitoredByLabel, monitoring.DefaultMonitoredByValue))

				pm := common.AwaitPodMonitor(ctx, cl, revisionName, sampleNamespace, monitoringGV)
				Expect(pm.Labels[stackLabelKey]).To(Equal(stackLabelValue))
				Expect(pm.Labels).NotTo(HaveKeyWithValue(monitoring.DefaultMonitoredByLabel, monitoring.DefaultMonitoredByValue))
			})

			It("sets the Integration CR as owner of the ServiceMonitor", func(ctx SpecContext) {
				sm := common.AwaitServiceMonitor(ctx, cl, revisionName, controlPlaneNamespace, monitoringGV)
				Expect(sm.OwnerReferences).NotTo(BeEmpty())
				owner := sm.OwnerReferences[0]
				Expect(owner.Kind).To(Equal(integrationv1alpha1.IntegrationKind))
				Expect(owner.Name).To(Equal(common.DefaultIntegrationName))
			})
		})

		AfterAll(func(ctx SpecContext) {
			if CurrentSpecReport().Failed() && keepOnFailure {
				return
			}
			clr.Cleanup(ctx)
		})
	})

	AfterAll(func() {
		if CurrentSpecReport().Failed() {
			common.LogDebugInfo(common.Observability, k)
		}
	})
})
