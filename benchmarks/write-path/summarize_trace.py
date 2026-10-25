#!/usr/bin/env python3
"""Summarize excluded strace fsync diagnostics by persisted object type."""

import collections
import gzip
import json
import pathlib
import re
import sys

path = pathlib.Path(sys.argv[1])
stream = gzip.open(path, "rt") if path.suffix == ".gz" else path.open()
counts = collections.Counter()
seconds = collections.Counter()
pending = {}
for line in stream:
    pid = line.split()[0] if line.split() else ""
    call = re.search(r"fsync\(\d+<([^>]+)>", line)
    category = None
    if call:
        name = pathlib.Path(call[1]).name
        category = (
            "wal"
            if name == "data.log" or name.startswith("wal_")
            else "manifest"
            if name == "MANIFEST.tmp"
            else "sstable_index"
            if ".index" in name
            else "sstable_data"
            if name.startswith("sstable_")
            else "directory"
        )
        counts[category] += 1
        if "<unfinished ...>" in line:
            pending[pid] = category
    elif "<... fsync resumed>" in line:
        category = pending.pop(pid, None)
    duration = re.search(r"<([\d.]+)>\s*$", line)
    if category and duration:
        seconds[category] += float(duration[1])
stream.close()
assert not pending, "incomplete traced syscall"
print(
    json.dumps(
        {
            "scope": "whole instrumented test process; diagnostic wall times are not uninstrumented benchmark latencies",
            "objects": {
                name: {"fsync_calls": count, "traced_seconds": round(seconds[name], 6)}
                for name, count in sorted(counts.items())
            },
            "total_fsync_calls": sum(counts.values()),
            "summed_traced_fsync_seconds": round(sum(seconds.values()), 6),
        },
        indent=2,
    )
)
