#!/usr/bin/env python3
"""Capture REAL metric series from the live abb-genix kind cluster.

Sources (exactly what obsd scrapes):
  - cAdvisor via the API-server proxy:  kubectl get --raw /api/v1/nodes/<node>/proxy/metrics/cadvisor
  - app /metrics via exec:              kubectl -n <ns> exec deploy/<pod> -- urlopen localhost:8080/metrics

Writes long-format CSV: wall_ts, source, pod, container, metric, value
Flushes every cycle so a kill -INT leaves a complete file.

Design-independent: pass the metric/pod sets on the CLI; the experiment driver decides which.
"""
import argparse
import csv
import re
import signal
import subprocess
import sys
import time

PROM_LINE = re.compile(r'^(?P<name>[a-zA-Z_:][a-zA-Z0-9_:]*)\{(?P<labels>[^}]*)\}\s+(?P<val>[-+0-9.eE]+)')
PROM_BARE = re.compile(r'^(?P<name>[a-zA-Z_:][a-zA-Z0-9_:]*)\s+(?P<val>[-+0-9.eE]+)\s*$')


def kubectl(*args, timeout=30):
    return subprocess.run(["kubectl", *args], capture_output=True, text=True, timeout=timeout)


def detect_node():
    r = kubectl("get", "nodes", "-o", "jsonpath={.items[0].metadata.name}")
    return r.stdout.strip()


def parse_labels(s):
    out = {}
    for m in re.finditer(r'(\w+)="([^"]*)"', s):
        out[m.group(1)] = m.group(2)
    return out


def scrape_cadvisor(node, metrics, namespace):
    """Return list of (pod, container, metric, value) for workload containers in namespace."""
    r = kubectl("get", "--raw", f"/api/v1/nodes/{node}/proxy/metrics/cadvisor", timeout=40)
    rows = []
    if r.returncode != 0:
        sys.stderr.write(f"[cadvisor] rc={r.returncode} {r.stderr[:200]}\n")
        return rows
    for line in r.stdout.splitlines():
        if not line or line[0] == '#':
            continue
        # cheap prefilter
        if not any(line.startswith(m) for m in metrics):
            continue
        m = PROM_LINE.match(line)
        if not m:
            continue
        name = m.group("name")
        if name not in metrics:
            continue
        lbl = parse_labels(m.group("labels"))
        if lbl.get("namespace") != namespace:
            continue
        container = lbl.get("container", "")
        # keep only the real workload container (skip pod-cgroup roll-up "" and the pause "POD")
        if container in ("", "POD"):
            continue
        pod = lbl.get("pod", "")
        try:
            val = float(m.group("val"))
        except ValueError:
            continue
        rows.append((pod, container, name, val))
    return rows


def scrape_app(namespace, deploy):
    """exec into a deployment pod and pull its /metrics gauges."""
    py = ("from urllib.request import urlopen;"
          "import sys;"
          "sys.stdout.write(urlopen('http://localhost:8080/metrics', timeout=5).read().decode('utf-8','replace'))")
    r = kubectl("-n", namespace, "exec", f"deploy/{deploy}", "--", "python3", "-c", py, timeout=25)
    rows = []
    if r.returncode != 0:
        sys.stderr.write(f"[app:{deploy}] rc={r.returncode} {r.stderr[:160]}\n")
        return rows
    for line in r.stdout.splitlines():
        if not line or line[0] == '#':
            continue
        m = PROM_LINE.match(line) or PROM_BARE.match(line)
        if not m:
            continue
        name = m.group("name")
        try:
            val = float(m.group("val"))
        except ValueError:
            continue
        rows.append((deploy, "app", name, val))
    return rows


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--interval", type=float, default=5.0)
    ap.add_argument("--duration", type=float, default=0.0, help="seconds; 0 = until SIGINT")
    ap.add_argument("--out", required=True)
    ap.add_argument("--namespace", default="abb-genix")
    ap.add_argument("--node", default="")
    ap.add_argument("--cadvisor-metrics", default="container_memory_working_set_bytes,container_cpu_usage_seconds_total,container_memory_rss,container_network_receive_bytes_total,container_network_transmit_bytes_total")
    ap.add_argument("--app-deploys", default="", help="comma list of deployments to scrape /metrics from (e.g. pdm-analyzer,stream-processor,asset-api)")
    args = ap.parse_args()

    node = args.node or detect_node()
    metrics = [m for m in args.cadvisor_metrics.split(",") if m]
    deploys = [d for d in args.app_deploys.split(",") if d]
    sys.stderr.write(f"[capture] node={node} ns={args.namespace} interval={args.interval}s out={args.out}\n")
    sys.stderr.write(f"[capture] cadvisor metrics={metrics}\n[capture] app deploys={deploys}\n")

    stop = {"v": False}
    signal.signal(signal.SIGINT, lambda *_: stop.update(v=True))
    signal.signal(signal.SIGTERM, lambda *_: stop.update(v=True))

    f = open(args.out, "w", newline="")
    w = csv.writer(f)
    w.writerow(["wall_ts", "source", "pod", "container", "metric", "value"])
    t0 = time.time()
    cycles = 0
    while not stop["v"]:
        cyc_start = time.time()
        ts = round(cyc_start, 3)
        n = 0
        for (pod, container, name, val) in scrape_cadvisor(node, metrics, args.namespace):
            w.writerow([ts, "cadvisor", pod, container, name, val]); n += 1
        for d in deploys:
            for (pod, container, name, val) in scrape_app(args.namespace, d):
                w.writerow([ts, "app", pod, container, name, val]); n += 1
        f.flush()
        cycles += 1
        if cycles % 6 == 0:
            sys.stderr.write(f"[capture] cycle={cycles} rows_this_cycle={n} elapsed={int(cyc_start-t0)}s\n")
        if args.duration and (time.time() - t0) >= args.duration:
            break
        dt = args.interval - (time.time() - cyc_start)
        if dt > 0:
            time.sleep(dt)
    f.close()
    sys.stderr.write(f"[capture] done: {cycles} cycles -> {args.out}\n")


if __name__ == "__main__":
    main()
