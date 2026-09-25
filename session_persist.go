package main

// session_persist.go — B1: PID→SessionID haritasının diske kalıcılığı.
//
// Neden: getStealthSessionID rastgele üretir; gateway restart olduğunda tüm
// oturumlar yeni SID alır → Google tarafındaki oturum/worker affinitesi kırılır
// ve ilk isteklerde cache miss görülür. sessions.json ile SID'ler açılışta
// geri yüklenir, üretilen yeni SID'ler 1 saniyelik debounce ile atomik yazılır.
//
// Risk (bilinçli kabul): pid reuse → iki farklı süreç aynı SID'yi paylaşabilir;
// SID yalnızca affinitesel etki taşır (Google telemetri korelasyonu dışında
// güvenlik değeri yoktur).

import (
	"encoding/json"
	"log"
	"os"
	"time"
)

type sessionPersist struct {
	PIDs map[int]string `json:"pids"`
}

const sessionsFilePath = "sessions.json"

var (
	sessionsDirtyCh = make(chan struct{}, 1)
)

func init() {
	loadStealthSessions()
	startSessionPersister()
}

// loadStealthSessions sessions.json varsa haritayı birleştirir.
// Bozuk dosya sessizce yok sayılmaz: loglanır ve karar verilir (A4/B1 dürüstlük).
func loadStealthSessions() {
	data, err := os.ReadFile(sessionsFilePath)
	if err != nil {
		return // ilk çalıştırmada dosya yok — normal
	}
	var p sessionPersist
	if err := json.Unmarshal(data, &p); err != nil {
		log.Printf("[Sessions] sessions.json BOZUK, yok sayılıyor (dosya yerinde bırakıldı): %v", err)
		GlobalReqLog.LogEvent("sessions_corrupt", map[string]interface{}{"error": err.Error()})
		return
	}
	stealthSessionMutex.Lock()
	added := 0
	for k, v := range p.PIDs {
		if v != "" {
			if _, exists := stealthSessionMap[k]; !exists {
				added++
			}
			stealthSessionMap[k] = v
		}
	}
	stealthSessionMutex.Unlock()
	log.Printf("[Sessions] sessions.json yüklendi: %d kayıt (%d yeni)", len(p.PIDs), added)
}

// sessionsDirty yeni SID üretildiğinde çağrılır (kilit altında güvenlidir;
// kanal doluysa zaten bekleyen bir yazım var demektir).
func sessionsDirty() {
	select {
	case sessionsDirtyCh <- struct{}{}:
	default:
	}
}

// startSessionPersister debounce'lu atomik (.tmp → Rename) yazımı yapar.
func startSessionPersister() {
	go func() {
		for range sessionsDirtyCh {
			time.Sleep(1 * time.Second)
		drain:
			for {
				select {
				case <-sessionsDirtyCh:
				default:
					break drain
				}
			}
			saveSessions()
		}
	}()
}

func saveSessions() {
	stealthSessionMutex.Lock()
	p := sessionPersist{PIDs: make(map[int]string, len(stealthSessionMap))}
	for k, v := range stealthSessionMap {
		p.PIDs[k] = v
	}
	stealthSessionMutex.Unlock()

	raw, err := json.Marshal(p)
	if err != nil {
		log.Printf("[Sessions] marshal hatası: %v", err)
		return
	}
	tmp := sessionsFilePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		log.Printf("[Sessions] yazma hatası: %v", err)
		return
	}
	if err := os.Rename(tmp, sessionsFilePath); err != nil {
		log.Printf("[Sessions] rename hatası: %v", err)
	}
}
