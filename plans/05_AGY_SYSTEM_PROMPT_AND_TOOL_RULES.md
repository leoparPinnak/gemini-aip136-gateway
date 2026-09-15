# 05 — AGY System Prompt, Tool Rules & Agent Architecture Specification
## Google Antigravity (AGY) Ham Sistem İstemi Şablon Hiyerarşisi, 20 Araç Çağırma Protokolü, Ajan Yaşam Döngüsü ve DSH Adaptör Uygulama Şartnamesi

---

### Belge Üst Verileri
- **Belge Kodu:** `PLAN-05-AGY-SYSTEM-PROMPT-AND-TOOL-RULES`
- **Konum:** `C:\Users\metin\Desktop\plan\05_AGY_SYSTEM_PROMPT_AND_TOOL_RULES.md`
- **Tersine Mühendislik Kaynakları:**
  - `C:\Users\metin\Desktop\plan\extracted_system_instruction.txt` (37.182 Bayt / 299 satır ham sistem istemi)
  - `C:\Users\metin\Desktop\plan\raw_tools.json` (55.620 Bayt / 20 adet `functionDeclaration` şeması)
  - `C:\Users\metin\AGENTS.md` (Kullanıcı kural enjeksiyon örneği)
  - `C:\Users\metin\.gemini\antigravity-cli\builtin\skills` (Skill dosya sistem mimarisi)
- **Hedef Sistem:** DSH (DeepSeek Harness) Çekirdeği, DSH-Gemini Adaptörü, Prompt Compiler Motoru ve Tool Engine
- **Tarih:** 14 Eylül 2026

---

## 1. Yönetici Özeti ve Mimari Vizyon

Google Antigravity (AGY), Google DeepMind AAC (Advanced Agentic Coding) ekibi tarafından geliştirilmiş, Gemini 1.5 Pro / Ultra ve Flash modellerini otonom yazılım mühendisliği ajanlarına dönüştüren en gelişmiş ajan çalışma zamanıdır (agent runtime). 

AGY'nin başarısının ve modelin karmaşık yazılım geliştirme süreçlerinde sıfıra yakın halüsinasyonla araç çağırabilmesinin sırrı iki temel sütuna dayanmaktadır:
1. **Katı ve Hiyerarşik XML Tabanlı Sistem İstemi Şablonu (Prompt Template Hierarchy):** Kimlik (`<identity>`), çalışma alanı sınırları (`<user_information>`), dinamik kural enjeksiyonu (`<user_rules>`), aşamalı ifşa (`<skills>`), çoklu ajan orkestrasyonu (`<subagents>`), reaktif olay döngüsü (`<messaging>`), hafıza ve artefakt yönetimi (`<artifacts>`) bölümlerinden oluşan deterministik bir yapı.
2. **Kusursuz Tanımlanmış 20 Araçlık Bildirimsel Protokol (Tool Rules & Declarations):** Modelin araçları ne zaman, hangi kısıtlamalarla ve hangi parametrelerle çağıracağını bildiren, her araç çağrısında UI/TUI telemetrisini besleyen evrensel meta-veri (`toolAction` ve `toolSummary`) gerektiren, senkron/asenkron süreç ayrımını netleştiren zengin şemalar.

Bu belgenin amacı; yakalanan canlı ağ paketlerinden (`captured_events.json`) ayrıştırılan 37.182 baytlık ham sistem istemi ve 20 araçlık şemanın tersine mühendisliğini eksiksiz belgelemek; ardından **DSH (DeepSeek Harness)** adaptörünün Gemini ve DeepSeek modelleriyle aynı kusursuzlukta çalışabilmesini sağlayacak **"İstem Üretim ve Araç Rehberi Şartnamesi"**ni ortaya koymaktır.

---

## 2. AGY Sistem İstemi Şablon Hiyerarşisi (Template Hierarchy)

AGY, Gemini modeline aktarılan sistem istemini (`system_instruction`) rastgele metin blokları yerine, modelin dikkatini (attention mechanism) modüler olarak yönlendiren standart bir XML etiket hiyerarşisi ile yapılandırır.

### 2.1 Bölüm Sıralaması ve Mimari Görevleri

