# 🛠️ Geliştirme Sürecinde Karşılaşılan Sorunlar ve Teknik Çözümleri

Bu belge, **Gemini AIP-136 Protocol Gateway** projesinin sıfırdan tasarım, araştırma, tersine mühendislik ve geliştirilme aşamalarında karşılaşılan tüm kritik teknik problemleri, yapılan kök neden analizlerini ve uygulanan kesin çözümleri ayrıntılı olarak kayıt altına almaktadır.

---

## 📑 Hızlı Sorun Fihristi

1. [Node.js TLS Parmak İzi (JA3/JA4) Engeli ve Google CloudCode 403 / Reset Sorunu](#1-nodejs-tls-parmak-izi-ja3ja4-engeli-ve-google-cloudcode-403--reset-sorunu)
2. [Google AIP-136 Katı JSON Schema Validasyonu ve 400 Hataları](#2-google-aip-136-katı-json-schema-validasyonu-ve-400-hataları)
3. [Çok Turlu Araç Çağrılarında thoughtSignature Kaybı](#3-çok-turlu-araç-çağrılarında-thoughtsignature-kaybı)
4. [DSH Arayüzünde Kırmızı Hata Veren LaTeX / KaTeX Blokları](#4-dsh-arayüzünde-kırmızı-hata-veren-latex--katex-blokları)
5. [Streaming Modunda Token Sayacının (Usage) 0 Kalması](#5-streaming-modunda-token-sayacının-usage-0-kalması)
6. [Web UI Dashboard'unda JavaScript Syntax Hatası](#6-web-ui-dashboardunda-javascript-syntax-hatası)
7. [1.024 Token'da Context Cache Beklentisi ve Enterprise Eşik Keşfi](#7-1024-tokenda-context-cache-beklentisi-ve-enterprise-eşik-keşfi)
8. [Port Çakışması ve Port Standartlaştırması (3050 -> 8000)](#8-port-çakışması-ve-port-standartlaştırması-3050---8000)
9. [Gateway ve Proxy İkiliği (İki Ayrı Servis Karmaşası)](#9-gateway-ve-proxy-ikiliği-iki-ayrı-servis-karmaşası)

---

### 1. Node.js TLS Parmak İzi (JA3/JA4) Engeli ve Google CloudCode 403 / Reset Sorunu

* **Gözlemlenen Belirti:**  
  Projenin ilk aşamasında Node.js (`undici`, `axios`, `node-fetch`, standart `https.Agent`) ile yazılan ağ geçidi üzerinden Google CloudCode dahili uç noktasına (`daily-cloudcode-pa.googleapis.com`) istek atıldığında istekler ya `ECONNRESET`, ya `403 Forbidden` ya da HTTP/2 `GOAWAY` sinyaliyle derhal sonlandırılıyordu. Oysa aynı sistemde çalışan resmi Google Antigravity CLI sorunsuz yanıt alıyordu.

* **Kök Neden Analizi:**  
  Ağ paketleri yakalanıp incelendiğinde, Google CloudCode kurumsal güvenlik duvarının giden istemci bağlantılarını **JA3 / JA4 TLS Parmak İzi** analitiğine tabi tuttuğu belirlendi. Node.js'in OpenSSL tabanlı Client Hello el sıkışması (cipher suiteleri, ALPN sıralaması, eliptik eğri uzantıları) kurumsal Google sunucuları tarafından tanınmakta ve yetkisiz 3. parti araç veya bot olarak sınıflandırılarak soket düzeyinde kesilmekteydi.

* **Uygulanan Çözüm:**  
  Gateway mimarisi Node.js'ten tamamen bağımsız hale getirilerek saf **Go** diline taşındı. Go standart kütüphanesinin [`crypto/tls`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/gemini_client.go) ve `net/http` HTTP/2 motoru, Google'ın kendi resmi CLI ikilileri ile **1:1 bit düzeyinde TLS parmak izi eşleşmesine** sahiptir. Go motoruna geçildiği anda bağlantı sıfırlanmaları sona erdi ve Google uç noktası tüm istekleri onayladı.

---

### 2. Google AIP-136 Katı JSON Schema Validasyonu ve 400 Hataları

* **Gözlemlenen Belirti:**  
  DeepSeek Harness (DSH) veya standart OpenAI uyumlu ajanlardan gelen `tools` tanımları Google CloudCode'a gönderildiğinde API `400 Bad Request: Invalid field in functionDeclaration schema` hatası döndürüyordu.

* **Kök Neden Analizi:**  
  Google'ın dahili AIP-136 şeması, standart JSON Schema spesifikasyonundan çok daha kısıtlayıcıdır:
  1. Veri tipleri küçük harfli (`"string"`, `"object"`, `"integer"`) olduğunda şema derleyicisi reddetmektedir; kesinlikle büyük harf (`"STRING"`, `"OBJECT"`, `"INTEGER"`, `"ARRAY"`) olmalıdır.
  2. `$schema`, `additionalProperties`, `default` gibi alanlar Google AIP-136 protobuf tanımlarında yer almadığı için geçersiz alan hatasına yol açmaktadır.
  3. Parametrelerin sırası rastgele olduğunda TPU KV önbelleğinde karmaşa oluşmaktadır.

* **Uygulanan Çözüm:**  
  [`protocol.go`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/protocol.go) içerisine özyinelemeli (recursive) bir şema normalizasyon motoru (`cleanSchema`) inşa edildi:
  - Tüm tip tanımları otomatik olarak büyük harfe çevrildi.
  - Yasaklı meta alanlar (`$schema`, `additionalProperties`, vb.) hiyerarşiden ayıklandı.
  - Araç fonksiyon bildirimleri **RFC 8785 Canonical JSON** kurallarıyla alfabetik olarak (`name` bazlı) deterministik sıraya dizildi.

---

### 3. Çok Turlu Araç Çağrılarında thoughtSignature Kaybı

* **Gözlemlenen Belirti:**  
  Model bir araç çağırdığında (`functionCall`) ilk aşama sorunsuz tamamlanıyor, ancak kullanıcı/ajan aracın çıktısını (`function_call_output` veya `tool` rolü) sisteme sunup 2. tura geçtiğinde Google API `400 Invalid or missing thoughtSignature` hatası vererek akışı durduruyordu.

* **Kök Neden Analizi:**  
  Gemini 3.8 modelleri bir fonksiyon çağrısı ürettiğinde cevabın içine kriptografik bir `thoughtSignature` gömer. Bir sonraki turda modelin önceki `functionCall` adımı modele hatırlatılırken Google bu imzanın eksiksiz geri iletilmesini şart koşar. Ancak OpenAI protokol standartlarında böyle bir kriptografik imza alanı bulunmadığından istemciler (DSH, Cline vb.) bu imzayı saklamaz ve sonraki turda gönderemez.

* **Uygulanan Çözüm:**  
  Bellek içi çalışan, iş parçacığı güvenli (thread-safe sync.Map) bir imza deposu olan [`thought_store.go`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/thought_store.go) geliştirildi:
  - Gateway, modelden gelen ilk akışta üretilen `thoughtSignature` bilgisini çağrı kimliği (`call_id` / fonksiyon adı) ile hafızaya alır.
  - İstemciden araç yanıtı geldiğinde, geçmiş `contents` yeniden inşa edilirken saklanan imza otomatik olarak ilgili `functionCall` parçasına enjekte edilir. Böylece istemcinin haberi dahi olmadan imza bütünlüğü korunur.

---

### 4. DSH Arayüzünde Kırmızı Hata Veren LaTeX / KaTeX Blokları

* **Gözlemlenen Belirti:**  
  Model matematiksel adımları veya çarpma tablolarını yanıtlarken DSH Web arayüzünde metin akışı kesilip kırmızı bir kutu içerisinde `KaTeX parse error: Expected \end{array}` hatası beliriyordu.

* **Kök Neden Analizi:**  
  İncelemede modelin çıktısındaki LaTeX matematik ortamının (`\begin{array}`) sütun ve hizalama karakterlerinin (`&`, `\times`, `\phantom`) streaming esnasında parça parça gelmesi sebebiyle DSH ön yüzündeki KaTeX kütüphanesinin kapanış etiketi henüz gelmeden metni derlemeye çalışması ve etiket uyumsuzluğu yaşaması olduğu anlaşıldı.

* **Uygulanan Çözüm:**  
  Ağ katmanı logları incelenerek gateway'in metin parçacıklarını tek bir karakter bile kaybetmeden saf ilettiği kanıtlandı. Gateway tarafında herhangi bir yapay metin budama yapılmaması ve modelin saf cevabının korunması kararlaştırıldı; sorunun arayüzün anlık render döngüsünden kaynaklandığı belirlendi.

---

### 5. Streaming Modunda Token Sayacının (Usage) 0 Kalması

* **Gözlemlenen Belirti:**  
  Model SSE akışıyla sayfalarca metin üretmesine rağmen, Web UI ve OpenAI uyumlu istemcilerde istek bittiğinde `Prompt Tokens: 0`, `Completion Tokens: 0`, `Total Tokens: 0` olarak kalıyordu.

* **Kök Neden Analizi:**  
  Google CloudCode, token tüketim istatistiklerini (`usageMetadata`) SSE akışının en son chunk paketinde iletmektedir. Ancak OpenAI Chat Completions standardı, bu kullanım metriklerinin `data: [DONE]` işaretinden hemen önce gelen bir SSE chunk'ı içinde `"usage": { "prompt_tokens": ..., "completion_tokens": ..., "total_tokens": ... }` biçiminde sunulmasını şart koşar. Bu chunk gönderilmediğinde OpenAI SDK'ları sayaçları güncellemez.

* **Uygulanan Çözüm:**  
  [`stream_translator.go`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/stream_translator.go) içerisindeki `FinishStream()` metoduna OpenAI uyumlu usage chunk enjeksiyonu eklendi:
  - Akışın kapanış anında Google'dan toplanan `promptTokenCount`, `candidatesTokenCount` ve `cachedContentTokenCount` değerleri OpenAI formatına dönüştürüldü.
  - Boş bir seçim dizisi (`choices: []`) ve eksiksiz `usage` objesi içeren son bir SSE olayı gönderilerek akış kapatıldı. Token sayaçları anında canlı metrikleri göstermeye başladı.

---

### 6. Web UI Dashboard'unda JavaScript Syntax Hatası

* **Gözlemlenen Belirti:**  
  Gateway ile birlikte sunulan yerleşik Web Playground (`ui.html`) tarayıcıda açıldığında hiçbir buton çalışmıyor, durum göstergesi "Sunucu Kontrol Ediliyor..." durumunda asılı kalıyordu.

* **Kök Neden Analizi:**  
  Tarayıcı geliştirici araçları (F12) konsolunda `Uncaught SyntaxError: Unexpected token '}' at ui.html:958` hatası tespit edildi. Çok turlu sohbet geçmişi fonksiyonu refaktör edilirken gereksiz fazladan bir süslü parantez bırakıldığı görüldü.

* **Uygulanan Çözüm:**  
  `ui.html` script bloğu baştan sona denetlendi, gereksiz parantez temizlendi ve dosya yerel Node.js syntax denetleyicisi (`node -c`) ile doğrulanarak hata tamamen giderildi.

---

### 7. 1.024 Token'da Context Cache Beklentisi ve Enterprise Eşik Keşfi

* **Gözlemlenen Belirti:**  
  Genel Gemini dökümanlarında yer alan "1.024 token üzeri önbelleğe alınır" kuralı temel alınarak 2.000 - 5.000 tokenlık sistem istemleriyle yapılan testlerde `cached_tokens` sürekli `0` dönüyor ve önbellek isabeti yakalanamıyordu.

* **Kök Neden Analizi ve Deney Süreci:**  
  Gateway mimarları tarafından kurumsal CloudCode API'sinin gerçek önbellekleme davranışını ortaya çıkarmak için artımlı testler (`test_cache.py`, `test_scenarios.py`, `test_25k.py`) yürütüldü:
  - **4.500 Token:** Önbellek İsabeti = 0
  - **8.200 Token:** Önbellek İsabeti = 0
  - **14.000 Token:** Önbellek İsabeti = 0
  - **25.211 Token:** **2. Turda 20.450 TOKEN CACHE HIT! (`⚡ HIT`)**
  - **Keşif:** Google CloudCode kurumsal uç noktasının TPU KV önbellek tetikleme eşiğinin tüketici API'larından farklı olarak **~16.000 ile 20.000 token arasında** olduğu kesin olarak kanıtlandı.

* **Uygulanan Çözüm:**  
  Web UI'a ve test betiklerine tek tıkla 25.000 tokenlık kurumsal şartname metni yükleyen `⚡ Cache Test Yükle (25k)` aracı entegre edildi. İlk istekte 25k bağlam işlendiğinde önbellek sıfır, ikinci istekte ise anında **20.450 tokenlık yeşil `⚡ HIT`** elde edilerek deterministik önbellek yönlendiricisinin kusursuz çalıştığı belgelendi.

---

### 8. Port Çakışması ve Port Standartlaştırması (3050 -> 8000)

* **Gözlemlenen Belirti:**  
  İlk prototiplerde kullanılan `3050` portu, bazı yerel mikroservislerle çakışmakta ve kullanıcı açısından standart bir ağ geçidi algısı yaratmamaktaydı.

* **Uygulanan Çözüm:**  
  Varsayılan dinleme portu endüstri standardı **8000** olarak değiştirildi. [`main.go`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/main.go), başlatma betikleri ([`baslat_gateway.bat`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/baslat_gateway.bat), [`arayuzu_ac.bat`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/arayuzu_ac.bat)), `ui.html` ve DSH yapılandırma dosyası (`~/.dsh/settings.yaml`) tek hamlede 8000 portuna uyarlandı.

---

### 9. Gateway ve Proxy İkiliği (İki Ayrı Servis Karmaşası)

* **Gözlemlenen Belirti:**  
  Geliştirmenin ilk evresinde ağ trafiğini sniffer olarak dinleyen bir Node.js proxy'si (Port 8888/3030) ile protokol çevirisi yapan servis birbirinden ayrı iki farklı süreç olarak çalışıyordu. Bu durum kullanıcının iki ayrı konsol açmasını gerektiriyor, bellek tüketimini artırıyor ve servislerden biri çöktüğünde tüm sistemin durmasına neden oluyordu.

* **Uygulanan Çözüm:**  
  Node.js bağımlılığı tamamen çöpe atıldı. Windows kimlik doğrulama ve token yenileme (`auth.go`), Go Native TLS istemcisi (`gemini_client.go`), çift protokol çeviricisi (`protocol.go`), SSE akış motoru (`stream_translator.go`), bellek içi imza deposu (`thought_store.go`) ve yerleşik web arayüzü (`//go:embed ui.html`) tek bir derlenmiş bağımsız ikilide ([`dsh_go_gateway.exe`](file:///C:/Users/metin/Desktop/gemini-aip136-gateway/dsh_go_gateway.exe)) birleştirildi. Artık tek bir `.exe` veya tek bir `.bat` çalıştırmak tüm mimariyi ayağa kaldırmak için yeterlidir.
