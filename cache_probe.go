package main

// cache_probe.go — cached=0 KÖK-NEDEN DENEYİ (goal rev 2)
//
// Sınıflandırma yaptığımız "akış içinde aniden 0" olayı için kontrollü test:
// miss anında AYNI içerikle, AYNI hesapla, arka planda tek bir deneme yapılır
// (maxOutputTokens=1 → çıktı maliyeti 1 token; yanıt tüketilmez, hiçbir
// conversation store'a yazılmaz — doğrudan GeminiClient çağrılır).
//
//   - probe HIT  → içerik/upstream anlık; TEK SEFERLİK şans eseri shard
//                  rastgeleliği KANITLANIR (aynı içerik saniyeler içinde cache'lendi)
//   - probe MISS → isteğin kendisi/timing etkisi (upstream tarafında tekrarlanabilir)
//
// Maliyet denetimi: her miss'te ~prompt boyu input riski olduğu için
// süreç başına en fazla 20 probe (CACHE_MISS_PROBE_MAX ile sınır).
// Varsayılan KAPALI: CACHE_MISS_PROBE=1 ile açılır.

import (
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

var cacheProbeEnabled = os.Getenv("CACHE_MISS_PROBE") == "1"
var cacheProbeMax = envInt("CACHE_MISS_PROBE_MAX", 20)
var cacheProbeFired int64

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// fireCacheProbe miss satırı loglanırken çağrılır (dump ile aynı guard'lar).
func fireCacheProbe(e *RequestLogEntry) {
	if !cacheProbeEnabled || e == nil || e.payload == nil || GlobalGeminiClient == nil {
		return
	}
	// dumpPayloadOnMiss ile AYNI guard: yalnız cache'lenebilir gerçek miss'lerde
	if e.Cached > 0 || e.Prompt < payloadDumpMinPrompt {
		return
	}
	if e.Account == "" || GlobalAccountStore == nil {
		return // deney geçersiz: aynı hesap zorunlu
	}
	acc := GlobalAccountStore.GetAccountByID(e.Account)
	if acc == nil {
		return
	}
	if atomic.AddInt64(&cacheProbeFired, 1) > int64(cacheProbeMax) {
		return
	}

	probe := *e.payload // değer kopyası; Contents dilimi paylaşılan ama değişmez
	probe.Request.GenerationConfig.MaxOutputTokens = 1

	origReq := e.ReqID
	origPrompt := e.Prompt
	origCached := e.Cached
	email := e.Account

	go func() {
		start := time.Now()
		var usage *GeminiUsageMetadata
		var trace string
		err := GlobalGeminiClient.StreamGenerateContentWithAccount(&probe, acc, nil, func(ch *GeminiStreamChunk) error {
			if ch.UsageMetadata != nil {
				usage = ch.UsageMetadata
			} else if ch.Response.UsageMetadata != nil {
				usage = ch.Response.UsageMetadata
			}
			if ch.TraceID != "" {
				trace = ch.TraceID
			}
			return nil
		})
		fields := map[string]interface{}{
			"orig_req":    origReq,
			"orig_prompt": origPrompt,
			"orig_cach":   origCached,
			"probe_ms":    time.Since(start).Milliseconds(),
			"acc":         email,
		}
		if usage != nil {
			fields["probe_prompt"] = usage.PromptTokenCount
			fields["probe_cached"] = usage.CachedContentTokenCount
			if usage.CachedContentTokenCount > 0 {
				fields["verdict"] = "HIT_SPARSE_SHARD" // kanit: ayni icerik aninda cache'lendi
			} else {
				fields["verdict"] = "MISS_REPEAT" // upstream tarafinda tekrarlanabilir
			}
		} else {
			fields["verdict"] = "NO_USAGE"
		}
		if trace != "" {
			fields["probe_trace"] = trace
		}
		if err != nil {
			fields["err"] = err.Error()
		}
		fields["probe_no"] = atomic.LoadInt64(&cacheProbeFired)
		GlobalReqLog.LogEvent("cache_probe", fields)
	}()
}
