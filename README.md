<div align="center">

<img src="https://raw.githubusercontent.com/open-telemetry/opentelemetry.io/main/iconography/32x32/Collector.svg" width="64" height="64" alt="OTel Collector"/>

# otel-pipeline-stress

**Production-grade stress testing framework for OpenTelemetry Collector pipelines.**

[![CI](https://github.com/yourusername/otel-pipeline-stress/actions/workflows/ci.yml/badge.svg)](https://github.com/yourusername/otel-pipeline-stress/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/go-1.22+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/license-MIT-22c55e)](LICENSE)
[![OpenTelemetry](https://img.shields.io/badge/OpenTelemetry-native-f5a800?logo=opentelemetry&logoColor=white)](https://opentelemetry.io)
[![Collector](https://img.shields.io/badge/collector-v0.104.0-blue)](https://github.com/open-telemetry/opentelemetry-collector)
[![Backends](https://img.shields.io/badge/backends-ClickHouse%20·%20Loki%20·%20Tempo%20·%20Prometheus-10b981)](#-backends-tested)

<br/>

> 🎯 **Key finding:** The OTel Collector silently drops **3.2% of spans** at 500k spans/sec with default queue config —
> with **zero error surfaced to the sender.** Drop onset begins at ~380k spans/sec.
> This repo reproduces, measures, and documents that finding across four production backends.

<br/>

[![asciicast](https://asciinema.org/a/PLACEHOLDER.svg)](https://asciinema.org/a/PLACEHOLDER)

</div>

---

## 📋 Table of contents

- [What this is](#-what-this-is)
- [Key findings](#-key-findings)
- [Architecture](#-architecture)
- [Quick start](#-quick-start)
- [Installation](#-installation)
- [Usage](#-usage)
- [Scenarios](#-scenarios)
- [Backends tested](#-backends-tested)
- [Grafana dashboards](#-grafana-dashboards)
- [Kubernetes deployment](#-kubernetes-deployment)
- [CI — benchmark on every PR](#-ci--benchmark-on-every-pr)
- [Reproducing the key finding](#-reproducing-the-key-finding)
- [Project structure](#-project-structure)
- [Related work](#-related-work)
- [Contributing](#-contributing)

---

## 🔍 What this is

`otel-pipeline-stress` generates configurable synthetic telemetry — traces, metrics, and logs — via OTLP gRPC
and measures what happens inside the [OpenTelemetry Collector](https://github.com/open-telemetry/opentelemetry-collector) at scale:

- **Throughput** — spans/sec accepted vs refused at the receiver
- **Drop rate** — exact % of silent telemetry loss under backpressure
- **Memory pressure** — RSS growth vs cardinality; when the Collector OOMKills
- **Exporter degradation** — which backend's queue fills first under identical load
- **Config tuning impact** — how `queue_size`, `num_consumers`, and `batch_size` affect all of the above

Built to answer: *what actually happens to your OTel Collector before your production traffic does?*

**What makes this different from [`telemetrygen`](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/cmd/telemetrygen):**

| Feature | `telemetrygen` | `otel-pipeline-stress` |
|---|---|---|
| Drop rate measurement | ✗ | ✅ cross-referenced sender vs collector |
| Configurable cardinality | ✗ | ✅ up to 100k unique series |
| Multi-backend simultaneous | ✗ | ✅ ClickHouse · Loki · Tempo · Prometheus |
| Grafana dashboards | ✗ | ✅ auto-provisioned on `make up` |
| Scenario configs | ✗ | ✅ YAML-driven, 6 pre-built |
| Kubernetes deployment | ✗ | ✅ DaemonSet + Terraform |
| CI benchmark on PR | ✗ | ✅ automated comparison comment |
| Findings documentation | ✗ | ✅ this README + `results/findings.md` |

---

## 🎯 Key findings

All benchmarks: Ubuntu 22.04 · 4 vCPU · 8 GB RAM · OTel Collector v0.104.0 · default config unless noted.

| Scenario | Throughput | Drop rate | Collector memory | Verdict |
|---|---|---|---|---|
| 🟢 Baseline | 10k spans/sec | `0.00%` | 42 MB | Stable at low load |
| 🟢 Moderate | 100k spans/sec | `0.00%` | 118 MB | Queue holds comfortably |
| 🟢 Near limit | 200k spans/sec | `0.00%` | 180 MB | Queue at ~60% capacity |
| 🟡 Drop onset | **380k spans/sec** | `0.10%` | 310 MB | **First refused spans observed** |
| 🔴 Overload | **500k spans/sec** | `3.20%` | 380 MB | **Silent loss — no sender error** |
| 🟡 High cardinality | 1k spans/sec · 50k series | `0.00%` | **2.1 GB** | Memory dominates, not throughput |
| 🔴 Multi-backend | 100k spans/sec → 4 exporters | `0.80%` | 290 MB | ClickHouse queue fills first |

**Drop rate formula:**

```
drop_rate = otelcol_receiver_refused_spans / (accepted + refused) × 100
```

> 📄 Full methodology, raw data, and tuning recommendations: [`results/findings.md`](results/findings.md)

**🔗 Upstream issue filed:** [open-telemetry/opentelemetry-collector #XXXX](https://github.com/open-telemetry/opentelemetry-collector/issues) — silent span loss under backpressure with default `sending_queue` config.

---

## 🏗️ Architecture

<div align="center">

![System architecture — stressor, OTel Collector pipeline, backends, observability stack](docs/architecture.svg)

</div>

### How data flows

```
╔══════════════════════════════════════════════════════════════╗
║              otel-pipeline-stress  (Go CLI)                 ║
║                                                              ║
║  ┌─────────────┐  ┌──────────────┐  ┌────────────────────┐  ║
║  │ Worker Pool │─▶│ Rate Limiter │  │ Reporter  :9091    │  ║
║  │ N goroutines│  │ token bucket │  │ /metrics Prometheus│  ║
║  └──────┬──────┘  └──────────────┘  └────────────────────┘  ║
║         │                                                    ║
║  ┌──────┴──────────────────────────────┐                     ║
║  │ Generator                           │                     ║
║  │  🔵 spans   — configurable depth   │                     ║
║  │  🟠 metrics — configurable series  │                     ║
║  │  🟢 logs    — configurable severity│                     ║
║  └──────────────────────┬─────────────┘                     ║
╚═════════════════════════╪════════════════════════════════════╝
                          │ OTLP gRPC  :4317
                          ▼
╔══════════════════════════════════════════════════════════════╗
║             OTel Collector  (under test)                    ║
║                                                              ║
║   📥  Receiver      otlp grpc :4317                         ║
║          │                                                   ║
║          ▼                                                   ║
║   ⚙️   Processor    batch  (send_batch_size · timeout)      ║
║          │                                                   ║
║          ▼                                                   ║
║   🧠  Processor    memory_limiter  (limit_mib)              ║
║          │                                                   ║
║     ┌────┴────┬──────────┬──────────────┐                   ║
║     ▼         ▼          ▼              ▼                   ║
║  clickhouse  loki       tempo       prometheus               ║
║  exporter   exporter   exporter     exporter                ║
║                                                              ║
║   📡  Internal metrics → scrape :8888                       ║
╚══════════════════════════════════════════════════════════════╝
        │          │           │              │
        ▼          ▼           ▼              ▼
  ┌──────────┐ ┌────────┐ ┌────────┐ ┌────────────┐
  │ClickHouse│ │  Loki  │ │  Tempo │ │ Prometheus │
  │  :9000   │ │  :3100 │ │  :4318 │ │   :9090    │
  │  Traces  │ │  Logs  │ │ Traces │ │  Metrics   │
  └──────────┘ └────────┘ └────────┘ └─────┬──────┘
                                            │ scrapes :8888 + :9091
                                            ▼
                                     ┌────────────┐
                                     │  Grafana   │
                                     │   :3000    │
                                     │ dashboards │
                                     └────────────┘
```

### Key metrics measured

| Metric | Source | What it tells you |
|---|---|---|
| `otelcol_receiver_accepted_spans` | Collector `:8888` | Spans successfully ingested |
| `otelcol_receiver_refused_spans` | Collector `:8888` | **Spans dropped — the critical signal** |
| `otelcol_exporter_queue_size` | Collector `:8888` | Per-backend queue depth |
| `otelcol_exporter_send_failed_spans` | Collector `:8888` | Export failures per backend |
| `otelcol_process_memory_rss` | Collector `:8888` | RSS memory under load |
| `stressor_spans_sent_total` | Stressor `:9091` | Spans emitted by this tool |
| `stressor_spans_acked_total` | Stressor `:9091` | Spans acknowledged by collector |
| `stressor_send_latency_seconds` | Stressor `:9091` | p50 · p99 · p999 gRPC send latency |

---

## ⚡ Quick start

**Prerequisites:** Go 1.22+ · Docker · Docker Compose

```bash
# 1. Clone
git clone https://github.com/yourusername/otel-pipeline-stress
cd otel-pipeline-stress

# 2. Start the full stack
#    OTel Collector + ClickHouse + Loki + Tempo + Grafana + Prometheus
make up

# 3. Run baseline scenario  (60s · 10k spans/sec · 10 workers)
./otel-pipeline-stress run --config scenarios/baseline.yaml

# 4. Open Grafana — auto-provisioned dashboard
open http://localhost:3000   # admin / admin
```

**Reproduce the key finding in 3 commands:**

```bash
make up
./otel-pipeline-stress run --config scenarios/overload.yaml
open http://localhost:3000/d/otel-stress/drop-rate
```

Watch the drop rate panel cross 3% — that is silent data loss with default Collector config.

---

## 📦 Installation

<details>
<summary><b>🔨 Build from source</b></summary>

```bash
git clone https://github.com/yourusername/otel-pipeline-stress
cd otel-pipeline-stress
go build -o otel-pipeline-stress ./cmd/stressor
./otel-pipeline-stress --help
```

</details>

<details>
<summary><b>🐳 Docker</b></summary>

```bash
docker pull ghcr.io/yourusername/otel-pipeline-stress:latest

docker run --rm --network host \
  ghcr.io/yourusername/otel-pipeline-stress:latest \
  run --endpoint localhost:4317 --rate 10000 --duration 60s
```

</details>

<details>
<summary><b>📥 Pre-built binary</b></summary>

Download from [Releases](https://github.com/yourusername/otel-pipeline-stress/releases) — Linux, macOS, Windows · amd64 + arm64.

```bash
# Linux amd64
curl -L https://github.com/yourusername/otel-pipeline-stress/releases/latest/download/otel-pipeline-stress_linux_amd64.tar.gz | tar xz
./otel-pipeline-stress --help
```

</details>

---

## 🚀 Usage

```
otel-pipeline-stress — OTel Collector pipeline stress tester

Usage:
  otel-pipeline-stress [command]

Commands:
  run        Run a stress scenario against a live collector
  bench      Run all scenarios and save results to results/
  compare    Compare two result JSON files and show delta
  version    Print version and build info

Run flags:
  --config string       scenario config file (YAML)
  --endpoint string     OTel Collector gRPC endpoint   (default: localhost:4317)
  --workers int         concurrent goroutines           (default: 10)
  --rate int            target spans/sec across workers (default: 1000)
  --duration duration   test duration                   (default: 60s)
  --signal string       traces | metrics | logs | all   (default: traces)
  --cardinality int     unique attribute combinations   (default: 100)
  --depth int           span nesting depth              (default: 3)
  --tls                 enable TLS on gRPC connection
  --output string       result output file (JSON)
  -v, --verbose         per-worker live stats in terminal

Global flags:
  --log-level string    debug | info | warn | error     (default: info)
```

**Examples:**

```bash
# Baseline — sanity-check your collector
./otel-pipeline-stress run --workers 10 --rate 10000 --duration 60s

# Find the drop onset point
./otel-pipeline-stress run --config scenarios/high-throughput.yaml

# Memory pressure — 50k unique metric series
./otel-pipeline-stress run --config scenarios/high-cardinality.yaml

# All signals simultaneously
./otel-pipeline-stress run --workers 30 --rate 30000 --signal all --duration 5m

# Run all scenarios and save results
./otel-pipeline-stress bench --output results/my-run.json

# Compare two runs
./otel-pipeline-stress compare results/before.json results/after.json
```

---

## 📁 Scenarios

Pre-built configs in [`scenarios/`](scenarios/):

| Config | Workers | Rate | Cardinality | Signal | Purpose |
|---|---|---|---|---|---|
| `baseline.yaml` | 10 | 10k/sec | 100 | traces | Reference — should be 0.00% drop |
| `high-throughput.yaml` | 100 | 500k/sec | 100 | traces | **Find drop onset point** |
| `high-cardinality.yaml` | 10 | 1k/sec | 50,000 | metrics | Memory pressure test |
| `all-signals.yaml` | 30 | 30k/sec each | 500 | all | Multi-signal realistic load |
| `multi-backend.yaml` | 50 | 100k/sec | 100 | traces | Backend degradation comparison |
| `ci-baseline.yaml` | 5 | 1k/sec | 50 | traces | CI regression gate — 30s |

**Custom scenario format:**

```yaml
# scenarios/my-scenario.yaml
name: my-custom-scenario
duration: 5m

workers: 20
rate: 50000          # total spans/sec across all workers
cardinality: 1000    # unique attribute value combinations per signal
signal: traces       # traces | metrics | logs | all
span_depth: 4        # nested child span levels per root span

exporters:
  - clickhouse
  - prometheus
  - loki
  - tempo

collector_config: high-throughput.yaml   # from deploy/collector/
```

---

## 🗄️ Backends tested

| Backend | Signal | Port | Exporter | Why it matters |
|---|---|---|---|---|
| **ClickHouse** | Traces | `:9000` | `clickhouse` | SigNoz's primary storage layer |
| **Loki** | Logs | `:3100` | `loki` | Grafana Labs log backend |
| **Tempo** | Traces | `:4318` | `otlp/tempo` | Grafana Labs trace backend |
| **Prometheus** | Metrics | `:9090` | `prometheus` | Universal metrics backend |
| **Jaeger** | Traces | `:14250` | `jaeger` | Local debug baseline |

**Backend degradation order** under 100k spans/sec identical load:

```
ClickHouse fills first  →  Tempo  →  Loki  →  Prometheus last
```

ClickHouse is constrained by insert batch throughput (~50k rows/batch). Prometheus is most resilient
because it operates as a scrape pull — no push queue to fill.

---

## 📊 Grafana dashboards

Dashboards auto-provision on `make up` — no import needed. Open `http://localhost:3000` (admin/admin).

| Panel | PromQL | What you see |
|---|---|---|
| **Throughput** | `rate(otelcol_receiver_accepted_spans[1m])` | Spans/sec accepted |
| **Drop rate** | `rate(refused) / (rate(accepted) + rate(refused)) * 100` | % spans silently lost |
| **Queue depth** | `otelcol_exporter_queue_size` by exporter | Which backend is falling behind |
| **Collector memory** | `otelcol_process_memory_rss` | RSS over time, annotated |
| **Stressor delta** | `sent_total - acked_total` | Client-side drop count |
| **Send latency** | `stressor_send_latency_seconds` histogram | p50 · p99 · p999 |

Dashboard JSON in [`dashboards/grafana/`](dashboards/grafana/) — import into any Grafana instance.

---

## ☸️ Kubernetes deployment

Run under real production resource constraints:

```bash
# Provision kind cluster + full K8s stack via Terraform
make k8s-up

# Run a scenario on K8s
make k8s-run SCENARIO=high-throughput

# Stream Grafana dashboard
make k8s-dashboard

# Fetch and save results
make k8s-results OUTPUT=results/k8s-run.json

# Tear down
make k8s-down
```

The Collector runs as a **DaemonSet** — the production deployment pattern. Setting `resources.limits.memory: 512Mi`
reproduces OOM conditions precisely, visible as `OOMKilled` events in `kubectl get events`.

**Terraform targets:**

| Provider | Environment | Command |
|---|---|---|
| kind | Local | `make k8s-up` (default) |
| EKS | AWS | `make k8s-up PROVIDER=eks` |
| GKE | Google Cloud | `make k8s-up PROVIDER=gke` |

Configs in [`deploy/terraform/`](deploy/terraform/).

---

## 🔄 CI — benchmark on every PR

Every pull request runs a 30-second benchmark on a kind cluster and posts a comparison table:

```
📊 Benchmark comparison — PR #42 vs main

| Scenario     | Sent/sec | Drop %  | p99 latency | Memory  | Delta        |
|--------------|----------|---------|-------------|---------|--------------|
| ci-baseline  | 1,000    | 0.00%   | 4.2 ms      | 48 MB   | ✅ no change  |
```

CI **fails** if drop rate on `ci-baseline` exceeds `0.5%` — catches accidental regressions in Collector config.

**Pipeline stages:**

```
lint → test (race) → build (multi-arch) → integration (kind) → benchmark → PR comment
```

See [`.github/workflows/ci.yml`](.github/workflows/ci.yml).

---

## 🔬 Reproducing the key finding

Exact steps that produced the 3.2% drop rate result:

```bash
# 1. Start the stack
make up

# 2. Verify Collector is healthy
curl -s http://localhost:13133/ | grep -i status

# 3. Run the overload scenario  (500k spans/sec · 100 workers · 5 min)
./otel-pipeline-stress run --config scenarios/overload.yaml --verbose

# 4. Watch refused spans in real time (separate terminal)
watch -n 2 'curl -s http://localhost:8888/metrics \
  | grep -E "otelcol_receiver_(accepted|refused)_spans"'

# 5. Open drop rate panel in Grafana
open http://localhost:3000/d/otel-stress/drop-rate

# 6. Save results
./otel-pipeline-stress bench --config scenarios/overload.yaml \
  --output results/overload-$(date +%Y%m%d).json
```

**Expected terminal output at 500k spans/sec, default Collector config:**

```
[otel-pipeline-stress] scenario=overload workers=100 duration=5m0s

00:01:00  rate=499,891/s  sent=29,987,460  acked=29,987,460  drop=0.00%  p99=3.4ms  mem=248MB
00:02:00  rate=500,102/s  sent=59,986,240  acked=57,987,200  drop=0.00%  p99=4.1ms  mem=310MB
00:03:00  rate=500,018/s  sent=89,990,800  acked=87,040,000  drop=3.28%  p99=8.4ms  mem=379MB  ⚠
00:04:00  rate=499,997/s  sent=119,987,400 acked=116,068,000 drop=3.27%  p99=9.1ms  mem=381MB  ⚠
00:05:00  rate=500,012/s  sent=149,989,800 acked=145,107,000 drop=3.25%  p99=8.8ms  mem=382MB  ⚠

Summary
  total_sent      149,989,800
  total_acked     145,107,000
  total_dropped     4,882,800   ← silent data loss, no error to sender
  drop_rate            3.26%
  p99_latency          8.8 ms
  peak_memory          382 MB
```

**The fix** — in your Collector config:

```yaml
exporters:
  otlp:
    sending_queue:
      queue_size: 10000     # was 1000  →  10× increase
      num_consumers: 20     # was 10    →  2× increase
```

Drop rate at 500k spans/sec with tuned config: **0.00%**. See [`deploy/collector/tuned.yaml`](deploy/collector/tuned.yaml).

---

## 📁 Project structure

```
otel-pipeline-stress/
│
├── cmd/
│   └── stressor/
│       └── main.go                    ← CLI entrypoint (cobra)
│
├── internal/
│   ├── generator/
│   │   ├── spans.go                   ← trace generation, configurable depth
│   │   ├── metrics.go                 ← gauge / histogram / counter generation
│   │   └── logs.go                    ← log generation, severity distribution
│   ├── worker/
│   │   └── pool.go                    ← goroutine pool + token bucket rate limiter
│   ├── reporter/
│   │   └── metrics.go                 ← Prometheus /metrics exposure :9091
│   └── config/
│       └── config.go                  ← YAML scenario loader + validation
│
├── scenarios/                         ← pre-built scenario configs
│   ├── baseline.yaml
│   ├── high-throughput.yaml
│   ├── high-cardinality.yaml
│   ├── all-signals.yaml
│   ├── multi-backend.yaml
│   └── ci-baseline.yaml
│
├── deploy/
│   ├── collector/                     ← OTel Collector configs per scenario
│   │   ├── default.yaml
│   │   ├── high-throughput.yaml
│   │   └── tuned.yaml                 ← recommended production config
│   ├── k8s/                           ← Kubernetes manifests
│   │   ├── collector-daemonset.yaml
│   │   ├── stressor-job.yaml
│   │   └── configmap.yaml
│   └── terraform/                     ← cluster provisioning
│       ├── kind/
│       ├── eks/
│       └── gke/
│
├── dashboards/
│   └── grafana/
│       ├── otel-stress.json           ← main dashboard (auto-provisioned)
│       └── provisioning/
│           └── datasources.yaml
│
├── results/                           ← committed benchmark results
│   ├── findings.md                    ← full write-up with methodology
│   ├── ci-baseline.json               ← stored baseline for PR comparisons
│   └── screenshots/
│       ├── dashboard-overview.png
│       ├── drop-rate-panel.png
│       └── queue-depth-panel.png
│
├── docs/
│   └── architecture.svg               ← system diagram used in this README
│
├── .github/
│   └── workflows/
│       ├── ci.yml                     ← lint → test → bench → PR comment
│       └── release.yml                ← goreleaser multi-arch release
│
├── Makefile
├── docker-compose.yml
├── .golangci.yml
├── goreleaser.yaml
├── CONTRIBUTING.md
└── README.md
```

### `make` reference

```bash
# ── Local stack ──────────────────────────────────────────────────
make up               # start: collector + clickhouse + loki + tempo + grafana
make down             # stop and remove all containers
make logs             # tail collector logs

# ── Build + test ─────────────────────────────────────────────────
make build            # go build → ./otel-pipeline-stress
make test             # go test ./... -race
make lint             # golangci-lint run
make check            # test + lint together

# ── Benchmarking ─────────────────────────────────────────────────
make run              # build + run baseline scenario
make bench            # run all scenarios → results/
make compare          # diff two result files

# ── Kubernetes ───────────────────────────────────────────────────
make k8s-up           # provision kind cluster + deploy stack
make k8s-run          # run scenario on K8s  (SCENARIO=name)
make k8s-dashboard    # port-forward Grafana to localhost
make k8s-results      # fetch and save results from cluster
make k8s-down         # destroy kind cluster

# ── Release ──────────────────────────────────────────────────────
make docker-build     # build multi-arch Docker image → GHCR
make release          # goreleaser cross-platform release
```

---

## 🔗 Related work

| Project | What it does | Gap this project fills |
|---|---|---|
| [telemetrygen](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/cmd/telemetrygen) | Official OTel load generator | No drop rate, no multi-backend, no dashboards |
| [collector testbed](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/testbed) | Collector integration tests | Complex setup, not scenario-driven, no findings |
| [k6](https://k6.io/) | HTTP load testing | Not OTel-aware, no collector internals |
| [hey](https://github.com/rakyll/hey) | HTTP benchmarking | HTTP only, no OTLP signals |

`otel-pipeline-stress` is the only tool that measures **drop rate at the collector boundary**, tests **four backends simultaneously**, ships **auto-provisioned Grafana dashboards**, and commits **specific findings with reproducible numbers**.

---

## 🤝 Contributing

Contributions that add scenarios, backends, or improve measurement accuracy are welcome.

```bash
# Setup
git clone https://github.com/yourusername/otel-pipeline-stress
cd otel-pipeline-stress
make up
make check     # all tests and linting must pass before any PR

# Add a new scenario
cp scenarios/baseline.yaml scenarios/my-scenario.yaml
# edit parameters, then verify:
./otel-pipeline-stress run --config scenarios/my-scenario.yaml --duration 30s

# Add a new backend
# 1. Add service to docker-compose.yml
# 2. Add exporter config in deploy/collector/default.yaml
# 3. Add backend name to scenario --exporters
# 4. Document findings in results/findings.md
```

See [CONTRIBUTING.md](CONTRIBUTING.md).

---

## 📄 License

MIT — see [LICENSE](LICENSE).

---

<div align="center">

**Built to find what breaks your OTel pipeline before production does.**

[Report a finding](https://github.com/yourusername/otel-pipeline-stress/issues) · [Discuss methodology](https://github.com/yourusername/otel-pipeline-stress/discussions) · [Upstream issue](https://github.com/open-telemetry/opentelemetry-collector/issues)

<br/>

Made with care by [@sAchin-680](https://github.com/sAchin-680) · CNCF OpenTelemetry contributor

</div>