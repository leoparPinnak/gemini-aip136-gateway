import urllib.request
import json
import time
import sys

sys.stdout.reconfigure(encoding='utf-8')

# Generate a prompt with ~2500 tokens
long_doc = "Sistem ve mimari referans belgesi:\n" + ("Bilesen analizi ve mikroservis entegrasyonu, veri akisi ve durum yonetimi protokolleri detayli tanimi. " * 200)

print(f"Metin uzunlugu: {len(long_doc)} karakter, {len(long_doc.split())} kelime")

for turn in (1, 2):
    req_body = {
        "model": "gemini-3.8-flash-medium",
        "messages": [
            {"role": "system", "content": long_doc},
            {"role": "user", "content": f"Soru {turn}: Bu sistemi tek kelimeyle tanimla."}
        ],
        "stream": False
    }
    
    data = json.dumps(req_body).encode("utf-8")
    req = urllib.request.Request("http://127.0.0.1:3050/v1/chat/completions", data=data, headers={"Content-Type": "application/json"})
    t0 = time.time()
    with urllib.request.urlopen(req) as res:
        res_data = json.loads(res.read().decode("utf-8"))
    elapsed = int((time.time() - t0) * 1000)
    
    usage = res_data.get("usage", {})
    cached = usage.get("prompt_tokens_details", {}).get("cached_tokens", 0)
    prompt = usage.get("prompt_tokens", 0)
    output = usage.get("completion_tokens", 0)
    content = res_data["choices"][0]["message"]["content"]
    print(f"Tur {turn} ({elapsed}ms): Prompt={prompt}, Output={output}, Cached={cached} | Yanit: {content.strip()}")
    time.sleep(1)
