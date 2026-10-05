#!/usr/bin/env python3
"""Median of repeated runs per engine/durability, as Markdown tables.

Usage: summarize.py results.jsonl [RESULTS.md]
With a RESULTS.md path, replaces the text between the BEGIN/END TABLES
markers and keeps the rest of the file; otherwise prints the tables."""
import json, statistics, sys
from collections import defaultdict

OPS = [("get_document", "get document"), ("documents_in_room", "documents in room (20)"),
       ("rooms_for_user", "rooms for user"), ("update_document", "update document (read-modify-write tx)"),
       ("create_room", "create room + owner (tx)")]
ORDER = [("sqlite", "sync"), ("log", "sync"), ("sqlite", "nosync"), ("log", "nosync"), ("statebin", "nosync")]
NAME = {"sqlite": "SQLite", "log": "Sliqtly log", "statebin": "Sliqtly state-bin (before)"}

runs = defaultdict(list)
for line in open(sys.argv[1]):
    r = json.loads(line)
    runs[(r["engine"], r["durability"])].append(r)
cols = [c for c in ORDER if c in runs]
med = lambda c, f: statistics.median(f(r) for r in runs[c])

def fmt(n):
    return f"{n/1e6:.2f}M" if n >= 1e6 else f"{n/1e3:.1f}k" if n >= 1e3 else f"{n:.0f}"

out = []
head = "| | " + " | ".join(f"{NAME[e]} {d}" for e, d in cols) + " |"
sep = "|---" * (len(cols) + 1) + "|"
out += ["Throughput (ops/s, median of %d runs; higher is better)" % min(len(runs[c]) for c in cols), "", head, sep]
for k, label in OPS:
    out.append(f"| {label} | " + " | ".join(fmt(med(c, lambda r: r[k]["ops_per_sec"])) for c in cols) + " |")
out.append("| bulk load (rows/s, batches of 1000) | " + " | ".join(fmt(med(c, lambda r: r["load"]["rows_per_sec"])) for c in cols) + " |")
out += ["", "Latency p50 / p99 (µs; lower is better)", "", head, sep]
for k, label in OPS:
    out.append(f"| {label} | " + " | ".join(
        f"{med(c, lambda r: r[k]['p50_us']):.1f} / {med(c, lambda r: r[k]['p99_us']):.1f}" for c in cols) + " |")
out += ["", "Resources", "", head, sep]
out.append("| open after load (ms) | " + " | ".join(f"{med(c, lambda r: r['open_ms']):.1f}" for c in cols) + " |")
out.append("| disk (MB) | " + " | ".join(f"{med(c, lambda r: r['disk_bytes'])/1e6:.1f}" for c in cols) + " |")
out.append("| peak RSS (MB) | " + " | ".join(f"{med(c, lambda r: r['peak_rss_kb'])/1024:.0f}" for c in cols) + " |")
d = next(iter(runs.values()))[0]["dataset"]
out += ["", f"Dataset: {d['rooms']} rooms, {d['documents']} documents, {d['memberships']} memberships, {d['users']} users."]
tables = "\n".join(out)

if len(sys.argv) > 2:
    path = sys.argv[2]
    text = open(path).read()
    a, b = "<!-- BEGIN TABLES -->", "<!-- END TABLES -->"
    text = text[: text.index(a) + len(a)] + "\n" + tables + "\n" + text[text.index(b):]
    open(path, "w").write(text)
else:
    print(tables)
