# 📋 Gemini AIP-136 Protocol Gateway - Mimari Planlama Arşivi

Bu dizin, **Gemini AIP-136 Protocol Gateway** projesinin geliştirilme sürecinde oluşturulan kapsamlı araştırma raporlarını, tersine mühendislik bulgularını ve hiyerarşik ana planı (Master Plan) içermektedir.

> [!IMPORTANT]
> Bu dizindeki plan dosyaları (`01_...`, `02_...`, `MASTER_PLAN.md` vb.) projenin tarihsel tasarım belgeleridir ve **özgün halleriyle korunmuştur**. Herhangi bir modifikasyona uğramamışlardır.

---

## 🎯 Planın Kuruluş Amacı

Bu planlama sürecinin temel amaçları şunlardır:

1. **İki Farklı Dünya Arasında Kayıpsız Köprü Kurmak:**  
   Gelişmiş AI ajan platformlarının (özellikle DeepSeek Harness - DSH, Cline, Claude Code, Cursor vb.) kullandığı endüstri standardı **OpenAI Wire Formatlarını** (`/v1/chat/completions` ve `/v1/responses`), Google'ın yeni nesil **Gemini AIP-136 / CloudCode dahili motoruna** (`daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateCodeContent`) bağlamak.

2. **TLS Parmak İzi Engellerini Aşmak:**  
   Node.js, Python veya standart cURL kütüphanelerinin Google CloudCode sunucuları tarafından JA3/JA4 TLS parmak izi analiziyle engellenmesi sorununu tespit etmek ve resmi Antigravity CLI ile birebir (1:1) TLS parmak izi eşleşmesi sunan saf bir motor tasarlamak.

3. **Sıfır Prompt Injection (Zero Prompt Injection) Garantisi:**  
   Araya harici yapay sistem istemleri veya kurallar sokmadan, istemcinin (DSH/kullanıcı) özgün bağlamını, araç tanımlarını ve niyetini modele %100 saf ve şeffaf olarak iletmek.

4. **Kurumsal TPU Context Cache Optimizasyonu (%90+ Hit Rate):**  
   Google Gemini'nin kurumsal KV önbellek mekanizmasını maksimize etmek için iki katmanlı istem mimarisi (Two-Tier Prompt Layout) ve RFC 8785 Canonical JSON deterministik araç sıralaması tasarlamak.

5. **Kusursuz Ajan Yetenekleri & Araç İcrası (Tool Execution):**  
   Çok turlu konuşmalarda modelin ürettiği `thoughtSignature` imzasını kaybetmeden yöneten ve Google AIP-136'nın katı JSON Schema gereksinimlerini karşılayan sağlam bir şema normalizasyon altyapısı kurmak.

---

## 👥 Görevlendirilen Ajanlar ve Sorumluluk Alanları

Planlama aşamasında her bir kritik teknik zorluk için uzmanlaşmış bağımsız ajanlar görevlendirilmiş ve elde edilen raporlar sentezlenmiştir:

```
┌────────────────────────────────────────────────────────────────────────┐
│                          BAŞ MİMAR (SYNTHESIZER)                       │
│                        [MASTER_PLAN.md Derleyicisi]                    │
└───────┬───────────────────┬───────────────────┬───────────────────┬────┘
        │                   │                   │                   │
        ▼                   ▼                   ▼                   ▼
┌───────────────┐   ┌───────────────┐   ┌───────────────┐   ┌───────────────┐
│    AJAN 1     │   │    AJAN 2     │   │    AJAN 3     │   │    AJAN 4     │
│ DSH & OpenAI  │   │ Google AIP136 │   │ Context Cache │   │ Tool Engine & │
│ Responses     │   │ Protokol      │   │ Yönlendirici  │   │ Ajan Süreç    │
│ Protokolü     │   │ Uzmanı        │   │ Mimarı        │   │ İzolasyonu    │
└───────┬───────┘   └───────┬───────┘   └───────┬───────┘   └───────┬───────┘
        │                   │                   │                   │
        ▼                   ▼                   ▼                   ▼
  [01_DSH_...]        [02_GEMINI_...]     [03_DETERM_...]     [04_TOOL_...]
```

### 1. Ajan 1: DSH & OpenAI Responses Protokol Analisti
* **Hazırlanan Rapor:** [`01_DSH_OPENAI_RESPONSES_PROTOCOL.md`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/plans/01_DSH_OPENAI_RESPONSES_PROTOCOL.md)
* **Görevi:**
  - DeepSeek Harness (`denen DSH`) projesinin `llm-pi-ai` paketini ve `OpenAIResponsesProvider` modülünü incelemek.
  - `/v1/responses` uç noktasının HTTP gövde yapısını, `input` elemanlarını (`message`, `function_call`, `function_call_output`) ve SSE olay akışını (`response.created`, `response.reasoning_text.delta`, `response.output_text.delta`, `response.completed`) eksiksiz haritalamak.
  - DSH'nin çalışma zamanı dinamik sistem istemi güncelleme stratejisini (`systemPromptUpdate: 'in-history'`) belirlemek.

