# -*- coding: utf-8 -*-
"""Compaction/llm-retry olaylarını DSH v2 oturumlarından çıkarır."""
import zstandard
import sys
import io
import json
import glob
import os
import datetime

sys.stdout.reconfigure(encoding="utf-8")

root = sys.argv[1] if len(sys.argv) > 1 else r"C:\Users\metin\.dsh-diag\sessions\--C-Users-metin-Desktop-DSH~0020v2--"


def ts(ms):
    if not ms:
        return "?"
    return datetime.datetime.fromtimestamp(ms / 1000).strftime("%H:%M:%S")


for path in sorted(glob.glob(os.path.join(root, "*", "session*.jsonl*")), key=os.path.getmtime, reverse=True):
    try:
        d = zstandard.ZstdDecompressor()
        with open(path, "rb") as f:
            data = io.TextIOWrapper(d.stream_reader(f), encoding="utf-8", errors="replace").read()
    except Exception as e:
        print(f"[{os.path.basename(os.path.dirname(path))}] acilamadi: {e}")
        continue
    lines = [l for l in data.split("\n") if l.strip()]
    hits = []
    models = set()
    for l in lines:
        try:
            e = json.loads(l)
        except Exception:
            continue
        t = e.get("type", "")
        if t.startswith("compaction/") or t == "llm/retry":
            hits.append(e)
        if t == "model/selection":
            models.add((e["data"].get("provider", "?"), e["data"].get("model", "?")))
    if not hits:
        continue
    sid = os.path.basename(os.path.dirname(path))
    print(f"\n######## {sid}  ({len(lines)} satir, {len(hits)} compaction/olay)")
    print(f"  modeller: {sorted(models)}")
    for e in hits:
        t = e["type"]
        data_d = e.get("data", {})
        # compaction olaylarinin ozetini cikar
        if t == "compaction/start":
            desc = {k: data_d.get(k) for k in ("reason", "threshold", "pressure", "surfaceTokens", "keepTokens") if k in data_d}
            print(f"  {ts(e['time'])} START  {desc}")
        elif t == "compaction/end":
            desc = {k: data_d.get(k) for k in ("ok", "status", "error", "errorMessage", "removedCount", "removedTokens", "addedTokens", "durationMs") if k in data_d}
            print(f"  {ts(e['time'])} END    {json.dumps(desc, ensure_ascii=False)[:400]}")
        elif t == "compaction/summary":
            desc = {k: (str(data_d.get(k))[:180]) for k in data_d}
            print(f"  {ts(e['time'])} SUMM   {json.dumps(desc, ensure_ascii=False)[:400]}")
        elif t == "compaction/prune":
            desc = {k: data_d.get(k) for k in ("count", "tokens", "items", "reason") if k in data_d}
            print(f"  {ts(e['time'])} PRUNE  {json.dumps(desc, ensure_ascii=False)[:300]}")
        elif t == "llm/retry":
            desc = {k: (str(data_d.get(k))[:220]) for k in data_d}
            print(f"  {ts(e['time'])} RETRY  {json.dumps(desc, ensure_ascii=False)[:450]}")
