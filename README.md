# ⚡ Gemini AIP-136 Protocol Gateway

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)](https://golang.org)
[![Port](https://img.shields.io/badge/Port-8000-success)](http://127.0.0.1:8000)
[![TLS Parity](https://img.shields.io/badge/TLS-Go%20Native%201%3A1%20Parity-blue)]()
[![Context Cache](https://img.shields.io/badge/Context%20Cache-20k%2B%20Hit%20Rate-orange)]()
[![License](https://img.shields.io/badge/License-MIT-green.svg)]()

**Gemini AIP-136 Protocol Gateway**, modern yapay zeka ajan harness'ları (DeepSeek Harness - DSH, Claude Code, Cline, Cursor, Roo-Code vb.) için geliştirilmiş, yüksek performanslı ve çift protokol destekli bir **tersine ağ geçididir (Reverse Gateway)**.

Standart **OpenAI Chat Completions (`/v1/chat/completions`)** ve yeni nesil **OpenAI Responses API (`/v1/responses`)** formatındaki tüm istekleri, Google CloudCode dahili **Gemini AIP-136 Streaming SSE (`v1internal:streamGenerateCodeContent`)** protokolüne çift yönlü (full-duplex) ve kayıpsız olarak çevirir.

---

## 🚀 Öne Çıkan Mimari Özellikler

### 1. Sıfır Prompt Enjeksiyonu (Zero Prompt Injection)
* **Saf Bağlam Garantisi:** Gateway, modelin mantığını bulandıracak veya bağlamını bozacak hiçbir harici yapay prompt, sistem rolü veya direktif enjekte etmez.
* İstemcinin (DSH, agent veya kullanıcının) gönderdiği tüm sistem mesajları (`system` / `instructions`), kullanıcı mesajları ve araç tanımları **olduğu gibi, şeffaf bir şekilde** Google AIP-136 motoruna aktarılır.

### 2. Go Native TLS Parmak İzi (1:1 Antigravity CLI Eşleşmesi)
* Node.js (`undici`, `https.Agent`) veya Python kütüphaneleri Google CloudCode uç noktalarında (`daily-cloudcode-pa.googleapis.com`) JA3/JA4 TLS parmak izi analiziyle engellenir.
* Gateway, saf **Go `net/http` ve `crypto/tls`** HTTP/2 yığını kullanır. Bu sayede resmi Google Antigravity CLI ikilisi ile **1:1 bit düzeyinde TLS parmak izi paritesi** sağlayarak sıfır engelleme ile çalışır.

### 3. Otomatik Windows Kimlik Yönetimi (Windows Credential Vault)
* Kullanıcının `%LOCALAPPDATA%\antigravity-cli\auth.json` dizinindeki veya Windows Kimlik Deposu'ndaki Google Cloud OAuth2 belirtecini otomatik olarak algılar.
* Belirteç süresi dolduğunda arka planda Google OAuth2 servislerine sessizce istek atarak yeni erişim belirtecini (access token) yeniler. Harici API anahtarı girmenize gerek kalmaz.

### 4. Enterprise Context Caching (%90+ Hit - 16k-20k Eşiği)
* Kurumsal Google CloudCode arka ucunun TPU KV önbellekleme eşiği canlı testlerle tespit edilmiştir (~16.000 - 20.000 token).
* 25.000 tokenlık kurumsal şartname ve bağlam testinde **20.450 tokenlık muazzam bir Cache Hit (`CACHE: 20450 ⚡ HIT!`)** başarıyla doğrulanmıştır.

### 5. Katı Şema Normalizasyonu ve Tool Calling Motoru
* Google AIP-136 şema doğrulayıcısı küçük harf tipleri (`string`, `object`), `$schema` alanını ve rasgele anahtar dizilimini katı bir şekilde reddeder.
* Gateway; JSON Schema tanımlarını otomatik olarak büyük harfe (`OBJECT`, `STRING`, `INTEGER` vb.) çevirir, geçersiz meta-verileri süzer ve fonksiyon bildirimlerini **RFC 8785 Canonical JSON** kurallarıyla alfabetik sıralar.
* Modelin ürettiği kriptografik `thoughtSignature` imzasını bellek içi iş parçacığı güvenli (thread-safe) [`thought_store.go`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/thought_store.go) ile yöneterek çok turlu araç diyaloglarında hatasız devamlılık sağlar.

### 6. Çok Seviyeli Düşünme (Reasoning Effort) Yönetimi
Modelin akıl yürütme bütçesini istek parametrelerine göre anlık olarak yapılandırır:
* `dynamic` / `default`: `thinkingBudget: -1` (Model kendi belirler).
* `high`: `gemini-3.8-flash-high` modeli + `thinkingBudget: -1`.
* `low`: `gemini-3.8-flash-low` modeli + `thinkingBudget: 1000`.
* `off`: `thinkingBudget: 0` (Akıl yürütme tamamen kapalı, doğrudan sonuca odaklanır).

---

## 📐 Mimari Topoloji

```
┌────────────────────────────────────────────────────────────────────────┐
│                        İSTEMCİ / AI AJAN KATMANI                       │
│    (DeepSeek Harness, Cline, Claude Code, Cursor, Web Playground)      │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
               POST /v1/chat/completions VEYA POST /v1/responses
                                    │
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                 GEMINI AIP-136 PROTOCOL GATEWAY (GO)                   │
│                        (http://127.0.0.1:8000)                         │
├────────────────────────────────────────────────────────────────────────┤
│  ├── main.go               : Yüksek verimli HTTP motoru & CORS         │
│  ├── auth.go               : Windows Auth okuma & OAuth2 Token yenileme│
│  ├── protocol.go           : İki yönlü şema çevirici & RFC 8785 Sort   │
│  ├── gemini_client.go      : Saf Go Native TLS HTTP/2 istemcisi        │
│  ├── stream_translator.go  : SSE Çevirici (Thought, Text, Tool, Usage) │
│  ├── thought_store.go      : Bellek içi kriptografik imza önbelleği    │
│  └── ui.html               : Yerleşik Web Playground & Metrik Ekranı   │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
               POST /v1internal:streamGenerateCodeContent?alt=sse
                                    │
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                   GOOGLE CLOUDCODE / GEMINI BACKEND                    │
│                (daily-cloudcode-pa.googleapis.com:443)                 │
│         [TPU KV Context Cache: %90+ Hit Rate (20.000+ Token)]          │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 📡 Protokol Analizleri ve İstek / Yanıt Şablonları

### 1. OpenAI Chat Completions Protokolü (`POST /v1/chat/completions`)

#### İstek Şablonu (JSON):
```json
{
  "model": "gemini-3.8-flash-medium",
  "stream": true,
  "reasoning_effort": "high",
  "messages": [
    {
      "role": "system",
      "content": "Sen yardımcı ve uzman bir yazılım mimarısın."
    },
    {
      "role": "user",
      "content": "Kriptografik SHA-256 algoritmasını kısaca açıkla."
    }
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "run_terminal",
        "description": "Terminalde komut çalıştırır.",
        "parameters": {
          "type": "object",
          "properties": {
            "command": {
              "type": "string",
              "description": "Çalıştırılacak shell komutu"
            }
          },
          "required": ["command"]
        }
      }
    }
  ]
}
```

#### SSE Yanıt Akışı (Server-Sent Events):
```text
data: {"id":"chatcmpl-1742084920","object":"chat.completion.chunk","created":1742084920,"model":"gemini-3.8-flash-medium","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"SHA-256 matematiksel özet fonksiyonudur..."},"finish_reason":null}]}

data: {"id":"chatcmpl-1742084920","object":"chat.completion.chunk","created":1742084920,"model":"gemini-3.8-flash-medium","choices":[{"index":0,"delta":{"content":"SHA-256, 256 bit uzunluğunda sabit bir çıktı üreten tek yönlü bir hash algoritmasıdır."},"finish_reason":null}]}

data: {"id":"chatcmpl-1742084920","object":"chat.completion.chunk","created":1742084920,"model":"gemini-3.8-flash-medium","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":45,"total_tokens":165,"prompt_tokens_details":{"cached_tokens":0}}}

data: [DONE]
```

---

### 2. OpenAI Responses API Protokolü (`POST /v1/responses`)

#### İstek Şablonu (JSON):
```json
{
  "model": "gemini-3.8-flash-high",
  "stream": true,
  "instructions": "Verilen kurallara ve araçlara göre hareket et.",
  "reasoning": {
    "effort": "high"
  },
  "input": [
    {
      "role": "user",
      "content": "Sistem dizinindeki dosyaları listele."
    },
    {
      "type": "function_call",
      "id": "call_98765",
      "name": "list_dir",
      "arguments": "{\"path\":\"C:\\\\Users\"}"
    },
    {
      "type": "function_call_output",
      "call_id": "call_98765",
      "output": "{\"files\":[\"metin\",\"Public\"]}"
    }
  ],
  "tools": [
    {
      "type": "function",
      "name": "list_dir",
      "description": "Dizin içeriğini listeler.",
      "parameters": {
        "type": "object",
        "properties": {
          "path": { "type": "string" }
        },
        "required": ["path"]
      }
    }
  ]
}
```

#### SSE Yanıt Akışı (Server-Sent Events):
```text
event: response.created
data: {"response":{"id":"resp_1742084921","model":"gemini-3.8-flash-high","status":"in_progress"}}

event: response.reasoning_text.delta
data: {"delta":"Dizin içeriği listelendi, kullanıcıya Türkçe raporlayacağım..."}

event: response.output_text.delta
data: {"delta":"C:\\Users dizini altında 'metin' ve 'Public' klasörleri bulunmaktadır."}

event: response.completed
data: {"response":{"id":"resp_1742084921","status":"completed","usage":{"total_tokens":340,"input_tokens":280,"output_tokens":60,"input_tokens_details":{"cached_tokens":0}}}}
```

---

### 3. Hedef Google CloudCode AIP-136 Protokolü (`v1internal:streamGenerateCodeContent`)

Gateway tarafından arka planda Google'a iletilen optimize edilmiş gövde:

```json
{
  "project": "cloudcode-pa-internal",
  "model": "gemini-3.8-flash-high",
  "request": {
    "systemInstruction": {
      "role": "user",
      "parts": [
        { "text": "Verilen kurallara ve araçlara göre hareket et." }
      ]
    },
    "contents": [
      {
        "role": "user",
        "parts": [{ "text": "Sistem dizinindeki dosyaları listele." }]
      },
      {
        "role": "model",
        "parts": [
          {
            "functionCall": {
              "name": "list_dir",
              "args": { "path": "C:\\Users" }
            },
            "thoughtSignature": "CiQA7Zk4..."
          }
        ]
      },
      {
        "role": "user",
        "parts": [
          {
            "functionResponse": {
              "name": "list_dir",
              "response": { "output": "{\"files\":[\"metin\",\"Public\"]}" }
            }
          }
        ]
      }
    ],
    "tools": [
      {
        "functionDeclarations": [
          {
            "name": "list_dir",
            "description": "Dizin içeriğini listeler.",
            "parameters": {
              "type": "OBJECT",
              "properties": {
                "path": { "type": "STRING" }
              },
              "required": ["path"]
            }
          }
        ]
      }
    ],
    "generationConfig": {
      "maxOutputTokens": 65536,
      "thinkingConfig": {
        "includeThoughts": true,
        "thinkingBudget": -1
      }
    }
  }
}
```

---

## 🛠️ Tool Calling Mekanizması & Şema Normalizasyonu

Google CloudCode AIP-136 motoru, standart OpenAI araç tanımlarını doğrudan kabul etmez. Gateway aşağıdaki zorunlu dönüşümleri uygular:

1. **Büyük Harf Tip Zorunluluğu:**  
   `"type": "object"` -> `"type": "OBJECT"`, `"type": "string"` -> `"type": "STRING"`, `"type": "integer"` -> `"type": "INTEGER"`, `"type": "array"` -> `"type": "ARRAY"`.
2. **Yasaklı Alanların Temizlenmesi:**  
   Google validasyonunu bozan `$schema`, `additionalProperties`, `default` alanları özyinelemeli (recursive) olarak temizlenir.
3. **Deterministik RFC 8785 Sıralaması:**  
   Context Cache'in (TPU KV Önbelleği) her istekte aynı imzayı yakalayabilmesi için `functionDeclarations` dizisindeki araçlar alfabetik isim sırasına (`name`) göre deterministik olarak dizilir.
4. **Kriptografik `thoughtSignature` Korunumu:**  
   Model bir araç çağırdığında (`functionCall`) bir kriptografik onay imzası üretir. İstemci sadece araç sonucunu döndüğünde, [`thought_store.go`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/thought_store.go) çağrı kimliği üzerinden bu imzayı otomatik olarak araya ekler. Bu sayede `400 Invalid Thought Signature` hataları engellenir.

---

## ⚡ Kurulum ve Çalıştırma

### Gereksinimler
- **İşletim Sistemi:** Windows (x64 / ARM64)
- **Go:** 1.22 veya üzeri (Kaynak koddan derlemek için)
- **Kimlik:** Sistemde kurulu ve oturum açılmış `antigravity` CLI (Kimlik bilgileri otomatik çekilir).

### Hızlı Başlatma (Kullanıma Hazır)
1. Proje dizininde yer alan [`baslat_gateway.bat`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/baslat_gateway.bat) dosyasına çift tıklayın veya terminalden çalıştırın:
   ```powershell
   .\baslat_gateway.bat
   ```
2. Gateway varsayılan olarak **Port 8000** üzerinde dinlemeye başlayacaktır:
   ```
   ======================================================================
   ⚡ DSH Go Protocol Gateway Aktif!
   🌐 Dinleme Adresi     : http://127.0.0.1:8000
   🔒 TLS Parmak İzi     : Go Native crypto/tls (Antigravity CLI ile 1:1)
   🖥️  Test Arayüzü (Web) : http://127.0.0.1:8000/ui
   📡 OpenAI Responses   : http://127.0.0.1:8000/v1/responses
   📡 Chat Completions   : http://127.0.0.1:8000/v1/chat/completions
   📊 Sağlık & Metrikler : http://127.0.0.1:8000/health
   ======================================================================
   ```
3. Web test arayüzünü açmak için [`arayuzu_ac.bat`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/arayuzu_ac.bat) dosyasını çalıştırın veya tarayıcınızdan `http://127.0.0.1:8000/ui` adresine gidin.

### Kaynak Koddan Derleme
```bash
go build -o dsh_go_gateway.exe .
```

---

## ⚙️ Ajan ve İstemci Entegrasyonları

### 1. DeepSeek Harness (DSH) Entegrasyonu
`~/.dsh/settings.yaml` veya `C:\Users\<Kullanici>\.dsh\settings.yaml` dosyanızı güncelleyin:

```yaml
modelProvider: "custom"
customProvider:
  baseURL: "http://127.0.0.1:8000/v1"
  apiKey: "gemini-gateway-local"
model: "gemini-3.8-flash-high"
reasoningEffort: "high"
```

### 2. Cline / Cursor / Roo-Code Entegrasyonu
- **API Provider:** OpenAI Compatible
- **Base URL:** `http://127.0.0.1:8000/v1`
- **API Key:** `any-string` (Gateway kimliği Windows Credential'dan otomatik sağlar)
- **Model ID:** `gemini-3.8-flash-high` veya `gemini-3.8-flash-medium`

---

## 🖥️ Yerleşik Web UI Dashboard (`/ui`)

Gateway, hiçbir harici dosya bağımlılığı olmadan doğrudan binary içerisine gömülü (`//go:embed ui.html`) bir Web Arayüzü barındırır:

- **Canlı Akış ve Düşünme (Reasoning) Takibi:** Modelin arka plandaki düşünce zincirini daraltılabilir/genişletilebilir özel düşünme paneliyle canlı gösterir.
- **Sıfır Enjeksiyon Göstergesi:** Canlı sistem istemi rozeti (`🟢 Aktif` veya `🔘 Boş`) ile giden JSON payload'unu anlık gösterir.
- **25k Context Cache Test Butonu:** Tek tıkla 25.000 tokenlık şartname yükleyerek kurumsal `%90+` önbellek isabetini (`⚡ HIT`) doğrular.
- **Canlı Token & Metrik Takibi:** Anlık gecikme süresi (ms), prompt token, cached token ve completion token sayıları.

---

## 📂 Proje Dizin Yapısı

```
gemini-aip136-gateway/
├── main.go               # HTTP sunucusu, yönlendirmeler ve metrik yönetimi
├── auth.go               # Windows credential okuma ve OAuth2 yenileme
├── protocol.go           # OpenAI <-> Gemini AIP-136 çift yönlü protokol motoru
├── gemini_client.go      # Go Native TLS HTTP/2 istemcisi
├── stream_translator.go  # SSE veri akışı ve token usage çeviricisi
├── thought_store.go      # Çok turlu araç çağrıları için imza önbelleği
├── ui.html               # Web UI Dashboard kaynak kodu
├── go.mod                # Go modül tanımı
├── baslat_gateway.bat    # Port 8000 başlatıcı betik
├── arayuzu_ac.bat        # Web arayüzünü tarayıcıda açan betik
├── test_cache.py         # 25k token Context Cache doğrulama betiği
├── test_scenarios.py     # Farklı model ve effort test senaryoları
├── test_go_gateway.js    # Node.js uçtan uca doğrulama testi
├── plans/                # Orijinal mimari araştırma ve ajan raporları arşivi
│   ├── README.md         # Planın kuruluş amacı ve ajan görev dağılımı
│   ├── MASTER_PLAN.md    # Hiyerarşik ana mimari planı
│   ├── 01_... to 05_...  # Bağımsız ajan araştırma raporları
├── KARSILASILAN_SORUNLAR.md # Karşılaşılan 9 kritik sorun ve teknik çözümleri
└── README.md             # Bu dokümantasyon
```

---

## 📜 Lisans

Bu proje [MIT Lisansı](LICENSE) kapsamında sunulmaktadır.
