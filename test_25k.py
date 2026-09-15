import urllib.request
import json
import time
import sys

sys.stdout.reconfigure(encoding='utf-8')

# Let's create a large context of 25,000 tokens
repeat_block = "Enterprise cloud distributed architecture with consensus raft cluster, zero-trust network mTLS, distributed transaction saga pattern. "
# ~130 chars per block = ~30 tokens. 800 repeats = ~24,000 tokens
big_context = repeat_block * 800

print(f"Olusturulan metin: {len(big_context)} karakter (~{len(big_context.split())} kelime)")

for turn in (1, 2):
    req_body = {
        "model": "gemini-3.8-flash-high",
        "messages": [
            {"role": "user", "content": f"Context data:\n{big_context}\n\nQuestion {turn}: What is this text about in 3 words?"}
        ],
        "stream": False
    }
    
    data = json.dumps(req_body).encode("utf-8")
    req = urllib.request.Request("http://127.0.0.1:3050/v1/chat/completions", data=data, headers={"Content-Type": "application/json"})
    t0 = time.time()
    try:
        with urllib.request.urlopen(req) as res:
            res_data = json.loads(res.read().decode("utf-8"))
        elapsed = int((time.time() - t0) * 1000)
        usage = res_data.get("usage", {})
        prompt = usage.get("prompt_tokens", 0)
        cached = usage.get("prompt_tokens_details", {}).get("cached_tokens", 0)
        output = usage.get("completion_tokens", 0)
        content = res_data["choices"][0]["message"]["content"]
        print(f"Turn {turn} ({elapsed}ms) -> Prompt: {prompt} | CACHE: {cached} | Output: {output} | Reply: {content.strip()}")
    except Exception as e:
        print(f"Error Turn {turn}: {e}")
    time.sleep(2)