### 2. Ajan 2: Google CloudCode AIP-136 Protokol Uzmanı
* **Hazırlanan Rapor:** [`02_GEMINI_AIP136_PROTOCOL.md`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/plans/02_GEMINI_AIP136_PROTOCOL.md)
* **Görevi:**
  - `daily-cloudcode-pa.googleapis.com` arka uç servisinin dahili `v1internal:streamGenerateCodeContent` SSE uç noktasını incelemek.
  - Google AIP-136 `request.contents`, `parts`, `thought: true`, `thoughtSignature` ve `functionCall` veri yapısını deşifre etmek.
  - Katı şema doğrulama kurallarını (Büyük harf `OBJECT`, `STRING`, `INTEGER` tipleri, `$schema` reddi, deterministik alan sıralaması) belgelemek.

### 3. Ajan 3: Deterministik Context Cache Yönlendirici Mimarı
* **Hazırlanan Rapor:** [`03_DETERMINISTIC_CACHE_ROUTER.md`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/plans/03_DETERMINISTIC_CACHE_ROUTER.md)
* **Görevi:**
  - Google Gemini kurumsal TPU KV önbellek motorunun bozulmadan çalışması için **Strict Prefix Invariance (Katı Ön Ek Değişmezliği)** mimarisini kurgulamak.
  - İki katmanlı istem mimarisi (Two-Tier Prompt Layout: Statik Token 0 Çapası ve Dinamik Kuyruk) tasarımını oluşturmak.
  - Araç tanımlarının deterministik RFC 8785 kurallarıyla alfabetik sıralanmasını zorunlu kılan kuralları belirlemek.

### 4. Ajan 4: Tool Engine & Ajan Yetenekleri Mühendisi
* **Hazırlanan Rapor:** [`04_TOOL_ENGINE_AGENT_CAPABILITIES.md`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/plans/04_TOOL_ENGINE_AGENT_CAPABILITIES.md)
* **Görevi:**
  - Windows işletim sisteminde çalışan ajanların açtığı terminal ve komut süreçlerini `Win32 Job Objects` (`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`) ile zombi süreç kalmayacak şekilde izole etmek.
  - 64 KB RAM Ring-Buffer ve 64 MB Disk Spill çift kademeli tamponlama mimarisini tasarlamak.
  - Okuma (paralel) ve yazma (bariyer) araçlarının eşzamanlı icra planını ve hata durumunda modelin kendi kendini onarmasını sağlayan tanı zarfı (Self-Healing Stderr Envelope) sistemini kurgulamak.

### 5. Ajan 5: Antigravity Sistem İstemi ve Araç Kuralları Analisti
* **Hazırlanan Rapor:** [`05_AGY_SYSTEM_PROMPT_AND_TOOL_RULES.md`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/plans/05_AGY_SYSTEM_PROMPT_AND_TOOL_RULES.md)
* **Görevi:**
  - Canlı ağ dinleyicisi (Sniffer) ile resmi Antigravity CLI trafiğini incelemek.
  - Giden sistem istemini (`extracted_system_instruction.txt`), orijinal JSON şemalarını (`raw_system_instruction.json`) ve resmi araç tanımlarını (`raw_tools.json`) ham olarak yakalamak.
  - Modelin muhakeme (reasoning) eforlarını (`thinkingBudget`, tiered model isimleri) analiz etmek.

---

## 📁 Planlama Arşivi Dosya Fihristi

| Dosya | Açıklama |
| :--- | :--- |
| [`MASTER_PLAN.md`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/plans/MASTER_PLAN.md) | **Tüm araştırmaların sentezlendiği Hiyerarşik Ana Uygulama Planı.** Sistem mimarisi, dönüşüm matrisleri, tamponlama ve efor boru hatları. |
| [`01_DSH_OPENAI_RESPONSES_PROTOCOL.md`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/plans/01_DSH_OPENAI_RESPONSES_PROTOCOL.md) | DeepSeek Harness / OpenAI Responses API detaylı protokol analizi. |
| [`02_GEMINI_AIP136_PROTOCOL.md`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/plans/02_GEMINI_AIP136_PROTOCOL.md) | Google CloudCode AIP-136 SSE streaming protokolü analizi. |
| [`03_DETERMINISTIC_CACHE_ROUTER.md`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/plans/03_DETERMINISTIC_CACHE_ROUTER.md) | Deterministik KV önbellek optimizasyonu ve ön ek değişmezlik kuralları. |
| [`04_TOOL_ENGINE_AGENT_CAPABILITIES.md`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/plans/04_TOOL_ENGINE_AGENT_CAPABILITIES.md) | Win32 süreç yönetimi, ring buffer ve ajan hata yönetim mimarisi. |
| [`05_AGY_SYSTEM_PROMPT_AND_TOOL_RULES.md`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/plans/05_AGY_SYSTEM_PROMPT_AND_TOOL_RULES.md) | Antigravity CLI yakalanan sistem direktifleri ve araç kuralları. |
| [`extracted_system_instruction.txt`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/plans/extracted_system_instruction.txt) | Sniffer ile yakalanan ham sistem talimatı metni. |
| [`raw_system_instruction.json`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/plans/raw_system_instruction.json) | Ham sistem talimatı JSON paketi. |
| [`raw_tools.json`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/plans/raw_tools.json) | Sniffer ile yakalanan resmi Google AIP-136 fonksiyon bildirimleri listesi. |

Bu planlar doğrultusunda geliştirilen nihai Go gateway çekirdeği, kök dizindeki [`main.go`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/main.go) ve ilişkili modüller altında derlenerek üretime hazır hale getirilmiştir.
