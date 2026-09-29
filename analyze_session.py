# -*- coding: utf-8 -*-
"""DSH oturum analizi: model bazlı mesaj blokları + mimo flash-free narration davranışı."""
import zstandard
import sys
import io
import json
import collections

sys.stdout.reconfigure(encoding="utf-8")

path = sys.argv[1]
only = sys.argv[2] if len(sys.argv) > 2 else "flash-free"

d = zstandard.ZstdDecompressor()
with open(path, "rb") as f:
    data = io.TextIOWrapper(d.stream_reader(f), encoding="utf-8", errors="replace").read()

lines = [l for l in data.split("\n") if l.strip()]

by_model = collections.Counter()
msgs = []
for l in lines:
    try:
        e = json.loads(l)
    except Exception:
        continue
    if e.get("type") != "assistant/message":
        continue
    m = e["data"]["message"]
    src = m.get("source") or {}
    model = src.get("model", "?")
    by_model[model] += 1
    msgs.append((model, e["data"].get("turn"), e["data"].get("step"),
                 m.get("content") or [], e["seq"]))

print("--- mesaj sayisi modele gore ---")
for k, v in by_model.most_common():
    print(f"{v:5d}  {k}")
print()

# --- nicel desen sayimi: her model icin blok dizilim frekanslari ---
print("--- blok dizilim desenleri (tum modeller) ---")
pattern_by_model = collections.defaultdict(collections.Counter)
for model, turn, step, content, seq in msgs:
    kinds = []
    for b in content:
        t = b.get("type")
        if t == "text":
            kinds.append("T" if (b.get("text") or "").strip() else "_")
        elif t in ("thinking", "reasoning"):
            kinds.append("R")
        elif t == "tool-call":
            kinds.append("C")
        else:
            kinds.append("?")
    pattern_by_model[str(model)][">".join(kinds) or "(bos)"] += 1
for model, pats in pattern_by_model.items():
    print(f"  [{model}]")
    for pat, c in pats.most_common(8):
        print(f"      {c:4d}  {pat}")
print()

# --- hedef modelin zaman cizelgesi ---
print(f"--- zaman cizelgesi: {only} ---")
n = 0
for model, turn, step, content, seq in msgs:
    if only not in str(model):
        continue
    n += 1
    kinds = [b.get("type") for b in content]
    print(f"[seq {seq} t{turn}.{step}] " + " > ".join(kinds))
    for b in content:
        t = b.get("type")
        if t == "text":
            txt = b.get("text", "")
            print(f"   TEXT({len(txt)}ch): {txt[:350]}")
        elif t in ("thinking", "reasoning"):
            txt = b.get("text") or b.get("thinking") or b.get("summary") or ""
            if isinstance(txt, list):
                txt = " ".join(str(x) for x in txt)
            print(f"   {t.upper()}({len(str(txt))}ch): {str(txt)[:160]}")
        elif t == "tool-call":
            name = b.get("name", "?")
            args = b.get("arguments", "")
            print(f"   TOOL: {name}  args={str(args)[:110]}")
print(f"... toplam {only} mesaji: {n}")
