package main

import (
	"sync"
	"time"
)

type ThoughtItem struct {
	Signature string
	Timestamp time.Time
}

type ThoughtStore struct {
	cache map[string]ThoughtItem
	mu    sync.RWMutex
}

var GlobalThoughtStore = &ThoughtStore{
	cache: make(map[string]ThoughtItem),
}

func (s *ThoughtStore) key(callId, toolName string) string {
	return callId + ":" + toolName
}

func (s *ThoughtStore) Store(callId, toolName, signature string) {
	if callId == "" && toolName == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	item := ThoughtItem{
		Signature: signature,
		Timestamp: time.Now(),
	}

	if callId != "" {
		s.cache[s.key(callId, toolName)] = item
		s.cache[s.key(callId, "")] = item
	}
	if toolName != "" {
		s.cache[s.key("", toolName)] = item
	}
}

func (s *ThoughtStore) Get(callId, toolName string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if callId != "" && toolName != "" {
		if item, ok := s.cache[s.key(callId, toolName)]; ok {
			return item.Signature
		}
	}
	if callId != "" {
		if item, ok := s.cache[s.key(callId, "")]; ok {
			return item.Signature
		}
	}
	if toolName != "" {
		if item, ok := s.cache[s.key("", toolName)]; ok {
			return item.Signature
		}
	}
	return ""
}
