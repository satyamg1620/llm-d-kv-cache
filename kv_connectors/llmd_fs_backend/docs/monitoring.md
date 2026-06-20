# Monitoring vLLM with KV Offload Metrics

End-to-end guide: deploy vLLM + Prometheus + Grafana in K8s, port-forward to view dashboards locally, and validate with a benchmark.

## 1. Deploy vLLM with the FS Backend

```bash
# Create HF token secret and PVCs
export HF_TOKEN=<your-token>
kubectl create secret generic hf-token --from-literal=HF_TOKEN="$HF_TOKEN"
kubectl apply -f docs/deployment/vllm-pvc.yaml

# Deploy vLLM
kubectl apply -f docs/deployment/vllm-storage.yaml
```

Wait for the pod to become ready (model download + startup takes a few minutes):

```bash
kubectl wait --for=condition=ready pod -l app=vllm-storage --timeout=600s
```

## 2. Deploy Prometheus and Grafana

```bash
# Prometheus — scrapes vLLM metrics every 15s
kubectl apply -f docs/deployment/monitoring/prometheus.yaml

# Grafana dashboard ConfigMap (must be applied before Grafana)
kubectl apply -f docs/deployment/monitoring/grafana-dashboard-configmap.yaml

# Grafana — pre-configured with Prometheus datasource and the dashboard
kubectl apply -f docs/deployment/monitoring/grafana.yaml
```

## 3. Port-Forward to Your Machine

Open two terminals:

```bash
# Terminal 1: Grafana UI on http://localhost:3000
kubectl port-forward svc/grafana-svc 3000:3000
```

```bash
# Terminal 2: Prometheus UI on http://localhost:9090 (optional, for ad-hoc queries)
kubectl port-forward svc/prometheus-svc 9090:9090
```

Open the dashboard directly at:

**http://localhost:3000/d/vllm-kv-offload/vllm-kv-offload-dashboard**

Anonymous access is enabled so no login is needed.

## 4. Run a Benchmark

Port-forward the vLLM service and run the benchmark:

```bash
# Terminal 3: vLLM API on http://localhost:8000
kubectl port-forward svc/vllm-storage-svc 8000:8000
```

### Benchmark with Prefix Repetition (KV Cache Offload)

Run two benchmark iterations to test KV cache offload (write) and retrieval (read):

**Run 1: KV Cache Write/Offload Test**
```bash
vllm bench serve \
  --backend vllm \
  --base-url http://localhost:8000 \
  --model Qwen/Qwen3-32B \
  --dataset-name prefix_repetition \
  --prefix-repetition-prefix-len 16384 \
  --prefix-repetition-suffix-len 0 \
  --prefix-repetition-num-prefixes 100 \
  --prefix-repetition-output-len 5 \
  --num-prompts 100 \
  --max-concurrency 40 \
  --request-rate 40 \
  --burstiness 1 \
  --ignore-eos \
  --seed 42
```

**Run 2: KV Cache Read/Retrieval Test**
```bash
vllm bench serve \
  --backend vllm \
  --base-url http://localhost:8000 \
  --model Qwen/Qwen3-32B \
  --dataset-name prefix_repetition \
  --prefix-repetition-prefix-len 16384 \
  --prefix-repetition-suffix-len 0 \
  --prefix-repetition-num-prefixes 100 \
  --prefix-repetition-output-len 5 \
  --num-prompts 100 \
  --max-concurrency 40 \
  --request-rate 40 \
  --burstiness 1 \
  --ignore-eos \
  --seed 42
```

Watch the Grafana dashboard — you should see KV offload metrics (throughput, transfer rates, bytes offloaded) once the cache starts spilling to storage during the benchmark.

## 5. Verify Metrics Manually

```bash
curl -s http://localhost:8000/metrics | grep kv_offload
```

## 6. Alerting Rules

Default Prometheus alerting rules for the KV-cache metrics live alongside the
monitoring manifests:

| File | Use |
|------|-----|
| `monitoring/kvcache-alerts.rules.yml` | Canonical plain rules file (source of truth). |
| `monitoring/prometheus-rules.yaml` | `PrometheusRule` CRD for prometheus-operator setups. |
| `monitoring/kvcache-alerts.test.yaml` | `promtool` unit tests for the rules. |

The bundled `monitoring/prometheus.yaml` already wires the rules in via
`rule_files`, so applying it (step 2) loads the alerts automatically. Confirm
they loaded:

```bash
# Prometheus UI -> Status -> Rules, or:
curl -s http://localhost:9090/api/v1/rules | grep -o '"name":"[A-Za-z]*"'
```

The alerts cover:

- **KVCacheLowIndexHitRatio** — index hit ratio below 30% while lookups are
  happening (stale index / broken KV-events stream).
- **KVCacheHighLookupLatency** — P99 `kvcache_index_lookup_latency_seconds`
  above 500ms.
- **KVCacheAbnormalEvictionSpike** — eviction rate jumps >5x its 1h baseline
  (cache thrashing or repeated `AllBlocksCleared` resets).
- **KVCacheHighTokenizationLatency** — P99 tokenization latency above 1s.
- **VLLMHighRequestBacklog** / **VLLMHighTTFT** — optional vLLM-side saturation
  alerts.

> The `kvcache_*` series are exported by the kv-cache library — embedded in the
> EPP (precise-prefix-cache-scorer with `enableMetrics: true`) or as a
> standalone service — **not** by vLLM. Make sure Prometheus scrapes that
> `/metrics` endpoint. A commented-out `kvcache` scrape job is included in
> `prometheus.yaml` as a starting point. Thresholds are conservative defaults;
> tune them to your workload before paging on them.

### Using the Prometheus Operator instead

If you run the prometheus-operator (e.g. kube-prometheus-stack), skip the
`rule_files` wiring and apply the CRD instead. Adjust the `release` label in
`prometheus-rules.yaml` to match your operator's `ruleSelector`:

```bash
kubectl apply -f docs/deployment/monitoring/prometheus-rules.yaml
```

### Validating the rules

```bash
cd docs/deployment/monitoring
# Syntax check + unit tests (uses the promtool from the prometheus image):
docker run --rm --entrypoint=/bin/promtool -v "$PWD":/work -w /work \
  prom/prometheus:v2.53.0 check rules kvcache-alerts.rules.yml
docker run --rm --entrypoint=/bin/promtool -v "$PWD":/work -w /work \
  prom/prometheus:v2.53.0 test rules kvcache-alerts.test.yaml
```

## 7. Cleanup

Remove all monitoring and vLLM resources:

```bash
# Monitoring stack
kubectl delete -f docs/deployment/monitoring/grafana.yaml
kubectl delete -f docs/deployment/monitoring/grafana-dashboard-configmap.yaml
kubectl delete -f docs/deployment/monitoring/prometheus.yaml

# vLLM
kubectl delete -f docs/deployment/vllm-storage.yaml
kubectl delete -f docs/deployment/vllm-pvc.yaml
kubectl delete secret hf-token
```