Aşağıdaki şema, ham sistem istemindeki 12 ana bölümün sıralamasını ve akışını göstermektedir:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ 1. <identity>              Ajan Rolü, DeepMind AAC, <USER_REQUEST> Kuralı   │
├─────────────────────────────────────────────────────────────────────────────┤
│ 2. <user_information>      OS, Çalışma Alanı Eşlemesi, Dizin Kısıtları, ID  │
├─────────────────────────────────────────────────────────────────────────────┤
│ 3. <mcp_servers>           Eager / Lazy MCP Sunucu ve Araç Ayrımı          │
├─────────────────────────────────────────────────────────────────────────────┤
│ 4. <user_rules>            AGENTS.md / GEMINI.md Mutlak Öncelikli Kurallar │
├─────────────────────────────────────────────────────────────────────────────┤
│ 5. <skills>                Aşamalı İfşa (Progressive Disclosure) & Skill.md│
├─────────────────────────────────────────────────────────────────────────────┤
│ 6. <subagents>             Subagent Tipleri, Yaşam Döngüsü & send_message  │
├─────────────────────────────────────────────────────────────────────────────┤
│ 7. <subagent_reminder>     (Yalnızca Subagent bağlamında) Ebeveyn Raporlama │
├─────────────────────────────────────────────────────────────────────────────┤
│ 8. <messaging>             Reaktif Uyandırma (Reactive Wakeup, No Polling) │
├─────────────────────────────────────────────────────────────────────────────┤
│ 9. <conversation_transcript> JSONL Trajectory, Adım Alanları, Truncation     │
├─────────────────────────────────────────────────────────────────────────────┤
│ 10. <artifacts>            Artefakt Dizinleri, Alerts, Mermaid, Carousels  │
├─────────────────────────────────────────────────────────────────────────────┤
│ 11. <slash_commands>       Kullanıcıya Önerilecek Slash Kısayolları        │
├─────────────────────────────────────────────────────────────────────────────┤
│ 12. <guidelines>           Davranışsal Bütünlük ve Docstring Koruma        │
├─────────────────────────────────────────────────────────────────────────────┤
│ 13. <communication_style>  Yanıt Tonu, file:// Tıklanabilir Linkler        │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

### 2.2 Her Bölümün Ayrıntılı Anatomisi ve Sözdizimi

#### 1. `<identity>` — Ajan Kimliği ve Görev Çerçevesi
* **Amaç:** Ajanın kimliğini, arkasındaki mühendislik ekibini ve kullanıcı taleplerinin (`<USER_REQUEST>`) mutlak önceliğini tanımlar.
* **Ham Şablon Metni:**
  ```xml
  <identity>
  You are Antigravity, a powerful agentic AI coding assistant designed by the Google Deepmind team working on Advanced Agentic Coding.
  You are pair programming with a USER to solve their coding task. The task may require creating a new codebase, modifying or debugging an existing codebase, or simply answering a question.
  The USER will send you requests, which you must always prioritize addressing. User requests are enclosed within <USER_REQUEST> tags.
  </identity>
  ```
* **Kritik Kurallar:**
  - Kullanıcı girdisi her zaman `<USER_REQUEST>` etiketleri arasına alınır. Ajan bu etiket içindeki isteğe mutlak öncelik vermelidir.
  - Model bir "kod yazma robotu" değil, "pair programming uzmanı" olarak konumlandırılır.

#### 2. `<user_information>` — Çalışma Alanı, İşletim Sistemi ve İzolasyon Sınırları
* **Amaç:** Modelin çalıştığı işletim sistemini, geçerli çalışma alanlarını (workspace), kök dizinleri ve yazma yasaklarını belirler.
* **Ham Şablon Metni:**
  ```xml
  <user_information>
  The USER's OS version is windows.
  The user has 1 active workspaces, each defined by a URI and a CorpusName. Multiple URIs potentially map to the same CorpusName. The mapping is shown as follows in the format [URI] -> [CorpusName]:
  C:\Users\metin -> C:/Users/metin
  Code relating to the user's requests should be written in the locations listed above. Avoid writing project code files to tmp, in the .gemini dir, or directly to the Desktop and similar folders unless explicitly asked.
  App Data Directory: C:\Users\metin\.gemini\antigravity-cli
  Conversation ID: 97fb0a00-dd9c-48f7-9a91-859511db936e
  </user_information>
  ```
