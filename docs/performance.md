# Performance

Performance results in this document are measurements from named hardware and
software combinations, not guarantees for every OpenWrt or Home Assistant
installation. The scripts under `scripts/performance/` are the source of truth
for reproducing the methodology.

## July 2026 measurement

The first measured baseline used commit
`c5ad2ac87d1bf9c67e14f02adf040f2d1839847f` with:

- GL.iNet Flint 3 (`GL-BE9300`, IPQ5332, four ARM64 cores);
- GL.iNet firmware 4.9.0, OpenWrt 23.05-SNAPSHOT, kernel 5.4.213;
- Go 1.26.0 cross-compiled benchmark binary;
- Home Assistant 2026.7.3 on `qemux86-64`; and
- Home Assistant integration commit
  `9f682a30440db7676689ec25304435a2d2bad405`.

### Engine benchmarks on the router

Each case is the median of ten Go benchmark runs. The minimum and maximum show
the spread between runs.

| Case | Median | Minimum–maximum | Bytes/op | Allocations/op |
|---|---:|---:|---:|---:|
| Full reconciliation, 50 clients | 0.237 ms | 0.214–0.296 ms | 17,650 | 163 |
| Full reconciliation, 500 clients | 3.076 ms | 2.915–3.837 ms | 196,802 | 1,533 |
| Source reconciliation, 500 unchanged clients | 1.215 ms | 1.136–1.476 ms | 55,090 | 14 |

The source benchmark exercises the worst-shaped targeted input currently in the
suite: it still scans a 500-client source, but it does not allocate or emit
per-client changes when membership is unchanged. Real deployments generally
have far fewer clients per BSS.

### Observer event to Home Assistant state

A measurement process in the Home Assistant SSH add-on subscribed to both the
observer WebSocket and Home Assistant `state_changed` events. Both receipt
timestamps used the same process-local monotonic clock, avoiding wall-clock and
NTP error.

Fifty manual Wi-Fi off/on cycles produced 100 state transitions. Every measured
transition propagated from the observer WebSocket to Home Assistant in less
than 13 ms.

| Transition | Samples | Median | p95 | p99 | Maximum |
|---|---:|---:|---:|---:|---:|
| Observer `absent` → HA `not_home` | 50 | 6.706 ms | 8.134 ms | 8.848 ms | 8.848 ms |
| Observer `present` → HA `home` | 50 | 6.351 ms | 11.877 ms | 12.751 ms | 12.751 ms |
| Combined | 100 | 6.502 ms | 11.364 ms | 11.996 ms | 12.751 ms |

An earlier 10-transition pilot used the same measurement boundary and included
one 17.184 ms `home` transition. Across both sessions, all 110 transitions
completed in under 20 ms:

| Scope | Samples | Median | p95 | p99 | Maximum |
|---|---:|---:|---:|---:|---:|
| All measured sessions | 110 | 6.520 ms | 11.877 ms | 15.935 ms | 17.184 ms |

These values measure propagation after the observer publishes its authoritative
client event. They do **not** include radio/hostapd detection time or the
targeted `get_clients` call performed before an authoritative departure event.
The sanitized aggregate results are in
`performance/results/2026-07-flint3.json`; raw client-identifying output is not
committed.

### Idle router resources

The observer was sampled from `/proc` once per second for 299 seconds with one
Home Assistant WebSocket connected, normal 10-second discovery, and normal
30-second reconciliation. No client transitions or diagnostics requests were
made during the interval.

| Metric | Result |
|---|---:|
| Samples | 296 |
| Median RSS | 10,708 KiB |
| RSS range | 10,708–10,708 KiB |
| Process CPU | approximately 0.094% |
| Threads | 11 |

CPU utilization uses the delta of process user and system ticks from
`/proc/PID/stat`, the elapsed sampling interval, and the Linux `USER_HZ` value of
100. Virtual address size is intentionally not presented as resident memory;
the Go runtime reserves address space that is not resident in RAM.

### Router resources during 50 Wi-Fi cycles

The same process counters were sampled during the 100-transition manual run.

| Metric | Result |
|---|---:|
| Sampling interval | 234 seconds |
| Median RSS | 10,684 KiB |
| RSS range | 10,684–10,684 KiB |
| Process CPU | approximately 0.201% |
| Threads | 11 |

This interval includes targeted departure reconciliation, association events,
two WebSocket consumers, periodic discovery, and periodic full reconciliation.

## Reproduce the tests

Run host and router engine benchmarks from the repository root:

```sh
ROUTER_SSH_TARGET=root@ROUTER \
  BENCHMARK_COUNT=10 \
  scripts/performance/run-engine-benchmarks.sh /tmp/openwrt-performance
```

`ROUTER_SSH_TARGET` is required and accepts any SSH destination configured for
the target router. The script uses legacy SCP mode because it works with both
minimal Dropbear routers and routers that have the optional SFTP subsystem.

Sample the live observer once per second for five minutes:

```sh
scp -O scripts/performance/sample-router-resources.sh root@ROUTER:/tmp/
ssh root@ROUTER '/tmp/sample-router-resources.sh 300 1' > router-resources.csv
```

The CSV contains cumulative process CPU ticks, resident and virtual memory, and
thread count. Calculate CPU utilization from the tick delta and elapsed time
using the target's Linux user clock frequency. Do not treat Go heap `Alloc` from
`/v1/diagnostics` as process RSS.

The observer-to-HA harness requires Python and `aiohttp` on a trusted machine
that can reach both WebSockets:

```sh
SUPERVISOR_TOKEN=... python3 scripts/performance/measure-ha-latency.py \
  --observer-url ws://ROUTER:8787/v1/events \
  --observer-token-file /protected/path/token \
  --entity-id device_tracker.TEST_CLIENT \
  --label test-client \
  --transitions 50 \
  --output latency.json
```

Never pass a token on the command line, commit credential files, or publish raw
client IDs, MAC addresses, entity IDs, hostnames, SSIDs, router dumps, or
unsanitized environment output.

## Reporting policy

For results intended to support a performance claim:

- run at least ten benchmark repetitions;
- use at least 30 physical transitions per direction;
- publish median, p95, p99, and maximum only when the sample size supports them;
- record exact hardware, firmware, kernel, Home Assistant, Go, and commit
  versions;
- distinguish hostapd-to-observer latency from observer-to-HA propagation;
- keep full-path phone/radio results separate from software-only propagation;
- retain raw output privately or as a restricted CI artifact; and
- commit only small, reviewed, sanitized result files.

Desktop benchmark numbers are useful for regression comparisons but must not be
presented as router performance.
