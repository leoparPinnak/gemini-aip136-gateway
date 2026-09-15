# ⚡ HARNESS <-> GEMINI ADAPTÖRÜ: HİYERARŞİK ANA MİMARİ VE UYGULAMA PLANI (MASTER PLAN)

**Belge Kimliği:** `MASTER-PLAN-DSH-GEMINI-V1`  
**Tarih:** 14 Eylül 2026  
**Durum:** ONAYLANDI & UYGULAMAYA HAZIR  
**Konum:** `C:\Users\metin\Desktop\plan\MASTER_PLAN.md`  

---

## 1. YÖNETİCİ ÖZETİ VE MİMARİ VİZYON

Bu planın temel amacı; DeepSeek Harness (DSH) ekosisteminin kullandığı **OpenAI Responses API (`POST /v1/responses`)** protokolünü, Google'ın yeni nesil **Gemini AIP-136 / CloudCode dahili motoruna (`POST /v1internal:streamGenerateContent?alt=sse`)** bağlayan, sıfır veri kayıplı, çift yönlü kesintisiz borulamaya (full-duplex non-blocking) sahip ve **%90+ Cache Hit (Önbellek İsabeti)** garantisi sunan endüstriyel bir adaptör inşa etmektir.

4 uzman ajanın bağımsız araştırma raporları ([01_DSH_OPENAI_RESPONSES_PROTOCOL.md](file:///C:/Users/metin/Desktop/plan/01_DSH_OPENAI_RESPONSES_PROTOCOL.md), [02_GEMINI_AIP136_PROTOCOL.md](file:///C:/Users/metin/Desktop/plan/02_GEMINI_AIP136_PROTOCOL.md), [03_DETERMINISTIC_CACHE_ROUTER.md](file:///C:/Users/metin/Desktop/plan/03_DETERMINISTIC_CACHE_ROUTER.md), [04_TOOL_ENGINE_AGENT_CAPABILITIES.md](file:///C:/Users/metin/Desktop/plan/04_TOOL_ENGINE_AGENT_CAPABILITIES.md)) sentezlenerek bu nihai ana plan hazırlanmıştır.

---

## 2. UÇTAN UCA SİSTEM TOPOLOJİSİ

```
┌────────────────────────────────────────────────────────────────────────┐
│                        DEEPSEEK HARNESS (DSH)                          │
│   (Agent Loop, Presets, SKILL.md, llm-pi-ai, Terminal, MCP Client)     │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
                  POST /v1/responses (OpenAI Wire Format)
                  SSE Event Stream (response.output_text.delta, etc.)
                                    │
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│               CANONICAL DSH-TO-GEMINI ADAPTER GATEWAY                  │
│                     (Node.js / TypeScript Core)                        │
├────────────────────────────────────────────────────────────────────────┤
│  1. Canonical Prefix Normalizer (Token 0 Anchor & RFC 8785 Sort)       │
│  2. Dual-Protocol Transpiler (Responses <-> AIP-136 Schema)            │
│  3. Reasoning & Effort Router (Thinking Budget & Model Multiplexer)    │
│  4. Full-Duplex SSE Stream Pipeline (Byte-level Non-blocking Pipe)     │
│  5. Win32 Job Object Tool Bridge (Process Tree & Zombie Isolation)     │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
                  POST /v1internal:streamGenerateContent?alt=sse
                  SSE Stream (thought: true, thoughtSignature, functionCall)
                                    │
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                  GOOGLE CLOUDCODE / GEMINI BACKEND                     │
│               (daily-cloudcode-pa.googleapis.com:443)                  │
│         [TPU KV Context Cache: %90+ Hit Rate (24K+ Tokens)]            │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 3. PROTOKOL DÖNÜŞÜM VE EŞLEME MATRİSİ

### 3.1. İstek (Request) Gövdesi Eşleşmesi

| OpenAI Responses API (`/v1/responses`) | Gemini CloudCode API (`/v1internal:streamGenerateContent`) | Dönüşüm & Normalizasyon Kuralı |
| :--- | :--- | :--- |
| `model` | `model` | Efor eşleme: `gemini-3.8-flash-low`, `gemini-3.8-flash-tiered`, `gemini-3.8-flash-high`, `gemini-3.1-flash-lite`. |
| `input` (roller: `system`, `developer`) | `request.systemInstruction` | Kök önbellek için `systemInstruction.parts[0].text` içine alınır. CloudCode iç şemasında `role: "user"` olarak verilir. |
| `input` (roller: `user`) | `request.contents[].role = "user"` | Metin blokları `parts: [{ text: "..." }]` olarak aktarılır. |
| `input` (roller: `assistant`) | `request.contents[].role = "model"` | Asistan metinleri `parts: [{ text: "..." }]` olarak aktarılır. |
| `input` (item: `function_call`) | `request.contents[].role = "model"` | `parts: [{ functionCall: { id, name, args: JSON.parse(arguments) }, thoughtSignature }]`. |
| `input` (item: `function_call_output`)| `request.contents[].role = "model"` | `parts: [{ functionResponse: { id, name, response: { output } } }]`. CloudCode formatında `role: "model"` korunur. |
| `tools` (type: `function`) | `request.tools[].functionDeclarations` | JSON Schema tipleri büyük harfe (`OBJECT`, `STRING` vb.) çevrilir; `$schema` ve `additionalProperties` elenir. |
| `reasoning.effort` (`low/medium/high`) | `generationConfig.thinkingConfig` | `includeThoughts: true`, `thinkingBudget: 1000` (low) veya `-1` (tiered/high). |
| `max_output_tokens` | `generationConfig.maxOutputTokens` | Değer doğrudan aktarılır (varsayılan: 65536). |
| `prompt_cache_key` / `sessionId` | `request.sessionId` | KV Önbellek afinitesi için 64-bit negatif hash string'e çevrilir. |

---

### 3.2. Yanıt (Streaming SSE) Eşleşmesi

| Gemini SSE Akış Parçacığı | OpenAI Responses SSE Olayı | Açıklama |
| :--- | :--- | :--- |
| *Akış başlangıcı* | `response.created` | `response.id`, model ve oturum meta verisi başlatılır. |
| `parts[].thought === true` | `response.output_item.added` (`type: "reasoning"`)<br>-> `response.reasoning_text.delta` | Düşünce tokenları saf akıl yürütme olarak OpenAI istemcisine akar. |
| `thoughtSignature: "..."` | `response.reasoning_text.done` | Kriptografik imza arka planda saklanır, multi-turn replay için korunur. |
| `parts[].functionCall` | `response.output_item.added` (`type: "function_call"`)<br>-> `response.function_call_arguments.delta` | Fonksiyon adı ve JSON argüman parçaları anında istemciye basılır. |
| `parts[].text` (normal metin) | `response.output_item.added` (`type: "message"`)<br>-> `response.output_text.delta` | Modelin son kullanıcıya yönelik metin çıktıları iletilir. |
| `finishReason: "STOP"` | `response.output_item.done` | İlgili çıktı bloğu tamamlanır. |
| `usageMetadata` (son paket) | `response.completed` | `prompt_tokens`, `output_tokens`, `input_tokens_details.cached_tokens` eksiksiz hesaplanarak akış kapatılır. |

---

## 4. %90+ CACHE HIT (ÖN BELLEK İSABETİ) MOTORU

Google Gemini TPU KV Önbelleğinin bozulmaması için **Strict Prefix Invariance (Katı Ön Ek Değişmezliği)** kuralı işletilecektir:

### 4.1. Çift Katmanlı İstem Mimarisi (Two-Tier Prompt Layout)
1. **Statik Değişmez Çapa (Token 0 Kökü):**
   - Sabit sistem kuralları, agent kimliği, güvenlik kuralları ve `SKILL.md` yönergeleri en başta yer alır.
   - Asla dinamik tarih, milisaniye, rastgele ID veya PID içermez.
2. **Dinamik Geçici Kuyruk (Dynamic Ephemeral Tail):**
   - Anlık saat, çalışma dizini, son durum bilgileri sistem isteminden sökülür ve **yalnızca en son `user` turn'ünün sonuna** `[RUNTIME CONTEXT]` etiketiyle eklenir.

### 4.2. Deterministik Araç Sıralaması (RFC 8785)
- Tüm araç tanımları (`functionDeclarations`), fonksiyon adlarına göre alfabetik olarak sıralanır (`sort((a, b) => a.name.localeCompare(b.name))`).
- Parametre objeleri RFC 8785 Canonical JSON kurallarına göre deterministik anahtar sırasıyla serileştirilir.

### 4.3. DSH `systemPromptUpdate: 'in-history'` Uyarlaması
- DSH diyalog ortasında sistem istemini güncellediğinde, Token 0'daki ana sistem istemi kesinlikle ezilmez.
- Güncellenen direktif, diyalog akışında araya giren son `user` mesajına `[SYSTEM DIRECTIVE UPDATE]` bloğu olarak iliştirilir. Böylece geçmiş 20K+ tokenlık önbellek sıfırlanmaz.

---

## 5. ARAÇ İCRASI, SÜREÇ YÖNETİMİ VE GÜVENLİK KÖPRÜSÜ

### 5.1. Win32 Job Objects ile Süreç İzolasyonu (Zombie Process Mitigation)
- `denen DSH`'deki Win32 Job Object mimarisi uygulanır (`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`).
- Bir PowerShell veya terminal komutu iptal edildiğinde (`Ctrl+C` veya abort sinyali), komutun açtığı tüm alt süreçler (Chrome, Python, Node vb.) sistemde askıda kalmadan atomik olarak öldürülür.

### 5.2. Çift Kademeli Tamponlama (Pipe Stall Koruması)
- Soket takılmalarını önlemek için standart Node.js `exec` (5MB sınır) terk edilir.
- **64 KB RAM Ring-Buffer + 64 MB Disk Spill**: Komut çıktıları önce hızlı halka belleğe yazılır, eşik aşılırsa disk üzerindeki geçici dosyaya yönlendirilir.

### 5.3. Bariyer ve Eşzamanlı Havuz Zamanlayıcısı
- **Paralel Havuz (Read-Only):** `view_file`, `list_dir`, `grep_search`, `read_url_content` gibi yan etkisi olmayan araçlar eşzamanlı çalıştırılır.
- **Bariyer (Exclusive Barrier):** `run_command`, `write_to_file`, `replace_file_content` gibi sistem durumunu değiştiren araçlar kuyrukta tekil çalıştırılır; önceki okuma işlemlerinin bitmesi beklenir.
- Sonuçlar modele orijinal model çağrı sırasına göre (`model-ordered commit`) tek bir `contents` turunda teslim edilir.

### 5.4. Kendi Kendini Düzeltme (Self-Correction & Stderr Envelope)
- Bir komut hata verdiğinde (`exitCode !== 0`), adaptör soketi çökertmez veya 500 hatası basmaz.
- Hata çıktısı yapılandırılmış bir tanı zarfına sarılır:
  ```json
  {
    "exitCode": 1,
    "errorType": "COMMAND_EXECUTION_FAILURE",
    "stderr": "Cannot find path '...'",
    "hint": "Dizin mevcut değil, önce dizin varlığını kontrol etmeyi deneyin."
  }
  ```
- Bu çıktı `functionResponse` olarak modele döner ve modelin anında alternatif araç çağrısı üretmesi (Self-healing) sağlanır.

---

## 6. EFOR VE DÜŞÜNME (THINKING / REASONING) PİPELINE'I

OpenAI Responses API'dan gelen `reasoning.effort` parametresi ile Gemini iç motoru arasındaki harita:

```
OpenAI reasoning.effort
   │
   ├── "off"   ──────> Model: gemini-3.1-flash-lite (thinkingBudget = null)
   ├── "low"   ──────> Model: gemini-3.8-flash-low  (thinkingBudget = 1000)
   ├── "medium" ─────> Model: gemini-3.8-flash-tiered (thinkingBudget = -1)
   └── "high" / "max" > Model: gemini-3.8-flash-high (thinkingBudget = -1, maxOutput: 65536)
```

Akış sırasında:
1. `thought: true` etiketli parçalar `response.reasoning_text.delta` olarak iletilir.
2. `thoughtSignature` yakalanır ve hafızada tutulur.
3. Asıl kullanıcı yanıtı `response.output_text.delta` olarak iletilir.
4. Böylece kullanıcının terminalinde düşünce süreci ve nihai yanıt birbirine karışmadan şeffaf şekilde akar.

---

## 7. ADIM ADIM UYGULAMA VE GELİŞTİRME FAZLARI

Aşağıdaki 5 faz sırasıyla icra edilecektir:

```
┌────────────────────────────────────────────────────────────────────────┐
│ FAZ 1: ALTYAPI VE PROXY ÇEKİRDEĞİ                                      │
│ - Express + Native WebSocket + HTTP/2 Upstream İstemcisi Kurulumu       │
│ - POST /v1/responses Uç Noktasının Açılması                            │
│ - Gelen OpenAI İstek Gövdesi Doğrulama Katmanı                         │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│ FAZ 2: KANONİK ÖN BELLEK VE ŞEMA DÖNÜŞTÜRÜCÜ                          │
│ - RFC 8785 Deterministik Tool Sıralayıcı                               │
│ - Two-Tier Prompt Layout (Statik Token 0 vs Dinamik Kuyruk)            │
│ - OpenAI Tool -> Gemini functionDeclarations Kayıpsız Transpiler       │
│ - Polimorfik input -> AIP-136 contents Rol Çevirici                    │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│ FAZ 3: ÇİFT YÖNLÜ KESİNTİSİZ AKIŞ VE SSE TRANSLATOR                    │
│ - Gemini SSE Olay Ayrıştırıcısı (thought, thoughtSignature, Call, Text)│
│ - OpenAI Responses SSE Olay Üreticisi (created, delta, completed)      │
│ - usageMetadata -> cached_tokens Token Muhasebecisi                    │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│ FAZ 4: YÜKSEK PERFORMANSLI ARAÇ VE SÜREÇ MOTORU                        │
│ - Win32 Job Objects Süreç Ağacı İzolasyonu                             │
│ - 64KB RAM + 64MB Disk Spill Çift Kademeli Tampon                      │
│ - Bariyer & Paralel Araç Yürütme Zamanlayıcısı                         │
│ - Self-Correction Hata Zarfı Entegrasyonu                             │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│ FAZ 5: ENTEGRASYON VE DOĞRULAMA TESTLERİ                               │
│ - DSH üzerinden Canlı İstek Gönderimi                                  │
│ - %90+ Cache Hit Doğrulaması (cachedContentTokenCount Kontrolü)         │
│ - Çoklu Paralel Araç Çağrısı ve Alt Ajan Orkestrasyon Testi            │
│ - Stres ve Timeout Dayanıklılık Sınaması                               │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 8. SONUÇ VE HAZIRLIK BEYANI

Bu ana plan; `denen DSH`, `harness`, `agy` ve Google CloudCode protokollerinin kaynak kod düzeyinde incelenmesiyle hazırlanmıştır. İhtiyaç duyulan tüm şemalar, mimari kurallar ve veri yapıları netleşmiştir.

Kullanıcının onayıyla birlikte doğrudan **Faz 1: Adaptör Çekirdeğinin Kodlanması** adımına geçilebilir.