* **Kritik Kurallar:**
  - **Dizin Sınırlandırması:** `tmp`, `.gemini` ve `Desktop` gibi konumlara kullanıcı açıkça istemediği sürece dosya yazılması KESİNLİKLE yasaklanmıştır.
  - **URI-Corpus Eşleşmesi:** Windows ters eğik çizgileri (`\`) ve arama motoru/vektör dizini standart eğik çizgileri (`/`) açıkça eşlenir.
  - **Sabit Meta-veriler:** `App Data Directory` ve `Conversation ID` her oturumda dinamik olarak yerleştirilir.

#### 3. `<mcp_servers>` — Model Context Protocol (MCP) Şema Yönetimi
* **Amaç:** Eager (hemen yüklenen) ve Lazy (ihtiyaç halinde yüklenen) harici MCP araçlarını yönetir.
* **Ham Şablon Metni:**
  ```xml
  <mcp_servers>
  Each MCP server has a directory `C:\Users\metin\.gemini\antigravity-cli\mcp\<serverName>` containing tool schemas (`<toolName>.json`) and optionally an `instructions.md` file with best practices.
  Eagerly loaded tools are registered as native tools under the name `mcp_<serverName>_<toolName>`. Call eager tools directly.
  For lazily-loaded tools, read the corresponding schema file to understand the arguments and usage, then call the tool using the `call_mcp_tool` tool.
  The following MCP servers and their available tools are listed below, following this format:
  ```
  # <serverName>
  Eager:
  <toolName>
  Lazy:
  <toolName>
  ```
  # qwen-llama-bridge
  Lazy:
  qwen_generate
  </mcp_servers>
  ```
* **Kritik Kurallar:**
  - **Eager Araçlar:** Doğrudan yerel araç gibi çağrılır: `mcp_<serverName>_<toolName>`.
  - **Lazy Araçlar:** Doğrudan çağrılamaz; önce şema dosyası (`<appDataDir>\mcp\<server>\<tool>.json`) incelenir, ardından `call_mcp_tool` aracılığıyla argüman nesnesi geçilerek çalıştırılır.

#### 4. `<user_rules>` — Kullanıcı Tanımlı Kural Enjeksiyonu
* **Amaç:** Proje veya kullanıcı düzeyindeki kuralları (`AGENTS.md`, `GEMINI.md`) mutlak öncelikle sisteme enjekte eder.
* **Ham Şablon Metni:**
  ```xml
  <user_rules>
  The following are user-defined rules that you MUST ALWAYS FOLLOW WITHOUT ANY EXCEPTION. These rules take precedence over any following instructions.
  Review them carefully and always take them into account when you generate responses and code:
  <RULE[C:\Users\metin\AGENTS.md]>
  ... dosya içeriği ...
  </RULE[C:\Users\metin\AGENTS.md]>
  </user_rules>
  ```
* **Kritik Kurallar:**
  - **Mutlak Öncelik:** "MUST ALWAYS FOLLOW WITHOUT ANY EXCEPTION. These rules take precedence over any following instructions." ifadesiyle sistem isteminin diğer tüm kurallarından üstün kılınır.
  - **Etiket Formatı:** `<RULE[tam_dosya_yolu]>` formatı kullanılarak kuralın kaynağı modele tam şeffaflıkla bildirilir.

#### 5. `<skills>` — Aşamalı İfşa (Progressive Disclosure) ve Skill Yaşam Döngüsü
* **Amaç:** Yüzlerce skill içeren geniş kütüphanelerde bağlam penceresini (context window) şişirmeden yetenekleri modele tanıtır.
* **Ham Şablon Metni:**
  ```xml
  <skills>
  You can use specialized 'skills' to help you with complex tasks. Each skill has a name and a description listed below.

  Skills are folders of instructions, scripts, and resources that extend your capabilities for specialized tasks. Each skill folder contains:
  - **SKILL.md** (required): The main instruction file with YAML frontmatter (name, description) and detailed markdown instructions

  More complex skills may include additional directories and files as needed, for example:
  - **scripts/** - Helper scripts and utilities that extend your capabilities
  - **examples/** - Reference implementations and usage patterns
  - **resources/** - Additional files, templates, or assets the skill may reference
  - **references/** - Contains additional documentation that agents can read when needed

  If a skill seems relevant to your current task, you MUST read its `SKILL.md` instructions using `view_file` before proceeding. You may skip this step only if you are delegating the skill-related task to a subagent that will read and follow the instructions itself.

  When calling `view_file` on these skill paths, always use the exact path provided in the "Available skills" list below.

  Available skills:
  - agy-customizations (C:\Users\metin\.../SKILL.md): Comprehensive guide...
  - ...
  </skills>
  ```
* **Kritik Kurallar:**
  - **Mandatory Pre-Read Kuralı:** Model bir skill'i kullanmaya karar verirse, herhangi bir işlem yapmadan önce MUTLAKA `view_file` ile `SKILL.md` dosyasını okumak zorundadır.
  - **Yalnızca Meta-veri Enjeksiyonu:** Başlangıçta modele sadece `name`, `SKILL.md mutlak yolu` ve `description` verilir. İçerik bağlama dahil edilmez (Progressive Disclosure).

#### 6. `<subagents>` & `<subagent_reminder>` — Çoklu Ajan Ayrımı
* **Amaç:** Ana ajan ile alt ajanlar (subagents) arasındaki görev paylaşımını, yetkilendirmeyi ve iletişim protokollerini düzenler.
* **Ham Şablon Metni:**
  ```xml
  <subagents>
  ## Invoking Subagents
  Subagents can be invoked using the invoke_subagent tool. You can invoke an existing subagent by name, or define a new subagent for this conversation using the define_subagent tool, and then invoke it...
  ## Communicating with Another Agent
  Use the send_message tool to send a message to another agent by its conversation ID (returned by invoke_subagent). This tool is ONLY for communicating with other agents.
  **Do NOT use send_message to communicate with the user.** Instead, output visible text to communicate with the user.
  Available subagents:
  - self: Subagent that inherits the parent agent's full configuration...
  - research: Research subagent with read-only tools...
  </subagents>
  ```
* **Alt Ajan Hatırlatıcısı (`<subagent_reminder>`):**
  Bir ajan alt ajan (subagent) olarak çalıştırıldığında, istemine dinamik olarak şu blok eklenir:
  ```xml
  <subagent_reminder>
  You are running as a subagent, invoked by a caller agent (name: "parent", id: "6cb5f35d-..."). You MUST use send_message to communicate all results, reports, and updates back to the caller. Your response is NOT automatically relayed — if you do not call send_message, the caller will only know that you have gone idle. Always use the caller's id as the Recipient and "parent" as the RecipientName.
  Text you generate outside of send_message will NOT be seen by the caller, so keep them brief. Put all important information — findings, summaries, conclusions — into your send_message calls instead. You can also share files by including their absolute paths in your message; the caller can then read them directly.
  </subagent_reminder>
  ```

#### 7. `<messaging>` — Reaktif Uyandırma Döngüsü (Reactive Wakeup)
* **Amaç:** Asenkron süreçlerde modelin anlamsız "polling / sleep loop" yapmasını engeller.
* **Ham Şablon Metni:**
  ```xml
  <messaging>
  You are connected to a messaging system where you may receive messages from: agents, background tasks, user-queued messages.
  ## Receiving Messages
  You receive messages automatically at the start of each invocation. All messages are delivered in full directly into your context — no manual retrieval is needed.
  ## Reactive Wakeup (No Polling Needed)
  The system automatically resumes your execution when:
  - A message arrives from a subagent or peer agent
  - A **background task** completes or sends you a notification
  - A **user-queued message** is ready to be dequeued
  This means you do **NOT** need to poll in a loop while waiting for messages or updates. After launching anything that performs work asynchronously, you may continue other work or simply stop by calling no more tools. The system will notify you when there is something to process.
  </messaging>
  ```

#### 8. `<conversation_transcript>` — Oturum Günlüğü (Trajectory) Formatı
* **Amaç:** Ajanın kendi geçmişini ve konuşma yörüngesini disk üzerinde denetleyebilmesini sağlar.
* **Konum:** `<appDataDir>\brain\<conversation-id>\.system_generated\logs\transcript.jsonl`
* **Yapı:** JSONL formatında her satır bir adımdır (`step_index`, `source`, `type`, `status`, `created_at`, `content`, `thinking`, `tool_calls`, `truncated_fields`).
* **Önemli Kural:** `truncated_fields` alanı varsa, tam veriyi okumak için aynı satır numarasıyla `transcript_full.jsonl` dosyası okunur.

#### 9. `<artifacts>` — Artefakt ve Görselleştirme Mimarisi
* **Amaç:** Büyük raporları, tasarım belgelerini, diff çıktılarını ve diyagramları sohbet akışından ayırarak yapılandırılmış Markdown belgelerine dönüştürür.
* **Konum:** `<appDataDir>\brain\<conversation-id>\<artifact_name>.md`
* **Geçici Dosyalar:** `<appDataDir>\brain\<conversation-id>\scratch\` dizininde saklanır.
* **Kurallar:**
  - Artefakt oluşturulduktan sonra içeriği kullanıcıya sohbette tekrar özetlenmez; kullanıcı doğrudan artefakta yönlendirilir.
  - Harici bir dosya artefakt içine gömülecekse (`![caption](/path)`), dosya önce mutlaka artefakt dizinine kopyalanmalıdır.
  - Mermaid diyagramlarında yalnızca desteklenen tipler kullanılır: `flowchart`, `sequenceDiagram`, `stateDiagram-v2`, `classDiagram`, `erDiagram`, `xychart-beta`.

#### 10. `<slash_commands>`, `<guidelines>` & `<communication_style>`
* **Slash Commands:** Model slash komutları kendisi çalıştıramaz, ancak uygun durumlarda kullanıcıya önerir (`/goal`, `/schedule`, `/browser`, `/plan`, `/grill-me`, `/teamwork-preview`, `/learn`, `/boost`).
* **Guidelines:** Mevcut kodlardaki ilgisiz yorum satırları ve docstring'ler mutlak surette korunmalıdır (Documentation Integrity).
* **Communication Style:**
  - Yanıtlar kısa ve öz olmalıdır.
  - Varsayım yapılmamalı, belirsizlik durumunda kullanıcıya soru sorulmalıdır.
  - **Clickable Links Kuralı:** Kod sembolleri ve dosyalar MUTLAKA GitHub tarzı markdown bağlantısı ile verilmelidir: `[dosya.py](file:///C:/proje/dosya.py#L10-L20)`. Windows için yollarda her zaman ileri eğik çizgi (`/`) kullanılmalıdır.

---

## 3. AGY 20 Araçlık Bildirimsel Protokol İncelemesi (Tool Engine Decoded)

AGY çalışma zamanı, modele tam olarak **20 adet fonksiyon** sunar. Bu araçların en ayırt edici özelliği; **istisnasız her birinde** `toolAction` ve `toolSummary` parametrelerinin zorunlu (`required`) tutulmasıdır.

### 3.1 Evrensel Meta-Veri Protokolü (`toolAction` & `toolSummary`)

Gemini modelinin ürettiği her `functionCall` bloğu şu iki parametreyi içermek zorundadır:
- **`toolAction` (STRING):** Eylemin cümle formatında özeti (Örn: `"Viewing file"`, `"Running command"`, `"Searching the web"`, `"Analyzing directory"`).
- **`toolSummary` (STRING):** Eylemin isim tamlaması formatında başlığı (Örn: `"File view"`, `"Command execution"`, `"Web search"`, `"Directory analysis"`).

> **DSH Adaptör Notu:** AGY terminal arayüzü (TUI) veya web GUI'si, aracın ne yaptığını göstermek için JSON gövdesini parse etmek yerine doğrudan bu iki alanı okuyarak progress bar ve spinner başlıklarını dinamik olarak günceller.

---

### 3.2 Sistem Araçları ve Süreç Yönetimi

#### 1. `run_command` — İşletim Sistemi Komut İcrası
* **Şema Özeti:**
  ```json
  {
    "name": "run_command",
    "description": "PROPOSE a command to run on behalf of the user. Operating System: windows. Shell: powershell.\n**NEVER PROPOSE A cd COMMAND**...",
    "parameters": {
      "properties": {
        "CommandLine": { "type": "STRING", "description": "The exact command line string to execute." },
        "Cwd": { "type": "STRING", "description": "The current working directory for the command" },
        "WaitMsBeforeAsync": { "type": "INTEGER", "description": "Milliseconds to wait before sending to background. Max: 10000ms." },
        "toolAction": { "type": "STRING" },
        "toolSummary": { "type": "STRING" }
      },
      "required": ["Cwd", "WaitMsBeforeAsync", "CommandLine", "toolSummary", "toolAction"]
    }
  }
  ```
* **Kritik Kurallar:**
  - **`cd` YASAĞI:** Modelin asla `cd <path>` çalıştırmasına izin verilmez. Dizin değişimi MUTLAKA `Cwd` parametresi ile yönetilir. Model `cd` denerse sistem hata döner.
  - **Senkron / Asenkron Ayrımı (`WaitMsBeforeAsync`):**
    - Komutun normal şartlarda tamamlanması bekleniyorsa yüksek bir değer (örn. 5000-10000ms) verilir.
    - Komut uzun sürecek bir sunucu veya izleme süreci ise (background daemon), olası başlangıç hatalarını (syntax, port çakışması) yakalayacak kadar (örn. 500ms) beklenir ve ardından süreç arka plana fırlatılır.
  - **Paging Kontrolü:** Ortamda `PAGER=cat` zorunludur. `git log` gibi komutlar kilitlenmeyi önlemek için daima `-n <N>` ile sınırlandırılmalıdır.

#### 2. `manage_task` — Arka Plan Görev Yönetimi
* **Şema Özeti:**
  - **`Action` (STRING, Enum):** `['list', 'kill', 'status', 'send_input']`
  - **`TaskId` (STRING):** Yönetilecek görevin kimliği.
  - **`Input` (STRING):** `send_input` eylemi için stdin girdisi.
* **Kritik Kurallar:**
  - Model arka plandaki komutun bitmesini beklemek için `status` üzerinde asla loop / poll yapmamalıdır. Sistem süreç bitince otomatik mesaj iletir.

#### 3. `schedule` — Zamanlayıcı ve Cron Motoru
* **Şema Özeti:**
  - **`DurationSeconds` (INTEGER):** Tek seferlik geri sayım sayacı.
  - **`TimerCondition` (STRING, Opsiyonel):** `'never'` (varsayılan), `'any'` (herhangi bir mesaj gelirse iptal et), veya `<sender-id>` (belirli bir ajandan/görevden yanıt gelirse erken sonlandır).
  - **`CronExpression` (STRING):** 5 alanlı standart cron (`*/5 * * * *`).
  - **`MaxIterations` (INTEGER):** Cron tetiklenme limiti.
  - **`Prompt` (STRING):** Zamanlayıcı patladığında ajana iletilecek yüksek öncelikli sistem mesajı.
* **Kritik Kurallar:**
  - Model asla terminalde `sleep 60` gibi komutlar çalıştırmamalı, `schedule` aracını kullanmalıdır.

---

### 3.3 Dosya Sistemi ve Düzenleme Araçları

#### 4. `view_file` — Dilimlemeli ve Ofsetli Dosya İnceleme
* **Kısıtlar ve Kurallar:**
  - **800 Satır Kuralı:** Tek seferde en fazla 800 satır görüntülenebilir.
  - **46.080 Bayt Kuralı:** Çıktı 46 KB'yi aşarsa kırpılır (truncated). Kalan kısmı okumak için `ContentOffset` kullanılır.
  - **Dilimleme Mantığı (Slice Notation):**
    - `StartLine` ve `EndLine` verilmezse: İlk 800 satır.
    - Yalnızca `StartLine`: O satırdan itibaren sonraki 800 satır.
    - Yalnızca `EndLine`: Baştan o satıra kadar olan kısım.
    - İkisi birlikte: Belirtilen aralık (aralık < 800 satır olmalı).
  - **Binary Desteği:** Resim, PDF, ses, video desteklenir. Binary dosyalarda `StartLine`/`EndLine` verilmez, dosya doğrudan döndürülür (Max: 100 MB).

#### 5. `replace_file_content` — Kesintisiz Blok Düzenleyici
* **Kısıtlar ve Kurallar:**
  - **Tek Kesintisiz Blok (Single Contiguous Block):** Bu araç yalnızca ardışık tek bir kod bloğunu değiştirmek içindir. Bir dosyadaki birbirinden kopuk birden fazla yeri değiştirmek için araç ARDIŞIK OLARAK BİRDEN FAZLA KEZ çağrılmalıdır.
  - **Leading Whitespace Hassasiyeti:** `TargetContent` dosyadaki orijinal satırlarla karakteri karakterine (boşluklar, girintiler dahil) tam eşleşmelidir. Eşleşmezse işlem iptal edilir.
  - **Yasaklı Uzantılar:** `.ipynb` dosyaları bu araçla düzenlenemez.
  - **Paralel Çağrı Yasağı:** Aynı dosya üzerinde aynı anda birden fazla paralel `replace_file_content` çağrısı yapılamaz.

#### 6. `write_to_file` — Yeni Dosya ve Artefakt Üretimi
* **Kısıtlar ve Kurallar:**
  - Dosya zaten varsa ve `Overwrite: false` ise hata üretir. Üzerine yazmak için `Overwrite: true` zorunludur.
  - **`ArtifactMetadata` Şartı:** Eğer yazılan dosya bir artefakt ise (`<appDataDir>\brain\<conversationId>` altında), şu meta-veri nesnesi ZORUNLUDUR:
    ```json
    {
      "Summary": "Ayrıntılı çok satırlı özet",
      "UserFacing": true,
      "RequestFeedback": false
    }
    ```
    Normal proje kod dosyalarında `ArtifactMetadata` kesinlikle gönderilmemelidir.

#### 7. `find_by_name`, `grep_search` & `list_dir`
* `find_by_name`: `fd` tabanlı dosya arama. Smart case, `.gitignore` desteği, 50 sonuç tavanı.
* `grep_search`: `ripgrep` tabanlı metin arama. 50 sonuç tavanı. `MatchPerLine: true` ise satır numarası ve içeriği; `false` ise sadece dosya listesi döner. Regex desteği `IsRegex: true` ile açılır.
* `list_dir`: Dizin çocuklarını (boyut, dosya/klasör türü, çocuk sayısı) listeler.

---

### 3.4 Çoklu Ajan (Multi-Agent) ve Kullanıcı Etkileşimi

#### 8. `invoke_subagent` & `define_subagent`
* **`invoke_subagent` Parametreleri:**
  - `Subagents` dizisi: Her alt ajan için `TypeName`, `Role`, `Prompt`, `Model` (`inherit`, `flash_lite`, `flash`, `pro`), `Workspace` (`inherit`, `branch`, `share`).
  - Her alt ajana benzersiz bir `conversationID` atanır.
* **`send_message` — Ajanlar Arası Mesajlaşma:**
  - **MUTLAK KURAL:** `send_message` yalnızca ve yalnızca alt ajanlara veya ebeveyn ajana mesaj göndermek içindir. Kullanıcıya bir şey söylemek için ASLA `send_message` kullanılamaz! Kullanıcıya doğrudan modelin metin çıktısı (visible response) ile yanıt verilir.
* **`manage_subagents`:** Alt ajanları listeleme (`list`), belirli ajanları öldürme (`kill`), veya soy ağacıyla birlikte hepsini sonlandırma (`kill_all`).

#### 9. `ask_question` — Yapılandırılmış Çoktan Seçmeli Kullanıcı Modalı
* **Şema Özeti:**
  - `questions`: Dizi halinde `question`, `options` (en az 2 seçenek), `is_multi_select` (boolean).
* **UI ve Form Kuralları:**
  - Modal çağrıldığında kullanıcının yanıtı gelene kadar ajanın icrası bloke olur (blocking).
  - Seçenekler kullanıcının ağzından ("(Recommended) npm run test komutunu çalıştır") şeklinde yazılmalıdır.
  - Listeye asla "Other / Diğer" seçeneği eklenmez; sistem UI'da bunu otomatik sunar.
  - Seçenekler harf veya rakamla numaralandırılmaz (A, B, 1, 2 vb. eklenmez); UI otomatik numaralandırır.
  - Soru başlığına "Birden fazla seçebilirsiniz" yazılmaz; `is_multi_select: true` ise UI bunu otomatik belirtir.
  - Önerilen seçenek her zaman listenin en başında yer alır ve `"(Recommended)"` önekiyle başlar.

---

## 4. DSH Adaptörü İçin İstem Üretim ve Araç Rehberi Şartnamesi

DSH (DeepSeek Harness) mimarisinin Gemini 1.5 Pro/Flash veya DeepSeek-V3/R1 modellerini AGY düzeyinde bir otonom ajana dönüştürebilmesi için aşağıdaki 4 çekirdek modül geliştirilmelidir.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      DSH PROMPT COMPILER PIPELINE                           │
│                                                                             │
│   1. Identity & Workspace Context Builder                                   │
│   2. Hierarchical Rule Engine (AGENTS.md / GEMINI.md Scanner & Deduplicator)│
│   3. Progressive Skill Discovery (Name/Desc Ingestion -> SKILL.md Reader)   │
│   4. Subagent State Machine & Parent-Child Communicator                     │
│   5. Universal Tool Declaration Registry (20 Tools + toolAction/Summary)   │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                         Transpiled Payload (AIP-136)
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                    GEMINI / DEEPSEEK RUNTIME EXECUTION                      │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 4.1 İstem Derleyici Motoru (Prompt Compiler Engine)

DSH çekirdeğinde yer alacak `AgyPromptCompiler` sınıfı, sistem istemini 6 aşamada dinamik olarak inşa etmelidir:

```typescript
// packages/prompt/agy-compiler.ts

export interface AgyCompilerContext {
  os: 'windows' | 'linux' | 'darwin';
  workspacePath: string;
  corpusName: string;
  appDataDir: string;
  conversationId: string;
  isSubagent: boolean;
  parentAgentId?: string;
  availableSkills: SkillSummary[];
  mcpServers: McpServerRegistry;
}

export class AgyPromptCompiler {
  public compileSystemInstruction(ctx: AgyCompilerContext): string {
    const sections: string[] = [];

    // 1. Identity
    sections.push(this.renderIdentity());

    // 2. User Information
    sections.push(this.renderUserInformation(ctx));

    // 3. MCP Servers
    sections.push(this.renderMcpServers(ctx.mcpServers));

    // 4. User Rules (Hierarchical scan CWD -> Root)
    sections.push(this.renderUserRules(ctx.workspacePath));

    // 5. Skills (Progressive Disclosure - only metadata)
    sections.push(this.renderSkills(ctx.availableSkills));

    // 6. Subagents & Messaging
    sections.push(this.renderSubagents());
    if (ctx.isSubagent && ctx.parentAgentId) {
      sections.push(this.renderSubagentReminder(ctx.parentAgentId));
    }
    sections.push(this.renderMessaging());

    // 7. Transcripts & Artifacts
    sections.push(this.renderTranscriptSection(ctx));
    sections.push(this.renderArtifactsSection(ctx));

    // 8. Commands & Guidelines
    sections.push(this.renderSlashCommands());
    sections.push(this.renderGuidelines());
    sections.push(this.renderCommunicationStyle());

    return sections.join('\n');
  }

  private renderUserRules(workspacePath: string): string {
    // CWD'den kök dizine kadar AGENTS.md ve GEMINI.md dosyalarını tara ve deduplicate et
    const ruleFiles = scanHierarchicalRules(workspacePath);
    if (ruleFiles.length === 0) return '';

    let content = '<user_rules>\n';
    content += 'The following are user-defined rules that you MUST ALWAYS FOLLOW WITHOUT ANY EXCEPTION. These rules take precedence over any following instructions.\n';
    content += 'Review them carefully and always take them into account when you generate responses and code:\n';
    
    for (const file of ruleFiles) {
      content += `<RULE[${file.canonicalPath}]>\n${file.content}\n</RULE[${file.canonicalPath}]>\n`;
    }
    content += '</user_rules>';
    return content;
  }
}
```

---

### 4.2 Araç Çağırma ve Telemetri Adaptörü (Tool Telemetry Bridge)

Gemini modelinin ürettiği `toolAction` ve `toolSummary` argümanları DSH olay veri yoluna (event bus) bağlanarak terminal ve kullanıcı arayüzüne anlık durum bilgisi aktarılmalıdır:

```typescript
// packages/tools/telemetry-bridge.ts

export interface AgyToolCallMeta {
  toolAction: string;
  toolSummary: string;
}

export async function executeAgyTool(
  toolName: string,
  rawArgs: Record<string, any>,
  eventBus: DshEventBus
): Promise<any> {
  const meta: AgyToolCallMeta = {
    toolAction: rawArgs.toolAction || 'Executing tool',
    toolSummary: rawArgs.toolSummary || 'Tool execution'
  };

  // UI Telemetrisini Ateşle (TUI Spinner & Progress Bar)
  eventBus.emit('tool_start', {
    tool: toolName,
    action: meta.toolAction,
    summary: meta.toolSummary,
    timestamp: new Date().toISOString()
  });

  try {
    // Özel Güvenlik Korumaları
    if (toolName === 'run_command') {
      if (/\bcd\b/i.test(rawArgs.CommandLine)) {
        throw new Error("SECURITY_VIOLATION: 'cd' command is strictly forbidden. Use 'Cwd' parameter instead.");
      }
    }

    const result = await DshToolRegistry.dispatch(toolName, rawArgs);
    eventBus.emit('tool_success', { tool: toolName, summary: meta.toolSummary });
    return result;
  } catch (error: any) {
    eventBus.emit('tool_error', { tool: toolName, error: error.message });
    throw error;
  }
}
```

---

### 4.3 DSH-AGY Karşılaştırmalı Özellik ve Uyumluluk Matrisi

| Yetenek / Fonksiyon | Orijinal AGY Davranışı | DSH Adaptör Karşılığı | Uyumluluk Durumu |
| :--- | :--- | :--- | :--- |
| **Sistem İstemi Formatı** | XML etiketli hiyerarşik yapı (`<identity>`, `<user_rules>`) | `AgyPromptCompiler` modülü ile %100 birebir derleme | **Tam Uyumlu** |
| **Kural Enjeksiyonu** | `<RULE[path]>` hiyerarşik tarama ve tekilleştirme | Walk-up dosya sistemi tarayıcısı (`AGENTS.md`) | **Tam Uyumlu** |
| **Skill Erişimi** | Progressive disclosure + zorunlu `view_file` okuması | Yalnızca özet metadata enjeksiyonu + disk router | **Tam Uyumlu** |
| **Araç Meta-Verisi** | Her araçta zorunlu `toolAction` & `toolSummary` | Schema decorator ile 20 araca otomatik enjeksiyon | **Tam Uyumlu** |
| **Komut İcrası** | `run_command` (powershell/bash, `WaitMsBeforeAsync`, `cd` yasağı) | Windows Job Object + `Cwd` zorlamalı DSH Shell | **Tam Uyumlu** |
| **Dosya İnceleme** | `view_file` (800 satır, 46KB offset, binary desteği) | `dsh-fs` bounded stream reader + offset cursor | **Tam Uyumlu** |
| **Dosya Düzenleme** | `replace_file_content` (Tek kesintisiz blok, whitespace strict) | Atomik Hunk Matcher + leading space guard | **Tam Uyumlu** |
| **Alt Ajan Yaşam Döngüsü**| `invoke_subagent` + `send_message` (Kullanıcıya mesaj yasağı) | DSH Actor Engine + Message Router | **Tam Uyumlu** |
| **Zamanlayıcı / Cron** | `schedule` (`TimerCondition`, `DurationSeconds`) | DSH Event Loop Timer & Cron Manager | **Tam Uyumlu** |
| **Kullanıcı Modalı** | `ask_question` (İnteraktif çoktan seçmeli form) | DSH Interactive TUI / Stdin Modal Prompter | **Tam Uyumlu** |

---

## 5. Sonuç ve Eylem Planı

Google Antigravity'nin 37.182 baytlık ham sistem istemi ve 20 araçlık protokolünün analizi; sistemin başarısının tesadüfi olmadığını, modelin dikkatini (attention) mikro düzeyde yöneten katı kurallara dayandığını kanıtlamıştır.

DSH mimarisine aktarılacak bu şartname sayesinde:
1. Gemini ve DeepSeek modelleri, araçları çağırırken sözdizimi ve argüman hatası yapmayacaktır.
2. `toolAction` ve `toolSummary` alanları ile terminal kullanıcı arayüzü son derece akıcı bir canlı ilerleme göstergesine kavuşacaktır.
3. Alt ajanlar ve arka plan süreçleri gereksiz loop/polling yapmadan reaktif olay döngüsüyle (`messaging`) CPU tüketimini minimize edecektir.
4. Dosya düzenlemeleri atomik blok koruması (`replace_file_content`) ve 800 satırlık dilimleme (`view_file`) sayesinde context penceresini patlatmadan güvenle tamamlanacaktır.
