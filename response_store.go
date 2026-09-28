package main

import (
	"container/list"
	"sync"
	"time"
)

// ResponseState, /v1/responses durumlu akışı (stateful Responses) için sunucu tarafı
// sohbet durumudur. previous_response_id ile gelen isteklerde Contents yeniden kullanılır:
// thoughtSignature'lar ve ham args bayt-bayt sadık tutulduğundan Google'ın örtük
// KV-cache prefix'i kırılmaz (cache HIT korunur).
type ResponseState struct {
	ResponseID   string
	SessionID    string
	SystemPrompt string
	Contents     []GeminiContent        // bu yanıttan SONRAKİ sohbet durumu (input turları + model çıktısı)
	ResponseObj  map[string]interface{} // GET /v1/responses/{id} için tam yanıt nesnesi
	CreatedAt    time.Time
}

// responseStore, LRU sınırlı (varsayılan 300 giriş) yanıt durumu deposudur.
// RAM'de tutulur; gateway yeniden başlarsa eski zincirler düşer — istemci tam
// replay yapıyorsa (DSH varsayılanı) bu zararsızdır.
type responseStore struct {
	mu    sync.Mutex
	order *list.List // front = en yeni; eleman değeri ResponseState
	items map[string]*list.Element
	max   int
}

var GlobalResponseStore = newResponseStore(300)

func newResponseStore(max int) *responseStore {
	return &responseStore{order: list.New(), items: make(map[string]*list.Element), max: max}
}

// Save, durumu kaydeder; aynı response_id varsa üzerine yazar (LRU'da öne alır).
func (s *responseStore) Save(st ResponseState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st.CreatedAt.IsZero() {
		st.CreatedAt = time.Now()
	}
	if el, ok := s.items[st.ResponseID]; ok {
		el.Value = st
		s.order.MoveToFront(el)
		return
	}
	el := s.order.PushFront(st)
	s.items[st.ResponseID] = el
	for s.order.Len() > s.max {
		oldest := s.order.Back()
		if oldest == nil {
			break
		}
		s.order.Remove(oldest)
		delete(s.items, oldest.Value.(ResponseState).ResponseID)
	}
}

// Get, response_id ile kayıtlı durumu döndürür (LRU'da öne alır).
func (s *responseStore) Get(id string) (ResponseState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	el, ok := s.items[id]
	if !ok {
		return ResponseState{}, false
	}
	s.order.MoveToFront(el)
	return el.Value.(ResponseState), true
}

// Delete, kayıtlı durumu siler (DELETE /v1/responses/{id}).
func (s *responseStore) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	el, ok := s.items[id]
	if !ok {
		return false
	}
	s.order.Remove(el)
	delete(s.items, id)
	return true
}
