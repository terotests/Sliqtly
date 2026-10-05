#!/usr/bin/env python3
"""Turns the benchmark's JSON lines into Markdown tables.

usage: report.py results/*.jsonl > REPORT.md
"""
import json
import sys
from collections import defaultdict

ENGINES = ["sliqtly", "redb", "fjall", "lmdb", "rocksdb", "sqlite"]

rows = []
for path in sys.argv[1:]:
    with open(path) as f:
        for line in f:
            line = line.strip()
            if line.startswith("{"):
                rows.append(json.loads(line))

# (suite, param, phase) -> engine -> row; several runs of one measurement
# become one row holding the median of each numeric field
runs = defaultdict(list)
for r in rows:
    runs[(r["suite"], r["param"], r["phase"], r["engine"])].append(r)


def median(xs):
    xs = sorted(xs)
    n = len(xs)
    return xs[n // 2] if n % 2 else (xs[n // 2 - 1] + xs[n // 2]) / 2


by = defaultdict(dict)
nruns = {}
for (suite, param, phase, engine), rs in runs.items():
    merged = dict(rs[0])
    for k, v in rs[0].items():
        if isinstance(v, (int, float)) and not isinstance(v, bool) and k != "rep":
            merged[k] = median([r[k] for r in rs if k in r])
        elif k == "digest":
            merged[k] = rs[0][k] if all(r.get(k) == rs[0][k] for r in rs) else "differs between runs"
    by[(suite, param, phase)][engine] = merged
    nruns[(suite, engine)] = max(nruns.get((suite, engine), 0), len(rs))


def fmt(v, kind):
    if v is None:
        return "–"
    if kind == "ops":
        if v >= 1e6:
            return f"{v / 1e6:.2f} M"
        if v >= 1e3:
            return f"{v / 1e3:.1f} k"
        return f"{v:.0f}"
    if kind == "us":
        if v >= 1000:
            return f"{v / 1000:.1f} ms"
        if v >= 10:
            return f"{v:.0f} µs"
        return f"{v:.2f} µs"
    if kind == "mb":
        return f"{v / 1e6:.1f} MB"
    if kind == "x":
        return f"{v:.2f}×"
    if kind == "ms":
        return f"{v:.1f} ms"
    return str(v)


def best_mark(vals, higher_better):
    nums = [v for v in vals if isinstance(v, (int, float))]
    if not nums:
        return None
    return max(nums) if higher_better else min(nums)


def table(title, suite, param, phases, metrics):
    """phases: list of (phase, label); metrics: list of (field, label, kind, higher_better)."""
    print(f"\n#### {title}\n")
    engines = [e for e in ENGINES if any(e in by.get((suite, param, p), {}) for p, _ in phases)]
    print("| workload | metric | " + " | ".join(engines) + " |")
    print("|---|---|" + "---|" * len(engines))
    for phase, plabel in phases:
        d = by.get((suite, param, phase), {})
        if not d:
            continue
        for field, mlabel, kind, hb in metrics:
            vals = [d.get(e, {}).get(field) for e in engines]
            if all(v is None for v in vals):
                continue
            b = best_mark(vals, hb)
            cells = []
            for v in vals:
                s = fmt(v, kind)
                if v is not None and v == b:
                    s = f"**{s}**"
                cells.append(s)
            print(f"| {plabel} | {mlabel} | " + " | ".join(cells) + " |")


RAW_PHASES = [
    ("fill_seq", "put sequential (batches, no sync)"),
    ("fill_random", "put random (batches, no sync)"),
    ("get_random", "get random"),
    ("get_missing", "get missing key"),
    ("prefix_scan", "prefix scan"),
    ("range_scan", "range scan"),
    ("snapshot_read_10", "snapshot, 10 gets"),
    ("overwrite", "overwrite (batches, no sync)"),
    ("delete", "delete (batches, no sync)"),
    ("batch_commit_durable", "batch commit, fsync"),
    ("sync_commit_1", "1-put commit, fsync"),
    ("concurrent_writer", "1 writer (fsync) ‖ 3 readers: writer"),
    ("concurrent_readers_3", "1 writer ‖ 3 readers: readers"),
]
LAT = [
    ("ops_per_sec", "ops/s", "ops", True),
    ("p50_us", "p50", "us", False),
    ("p99_us", "p99", "us", False),
    ("p999_us", "p99.9", "us", False),
]

params = sorted({p for (s, p, _) in by if s == "raw"}, key=lambda x: int(x))
if params:
    print("## Raw engine benchmark\n")
    for p in params:
        table(f"Values of {p} bytes", "raw", p, RAW_PHASES, LAT)
        table(f"Values of {p} bytes: footprint", "raw", p, [("footprint", "after the run"), ("restart", "reopen")], [
            ("disk_alloc_bytes", "disk", "mb", False),
            ("space_amp", "disk / live data", "x", False),
            ("write_amp", "bytes written / logical", "x", False),
            ("peak_rss_kb", "peak RSS (kB)", None, False),
            ("open_ms", "open time", "ms", False),
        ])

SEM_PHASES = [
    ("op_get_document", "get document (50%)"),
    ("op_list_room_documents", "list room documents (15%)"),
    ("op_update_document", "update document, fsync (10%)"),
    ("op_membership_lookup", "membership lookup (10%)"),
    ("op_graph_neighbors", "graph neighbors (5%)"),
    ("op_graph_traverse3", "traverse depth 3 (5%)"),
    ("op_room_or_link_change", "room / link change, fsync (5%)"),
    ("watch_100", "watch(after_seq), 100 entries"),
]
sparams = sorted({p for (s, p, _) in by if s == "semantic"}, key=float)
for p in sparams:
    print(f"\n## Semantic benchmark (scale {p})\n")
    table("Load and mixed workload", "semantic", p, [("load", "bulk load"), ("mixed", "mixed workload")], [
        ("ops_per_sec", "ops/s", "ops", True),
        ("seconds", "seconds", None, False),
    ])
    table("Per operation", "semantic", p, SEM_PHASES, LAT)
    table("Footprint", "semantic", p, [("load_footprint", "after load"), ("footprint", "after run"), ("restart", "reopen")], [
        ("disk_alloc_bytes", "disk", "mb", False),
        ("space_amp", "disk / logical", "x", False),
        ("write_amp", "bytes written / logical", "x", False),
        ("peak_rss_kb", "peak RSS (kB)", None, False),
        ("open_ms", "open time", "ms", False),
    ])
    digests = {e: by.get(("semantic", p, "mixed"), {}).get(e, {}).get("digest") for e in ENGINES}
    wd = {e: by.get(("semantic", p, "watch_100"), {}).get(e, {}).get("digest") for e in ENGINES}
    print("\nAnswer digests (equal digests = identical answers to every query):\n")
    print("| engine | mixed workload | watch |")
    print("|---|---|---|")
    for e in ENGINES:
        if digests.get(e):
            print(f"| {e} | `{digests[e]}` | `{wd.get(e)}` |")
