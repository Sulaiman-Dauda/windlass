#!/usr/bin/env python3
"""Median of each metric per stage from measure.sh CSV files.

Usage: summarise.py results/*.csv
Prints one block per file: stage, then metric=median for each metric.
"""
import collections
import statistics
import sys

for path in sys.argv[1:]:
    rows = collections.defaultdict(list)
    order = []
    with open(path) as f:
        for line in f:
            parts = line.strip().split(",")
            if len(parts) != 4 or line.startswith("#"):
                continue
            _, stage, metric, value = parts
            try:
                v = float(value)
            except ValueError:
                continue
            if stage not in order:
                order.append(stage)
            rows[(stage, metric)].append(v)
    print(f"== {path}")
    for stage in order:
        metrics = sorted(m for (s, m) in rows if s == stage)
        n = max(len(rows[(stage, m)]) for m in metrics)
        out = [f"{m}={statistics.median(rows[(stage, m)]):.1f}" for m in metrics
               if statistics.median(rows[(stage, m)]) > 0 or m in ("used", "swap_used")]
        print(f"  {stage} (n={n}): " + " ".join(out))
