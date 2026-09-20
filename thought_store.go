package main

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

type ThoughtItem struct {
	Signature string    `json:"signature"`
	Timestamp time.Time `json:"timestamp"`
}

type ThoughtStore struct {
	cache         map[string]ThoughtItem
	lastSignature string
	mu            sync.RWMutex
	filePath      string
}

// DefaultFallbackSignature Google AIP-136 şemasında thought_signature boş olduğunda 400 hatasını önlemek için kullanılan geçerli base64 imzasıdır.
const DefaultFallbackSignature = "dGhvdWdodF9zaWduYXR1cmVfcHJlc2VydmVkX2Zvcl90b29sX2NhbGw="

var GlobalThoughtStore = NewThoughtStore("thought_signatures.json")

func NewThoughtStore(filePath string) *ThoughtStore {
	store := &ThoughtStore{
		cache:    make(map[string]ThoughtItem),
		filePath: filePath,
	}
	store.loadFromFile()
	return store
}

func (s *ThoughtStore) key(callId, toolName string) string {
	return callId + ":" + toolName
}

func (s *ThoughtStore) loadFromFile() {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return
	}
	var loaded map[string]ThoughtItem
	if err := json.Unmarshal(data, &loaded); err == nil {
		s.cache = loaded
		var newest time.Time
		for _, item := range loaded {
			if item.Timestamp.After(newest) && item.Signature != "" {
				newest = item.Timestamp
				s.lastSignature = item.Signature
			}
		}
	}
}

func (s *ThoughtStore) saveToFile() {
	data, err := json.Marshal(s.cache)
	if err == nil {
		_ = os.WriteFile(s.filePath, data, 0644)
	}
}

func (s *ThoughtStore) Store(callId, toolName, signature string) {
	if signature == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	item := ThoughtItem{
		Signature: signature,
		Timestamp: time.Now(),
	}

	s.lastSignature = signature

	if callId != "" {
		s.cache[s.key(callId, toolName)] = item
		s.cache[s.key(callId, "")] = item
	}
	if toolName != "" {
		s.cache[s.key("", toolName)] = item
	}

	s.saveToFile()
}

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
	if toolName != "" {
		if item, ok := s.cache[s.key("", toolName)]; ok && item.Signature != "" {
			return item.Signature
		}
	}
	return ""
}

func (s *ThoughtStore) GetFallback() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastSignature != "" && len(s.lastSignature) >= 80 {
		return s.lastSignature
	}
	return ""
}
