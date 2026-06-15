|Status                                             | Authors      | Created    | 
|---------------------------------------------------|--------------|------------|
|Implementation                                     | @mayleighnmyers, @yxun         | 2026-06-01 |

# Observability Integration (Metrics)

## Overview

Upstream Istio generates telemetry metrics for the control plane and sidecar proxies. Users can customize Istio metrics with the Telemetry API, but that does not cover scraping those metrics with Prometheus Operator. We want to automate creation of `ServiceMonitor` and `PodMonitor` resources so users can query Istio metrics in Prometheus or Kiali without manual configuration.

## Goals

* Provide a monitoring controller that reconciles `ServiceMonitor` and `PodMonitor` resources for Istio control plane and sidecar proxy metrics
* Apply platform-appropriate default relabeling rules on Kubernetes and OpenShift

## Non-goals

* Deploying observability stack components (Prometheus, COO, OpenShift user-workload monitoring, etc.). We assume those are installed separately.
* User customization of scrape paths, ports, or relabeling rules via the Sail API
* Tracing integration (tracked separately under [OSSM-14058](https://redhat.atlassian.net/browse/OSSM-14058))

## Design

### User Stories

1. As a user running Istio with a Prometheus Operator-based stack, I want the operator to configure metrics scraping jobs for Istio-generated telemetry metrics.
2. As a user running OpenShift Service Mesh, I want `PodMonitor` resources with OSSM-documented relabeling rules so metrics appear correctly in the OpenShift console and Kiali.

### API Changes

We will add an optional `spec.monitoring` field to the existing `Istio` CR (cluster-scoped). The `IstioRevision` CR is not modified; the monitoring controller reads the parent `Istio` CR via owner references on each revision.

#### Istio resource

Here's an example YAML enabling metrics integration:

```yaml
apiVersion: sailoperator.io/v1
kind: Istio
metadata:
  name: default
spec:
  version: v1.29.2
  namespace: istio-system
  monitoring:
    enabled: true
```

When `spec.monitoring.enabled` is `true`, the monitoring controller reconciles Prometheus Operator monitor CRs for each `IstioRevision` owned by this `Istio` resource. When `false` or when `spec.monitoring` is omitted, the controller does not create or update monitor CRs for those revisions.

#### MonitoringConfig

`MonitoringConfig` is an optional object under `Istio.spec.monitoring`.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `bool` | `false` | When `true`, the operator creates and updates `ServiceMonitor` and `PodMonitor` resources for Istio metrics scraping. |

`enabled` is the only field in v1alpha1. Scrape paths, ports, relabeling rules, and monitor selector labels are not configurable through this API; they are platform defaults applied by the monitoring controller (see [Architecture](#architecture)).

##### Enabling monitoring

Setting `spec.monitoring.enabled: true` on an `Istio` CR causes the monitoring controller to reconcile, for each owned `IstioRevision`:

* One `ServiceMonitor` named `{revision}-istiod` in the revision's control plane namespace (`IstioRevision.spec.namespace`).
* One or more `PodMonitor` resources named `{revision}-proxies` in namespaces where sidecar injection is enabled (see [Kubernetes vs OpenShift](#kubernetes-vs-openshift) below).

Reconciliation runs when:

* An owned `IstioRevision` is created or updated.
* The parent `Istio` CR's `spec.monitoring` field changes.
* A namespace gains or loses the `istio-injection=enabled` label (PodMonitor placement).

The monitoring controller does not reconcile monitor CRs while an `IstioRevision` is deleting. The `ServiceMonitor` is removed automatically via owner reference to the `IstioRevision`. `PodMonitor` resources in application namespaces are not owner-referenced (cross-namespace owner references are invalid) and are not deleted automatically in v1alpha1 when monitoring is disabled.

##### Disabling monitoring

Setting `spec.monitoring.enabled: false` or removing `spec.monitoring` stops further reconciliation. Existing monitor CRs created while monitoring was enabled are not deleted automatically in v1alpha1.

#### Managed monitor resources

When monitoring is enabled, the operator creates external CRs with the following conventions. These are not Sail CRDs; they target the Prometheus Operator API (COO uses `monitoring.rhobs/v1`).

##### ServiceMonitor (`{revision}-istiod`)

| Property | Value |
|----------|-------|
| API group | `monitoring.rhobs/v1` (v1alpha1; see [Architecture](#architecture)) |
| Namespace | `IstioRevision.spec.namespace` |
| Owner reference | `IstioRevision` (controller) |
| Labels | `app: istiod`, `monitored-by: coo-prometheus` |
| Spec | Selector `istio: pilot`, `targetLabels: [app]`, endpoint `http-monitoring` / `/metrics` / `30s` |

The `monitored-by: coo-prometheus` label allows Cluster Observability Operator (COO) Prometheus to select this resource via its `ServiceMonitor` selector.

##### PodMonitor (`{revision}-proxies`)

| Property | Value |
|----------|-------|
| API group | `monitoring.rhobs/v1` (v1alpha1) |
| Namespace | Each namespace with `istio-injection=enabled` (current implementation; see platform notes below) |
| Owner reference | None (cross-namespace) |
| Labels | `app: istio-proxy`, `monitored-by: coo-prometheus` |
| Spec | Selector `istio-prometheus-ignore` DoesNotExist, path `/stats/prometheus`, interval `30s`, platform-default `relabelings` |

On **OpenShift**, `PodMonitor` relabelings include a `mesh_id` label set to the parent `Istio` CR name. On **Kubernetes**, relabelings follow upstream Istio defaults (see [Architecture](#architecture)).

##### Prerequisites

Monitoring integration assumes `ServiceMonitor` and `PodMonitor` CRDs are already installed (for example by COO or the Prometheus Operator). The operator does not install them. If the CRDs are unavailable, create/update operations fail and errors are logged by the monitoring controller. Validation and status reporting for missing CRDs are planned (see [Implementation Plan](#implementation-plan)).

#### Istio status (v1alpha1)

v1alpha1 does not add monitoring-specific fields or conditions to `Istio.status`. The existing `Istio` conditions (`Reconciled`, `Ready`, `DependenciesHealthy`) are unchanged and do not reflect monitor CR reconciliation.

Monitor reconciliation failures (for example missing CRDs or API permission errors) are logged by the monitoring controller. Surfacing monitoring health on `Istio.status` is planned as part of CRD validation work in the implementation plan.

#### Changes to existing APIs

* **`Istio` CRD** — new optional `spec.monitoring` field (`MonitoringConfig`). No changes to `IstioRevision`, `IstioRevisionTag`, or other Sail CRDs in v1alpha1.
* **Operator RBAC** — ClusterRole permissions for `servicemonitors` and `podmonitors` under `monitoring.coreos.com` and `monitoring.rhobs`.
* **Operator scheme** — prometheus-operator types registered under `monitoring.rhobs/v1` for typed client operations.

#### Future API

A standalone `Integration` CR with `istioRef` and references to external metrics and tracing stacks (similar to `targetRef` on `ZTunnel` and `IstioRevisionTag`) is preferred long term for opt-in RBAC and stack-specific selector labels. That design is tracked in [OSSM-14058](https://redhat.atlassian.net/browse/OSSM-14058) and may supersede `Istio.spec.monitoring.enabled` in a future release.

### Architecture

We assume `ServiceMonitor` and `PodMonitor` CRDs are available under `monitoring.coreos.com/v1` and/or `monitoring.rhobs/v1`. The Sail Operator ClusterRole grants permissions for both API groups.

The monitoring controller watches `IstioRevision` resources and reconciles monitor CRs when monitoring is enabled on the parent `Istio` CR. It also watches `Istio` (for changes to `spec.monitoring.enabled`) and namespaces with the `istio-injection=enabled` label (for PodMonitor placement on OpenShift).

Monitor CRs are built using prometheus-operator Go API types and applied via the controller-runtime client. Platform-specific relabeling defaults are selected from `pkg/monitoring/relabeling` based on `ReconcilerConfig.Platform` (detected at startup via `config.DetectPlatform()`).

#### ServiceMonitor (istiod)

One `ServiceMonitor` per `IstioRevision` in the control plane namespace, named `{revision}-istiod`, with an owner reference to the `IstioRevision`. The spec follows upstream Istio and OSSM samples: selector `istio: pilot`, `targetLabels: [app]`, endpoint port `http-monitoring`, path `/metrics`, interval `30s`. No endpoint relabelings.

#### PodMonitor (istio-proxy)

One or more `PodMonitor` resources per `IstioRevision`, named `{revision}-proxies`. The spec uses selector `istio-prometheus-ignore` DoesNotExist, path `/stats/prometheus`, interval `30s`, and annotation-based scrape relabelings (no explicit port field).

Relabeling rules follow [upstream Istio prometheus-operator.yaml](https://github.com/istio/istio/blob/master/samples/addons/extras/prometheus-operator.yaml) on Kubernetes and the [OSSM 3.0 metrics documentation](https://docs.redhat.com/en/documentation/red_hat_openshift_service_mesh/3.0/html/observability/metrics-and-service-mesh) on OpenShift (additional `app`, `version`, and `mesh_id` labels; `mesh_id` is set to the parent `Istio` CR name).

#### Kubernetes vs OpenShift

On **Kubernetes**, a single `PodMonitor` in the control plane namespace with `namespaceSelector.any: true` is the preferred approach (matching upstream Istio). On **OpenShift**, user-workload monitoring ignores `namespaceSelector`, so a `PodMonitor` must be created in each mesh namespace with sidecar injection enabled.

The controller currently creates a `PodMonitor` per namespace with `istio-injection=enabled`. Platform-specific API group selection (`monitoring.coreos.com` vs `monitoring.rhobs`) is not yet implemented; created objects use `monitoring.rhobs/v1`.

### Performance Impact

`ServiceMonitor` reconciliation is limited to the control plane namespace and has low impact. `PodMonitor` reconciliation on OpenShift requires listing namespaces and may impact performance on clusters with many namespaces. Using `namespaceSelector` on Kubernetes avoids this per-namespace loop.

## Alternatives Considered

### Embedded YAML templates in Go

Rejected. Monitor CR specs should be built entirely from typed Go structs via the controller-runtime client, not from YAML fragments embedded in controller code.

### Named port scraping (`http-envoy-prom`)

Rejected. Upstream Istio and OSSM samples use annotation-based address relabeling without an explicit port field.

### Single PodMonitor strategy for all platforms

Rejected on OpenShift. User-workload monitoring ignores `namespaceSelector` on monitor CRs, requiring per-namespace PodMonitors per OSSM documentation.

### Standalone Integration CR with target references

A standalone `Integration` CR referencing `Istio`, metrics stacks, and tracing stacks (similar to `targetRef` on `ZTunnel` and `IstioRevisionTag`) is preferred long term for opt-in RBAC and stack-specific configuration. That design is tracked in [OSSM-14058](https://redhat.atlassian.net/browse/OSSM-14058) rather than in this SEP.

## Implementation Plan

v1alpha1
- [x] Monitoring controller reconciling `ServiceMonitor` and `PodMonitor` for `IstioRevision`
- [x] `Istio.spec.monitoring.enabled` API
- [x] Platform-specific PodMonitor relabeling defaults
- [x] Unit tests for controller and relabeling package
- [ ] External CRD/API group detection (`monitoring.coreos.com` vs `monitoring.rhobs`)
- [ ] Kubernetes PodMonitor strategy using `namespaceSelector.any: true`
- [ ] Validation when monitoring CRDs are unavailable
- [ ] Integration tests (`tests/integration/api/monitoring_test.go`)
- [ ] KinD e2e tests
- [ ] User-facing documentation

v1alpha2
- [ ] [OSSM-14058](https://redhat.atlassian.net/browse/OSSM-14058) — Integration API with target references for metrics and tracing

## Test Plan

Functionality will be tested in unit tests, integration tests (envtest), and KinD e2e tests.

## Updates

* 2026-06-11: Updated with platform relabeling implementation, remaining v1alpha1 work items, and OSSM-14058 reference
* 2026-06-11: Expanded API Changes section with MonitoringConfig fields, reconciliation behavior, and managed resource conventions
