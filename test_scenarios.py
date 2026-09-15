import urllib.request
import json
import time
import sys

sys.stdout.reconfigure(encoding='utf-8')

def test_cache_scenario(name, messages):
    print(f"\n==================== TEST: {name} ====================")
    for turn in (1, 2):
        body = {
            "model": "gemini-3.8-flash-medium",
            "messages": messages if turn == 1 else messages + [{"role": "user", "content": "Bunu onayliyorum, sonraki adimi soyle."}],
            "stream": False
        }
        data = json.dumps(body).encode("utf-8")
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
            print(f"Tur {turn} ({elapsed}ms) -> Prompt: {prompt} | Cache: {cached} | Output: {output}")
        except Exception as e:
            print(f"Hata Tur {turn}: {e}")
        time.sleep(1)

# Scenario 1: Long text in USER message (10k tokens)
text_10k = ("Bu bir kurumsal dagitik sistem ve mikroservis mimarisidir. Veri tutarliligi, yuk dengeleme, raft konsensusu ve yuksek erisilebilirlik standartlari. " * 300)
test_cache_scenario("10k Tokens in User Message", [
    {"role": "user", "content": f"Ayrintili sistem metni:\n{text_10k}\nBu metni anladin mi?"}
])

# Scenario 2: Multi-turn chat (User -> Assistant -> User) with 10k tokens
test_cache_scenario("Multi-turn with 10k context", [
    {"role": "user", "content": f"Referans veri:\n{text_10k}\nBu veriyi hafizana al."},
    {"role": "assistant", "content": "Veriyi aldim ve hafizama kaydettim."}
])
