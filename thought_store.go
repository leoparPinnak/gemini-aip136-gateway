package main

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"sync"
	"time"
)

// thought_store.go — B2: kriptografik düşünce imzası deposu.
//
// Cache-hit sprint düzeltmeleri (anahtar FORMATI DEĞİŞMEZ — mevcut 24k+ kayıt
// geçerliliğini korur):
//  1. Get zincirinden ":toolName" global alias'ı çıkarıldı — sonradan gelen
//     herhangi bir imza, yabancı oturumların imzalarıyla "imza bulunamadı"
//     metin çevirisine (B1 amplifikatörü) yol açıyordu.
//  2. ":toolName" anahtarına artık YAZILMIYOR (alias kaynağı ve dosya büyümesi kalktı).
//  3. callId: (twin) anahtarı write-once: farklı araç aynı callId ile gelirse
//     ilk değer ezilmez.
//  4. Exact anahtar ilk-değer-korunur + uyarı: sessiz overwrite kalktı
//     (114 çakışma kanıtlanmıştı — thought_signatures.json).
//  5. Disk yazımı ASYNC + ATOMİK: 2 sn debounce, RLock altında kopya,
//     marshal lock dışında, .tmp → Rename. (Önceki: 31MB marshal+write
//     tam yazma kilidi altında — tool-call gecikmeleri.)
//  6. Load hatası artık sessiz değil: bozuk dosya .corrupt-<ts>'e taşınır,
//     log + JSONL olayı üretilir.
//  7. TTL: 30 günden eski girişler düşülür; kayıt sayısı >50k ise en eskiler
//     başa düşülür (sadece eşik aşımında).
//  8. Get zinciri: exact → callId: → "" (alias yok).

type ThoughtItem struct {
	Signature string    `json:"signature"`
	Timestamp time.Time `json:"timestamp"`
}

type ThoughtStore struct {
	cache         map[string]ThoughtItem
	lastSignature string
	mu            sync.RWMutex
	filePath      string
	dirty         chan struct{}
}

// DefaultFallbackSignature Google AIP-136 şamasında thought_signature boş olduğunda 400 hatasını önlemek için kullanılan geçerli base64 imzasıdır.
const DefaultFallbackSignature = "dGhvdWdodF9zaWduYXR1cmVfcHJlc2VydmVkX2Zvcl90b29sX2NhbGw="

const (
	thoughtTTL      = 30 * 24 * time.Hour // B2-7: 30 gün
	thoughtMaxKeys  = 50000               // B2-7: boyut tavanı
	thoughtSaveWait = 2 * time.Second     // B2-5: debounce
)

var GlobalThoughtStore = NewThoughtStore("thought_signatures.json")

func NewThoughtStore(filePath string) *ThoughtStore {
	store := &ThoughtStore{
		cache:    make(map[string]ThoughtItem),
		filePath: filePath,
		dirty:    make(chan struct{}, 1),
	}
	store.loadFromFile()
	go store.persister()
	return store
}

// key formatı BİLEREK DEĞİŞTİRİLMEDİ (eski kayıtlar geçerli kalsın diye).
func (s *ThoughtStore) key(callId, toolName string) string {
	return callId + ":" + toolName
}

func (s *ThoughtStore) loadFromFile() {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return // dosya yok — ilk çalışma normal
	}
	var loaded map[string]ThoughtItem
	if err := json.Unmarshal(data, &loaded); err != nil {
		// B2-6: sessiz boş başlama KALKTI — bozuk dosya kenara alınır.
		corruptPath := s.filePath + ".corrupt-" + time.Now().Format("20060102150405")
		if renameErr := os.Rename(s.filePath, corruptPath); renameErr != nil {
			log.Printf("[ThoughtStore] BOZUK dosya taşınamadı: %v (hata: %v)", renameErr, err)
		}
		log.Printf("[ThoughtStore] thought_signatures.json BOZUK (%v) → %s olarak kenara alındı; depo boş başlatılıyor", err, corruptPath)
		GlobalReqLog.LogEvent("store_corrupt", map[string]interface{}{
			"error":  err.Error(),
			"renamed": corruptPath,
		})
		return
	}

	// B2-7: TTL temizliği (tek seferlik, yükleme anında)
	now := time.Now()
	kept := make(map[string]ThoughtItem, len(loaded))
	var newest time.Time
	for k, item := range loaded {
		if item.Signature == "" {
			continue
		}
		if !item.Timestamp.IsZero() && now.Sub(item.Timestamp) > thoughtTTL {
			continue // 30 günden eski
		}
		kept[k] = item
		if item.Timestamp.After(newest) {
			newest = item.Timestamp
			s.lastSignature = item.Signature
		}
	}
	s.cache = kept
	if dropped := len(loaded) - len(kept); dropped > 0 {
		log.Printf("[ThoughtStore] TTL/boş temizlik: %d giriş silindi, %d kaldı", dropped, len(kept))
		GlobalReqLog.LogEvent("store_ttl_cleanup", map[string]interface{}{
			"dropped": dropped,
			"kept":    len(kept),
		})
	}
}

