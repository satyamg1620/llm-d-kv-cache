# Monitoring the KV-Cache Core Library

An operational Grafana dashboard for the `llm-d-kv-cache` **core library**, covering
index throughput and efficiency, lookup and tokenization latency, and kvevents
subscriber health.

This is separate from the [`llmd_fs_backend` connector dashboard](../../../kv_connectors/llmd_fs_backend/docs/monitoring.md),
which monitors KV-offload transfer metrics (`vllm:kv_offload_*`) emitted by vLLM. The
core library dashboard is driven by the `kvcache_*` Prometheus metrics that the library
registers and exposes on its `/metrics` endpoint.

## Metrics

The dashboard expects the metrics registered in
[`pkg/kvcache/metrics/collector.go`](../../../pkg/kvcache/metrics/collector.go) and the
kvevents subscriber/pool metrics:

### Index (`kvcache_index_*`)

| Metric | Type | Description |
|---|---|---|
| `kvcache_index_admissions_total` | counter | KV-block admissions |
| `kvcache_index_evictions_total` | counter | KV-block evictions |
| `kvcache_index_lookup_requests_total` | counter | `Lookup()` calls |
| `kvcache_index_lookup_hits_total` | counter | Keys found on `Lookup()` |
| `kvcache_index_max_pod_hit_count_total` | counter | Max hits on a single pod per `Lookup()` |
| `kvcache_index_lookup_latency_seconds` | histogram | `Lookup()` latency |

### Tokenization (`kvcache_tokenization_*`, label `tokenizer`)

| Metric | Type | Description |
|---|---|---|
| `kvcache_tokenization_tokenization_latency_seconds` | histogram | Tokenization latency |
| `kvcache_tokenization_render_chat_template_latency_seconds` | histogram | Chat-template render latency |
| `kvcache_tokenization_tokenized_tokens_total` | counter | Tokens tokenized |

### KV-Events Subscriber Health (`kvcache_kvevents_*`)

These are added by the kvevents subscriber/pool health metrics work
([#641](https://github.com/llm-d/llm-d-kv-cache/issues/641)). The corresponding panels
show "No data" until that change is deployed.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `kvcache_kvevents_active_subscribers` | gauge | | Connected ZMQ subscribers |
| `kvcache_kvevents_messages_received_total` | counter | `pod_identifier` | KV-event messages received |
| `kvcache_kvevents_subscriber_reconnections_total` | counter | `pod_identifier` | Subscriber reconnections |
| `kvcache_kvevents_zmq_errors_total` | counter | `pod_identifier`, `operation` | ZMQ errors |
| `kvcache_kvevents_pool_queue_depth` | gauge | | Event-processing pool queue depth |
| `kvcache_kvevents_pool_capacity` | gauge | | Event-processing pool capacity |

> **Backend health** (subtask of [#617](https://github.com/llm-d/llm-d-kv-cache/issues/617),
> "Backend Connectivity Metrics") is not yet implemented in the core library. Backend
> health panels will be added to this dashboard once those metrics land.

## Deploy

The library exposes Prometheus metrics on its HTTP `/metrics` endpoint (port `8080` in
the reference [`examples/kv_events/online`](../../../examples/kv_events/online) server).
Make sure your deployment exposes that endpoint through a Service, then:

```bash
# Prometheus — scrapes the kv-cache /metrics endpoint every 15s.
# Edit prometheus.yaml first so the scrape target matches your kv-cache Service.
kubectl apply -f docs/deployment/monitoring/prometheus.yaml

# Grafana dashboard ConfigMap (must be applied before Grafana)
kubectl apply -f docs/deployment/monitoring/grafana-dashboard-configmap.yaml

# Grafana — pre-configured with the Prometheus datasource and the dashboard
kubectl apply -f docs/deployment/monitoring/grafana.yaml
```

If you run the Prometheus Operator, apply the ServiceMonitor instead of editing
`prometheus.yaml`:

```bash
kubectl apply -f docs/deployment/monitoring/prometheus-servicemonitor.yaml
```

## View

```bash
# Terminal 1: Grafana UI on http://localhost:3000
kubectl port-forward svc/grafana-svc 3000:3000

# Terminal 2: Prometheus UI on http://localhost:9090 (optional, ad-hoc queries)
kubectl port-forward svc/prometheus-svc 9090:9090
```

Open the dashboard directly at:

**http://localhost:3000/d/kvcache-core/llm-d-kv-cache-core-library**

Anonymous admin access is enabled, so no login is required.

## Importing into an existing Grafana

The dashboard JSON is embedded in `grafana-dashboard-configmap.yaml` under
`data["kvcache-core.json"]`. To import it into an existing Grafana, copy that JSON object
and use **Dashboards → New → Import**, then select your Prometheus datasource for the
`DS_PROMETHEUS` input.
