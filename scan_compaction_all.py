# -*- coding: utf-8 -*-
"""Tum DSH diag oturumlarinda: compaction hatalari + flash-medium oturumlari + zaman eslemesi."""
import zstandard
import sys
import io
import json
import glob
import os
import datetime

sys.stdout.reconfigure(encoding="utf-8")

root = r"C:\Users\metin\.dsh-diag\sessions"


def ts(ms):
    if not ms:
        return "?"
    return datetime.datetime.fromtimestamp(ms / 1000).strftime("%m-%d %H:%M:%S")


files = glob.glob(os.path.join(root, "**", "session*.jsonl*"), recursive=True)
files = [f for f in files if os.path.getmtime(f) > (datetime.datetime.now() - datetime.timedelta(days=2)).timestamp()]
print(f"son 2 gunun oturumlari: {len(files)}")

for path in files:
    try:
        d = zstandard.ZstdDecompressor()
        with open(path, "rb") as f:
            data = io.TextIOWrapper(d.stream_reader(f), encoding="utf-8", errors="replace").read()
    except Exception:
        continue
    compaction = []
    medium = False
    for l in data.split("\n"):
        if '"compaction/' not in l and not ('model/selection' in l and 'medium' in l):
            continue
        try:
            e = json.loads(l)
        except Exception:
            continue
        t = e.get("type", "")
        if t == "model/selection" and "medium" in json.dumps(e.get("data", {})):
            medium = True
        if t.startswith("compaction/"):
            compaction.append(e)
    if not compaction and not medium:
        continue
    sid = os.path.basename(os.path.dirname(path))
    cwd = ""
    if compaction:
        pass
    # baslik satirindan cwd
    head = data.split("\n", 1)[0]
    try:
        cwd = json.loads(head).get("cwd", "")
    except Exception:
        pass
    errs = [e for e in compaction if e["type"] == "compaction/end" and (e.get("data") or {}).get("error")]
    print(f"\n== {sid} | cwd={cwd} | medium={medium} | compaction={len(compaction)} | HATA={len(errs)}")
    for e in compaction:
        if e["type"] in ("compaction/start", "compaction/end"):
            d_ = e.get("data", {})
            print(f"   {ts(e['time'])} {e['type'].split('/')[1].upper():5s} {json.dumps(d_, ensure_ascii=False)[:260]}")