// persister B2-5: debounce'lu, kilit-dışı, atomik yazım.
func (s *ThoughtStore) persister() {
	for range s.dirty {
		time.Sleep(thoughtSaveWait)
	drain:
		for {
			select {
			case <-s.dirty:
			default:
				break drain
			}
		}
		s.saveAsync()
	}
}

func (s *ThoughtStore) saveAsync() {
	// 1) RLock altında hızlı kopya (marshal 31MB kilitsiz yapılır)
	s.mu.RLock()
	snap := make(map[string]ThoughtItem, len(s.cache))
	for k, v := range s.cache {
		snap[k] = v
	}
	s.mu.RUnlock()

	// B2-7: boyut tavanı — eşik aşıldığında en eskiler başa düşülür
	if len(snap) > thoughtMaxKeys {
		type kv struct {
			k string
			t time.Time
		}
		all := make([]kv, 0, len(snap))
		for k, v := range snap {
			all = append(all, kv{k, v.Timestamp})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].t.Before(all[j].t) })
		for i := thoughtMaxKeys; i < len(all); i++ {
			delete(snap, all[i].k)
		}
		GlobalReqLog.LogEvent("store_cap_evict", map[string]interface{}{"kept": len(snap)})
	}

	data, err := json.Marshal(snap)
	if err != nil {
		log.Printf("[ThoughtStore] marshal hatası: %v", err)
		return
	}
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		log.Printf("[ThoughtStore] yazma hatası: %v", err)
		GlobalReqLog.LogEvent("store_save_error", map[string]interface{}{"error": err.Error()})
		return
	}
	if err := os.Rename(tmp, s.filePath); err != nil {
		log.Printf("[ThoughtStore] rename hatası: %v", err)
		GlobalReqLog.LogEvent("store_save_error", map[string]interface{}{"error": err.Error()})
	}
}

func (s *ThoughtStore) markDirty() {
	select {
	case s.dirty <- struct{}{}:
	default:
	}
}

// Store bir imzayı kaydeder. İlk değer korunur; çakışma gizlenmez.
func (s *ThoughtStore) Store(callId, toolName, signature string) {
	if signature == "" {
		return
	}
	s.mu.Lock()

	s.lastSignature = signature

	if callId != "" {
		exactKey := s.key(callId, toolName)
		if existing, ok := s.cache[exactKey]; ok && existing.Signature != signature {
			// B2-4: ilk-değer-korunur + görünür uyarı (sessiz overwrite yok)
			log.Printf("[ThoughtStore] ⚠️ anahtar çakışması, İLK imza korundu: key=%q mevcut_len=%d yeni_len=%d", exactKey, len(existing.Signature), len(signature))
			GlobalReqLog.LogEvent("store_overwrite", map[string]interface{}{
				"key":         exactKey,
				"tool":        toolName,
				"kept_len":    len(existing.Signature),
				"dropped_len": len(signature),
			})
		} else if !ok {
			s.cache[exactKey] = ThoughtItem{Signature: signature, Timestamp: time.Now()}
		}

		// B2-3: twin (callId:) write-once
		twinKey := s.key(callId, "")
		if _, ok := s.cache[twinKey]; !ok {
			s.cache[twinKey] = ThoughtItem{Signature: signature, Timestamp: time.Now()}
		}
	}
	// B2-2: ":toolName" global alias YAZIMI KALDIRILDI.

	s.mu.Unlock()
	s.markDirty() // B2-5: disk yazımı artık async (kilit tutulmaz)
}

// Get imzayı exact → twin sırasıyla arar; global alias zinciri YOK (B2-1).
func (s *ThoughtStore) Get(callId, toolName string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if callId != "" && toolName != "" {
		if item, ok := s.cache[s.key(callId, toolName)]; ok && item.Signature != "" {
			return item.Signature
		}
	}
	if callId != "" {
		if item, ok := s.cache[s.key(callId, "")]; ok && item.Signature != "" {
			return item.Signature
		}
	}
	return ""
}

// GetFallback en son geçerli imzayı döner (protokol kurtarma yolu).
func (s *ThoughtStore) GetFallback() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastSignature != "" && len(s.lastSignature) >= 80 {
		return s.lastSignature
	}
	return ""
}
