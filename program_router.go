package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type ProgramRule struct {
	ID           string `json:"id"`
	PID          int    `json:"pid"`                    // Hedef PID (Örn: 90912). 0 ise desen bazlı genel kural.
	ProcessName  string `json:"process_name,omitempty"` // Süreç adı (Örn: node.exe (single_catalog_server.js))
	Name         string `json:"name"`                   // Açıklama / Kural Adı
	Pattern      string `json:"pattern,omitempty"`      // Fallback desen
	AccountID    string `json:"account_id"`
	AccountEmail string `json:"account_email"`
	AccountName  string `json:"account_name"`
	Enabled      bool   `json:"enabled"`
	CreatedAt    string `json:"created_at"`
}

type ProgramRouter struct {
	mu       sync.RWMutex
	rules    []ProgramRule
	filePath string
}

var GlobalProgramRouter *ProgramRouter

func InitProgramRouter(filePath string) {
	if filePath == "" {
		filePath = filepath.Join(".", "program_rules.json")
	}

	router := &ProgramRouter{
		rules:    make([]ProgramRule, 0),
		filePath: filePath,
	}
	GlobalProgramRouter = router

	_ = router.load()
}

func (r *ProgramRouter) load() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := os.ReadFile(r.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			r.rules = make([]ProgramRule, 0)
			return nil
		}
		return err
	}

	var rules []ProgramRule
	if err := json.Unmarshal(data, &rules); err != nil {
		return err
	}
	r.rules = rules
	return nil
}

func (r *ProgramRouter) saveLocked() error {
	data, err := json.MarshalIndent(r.rules, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.filePath, data, 0644)
}

func (r *ProgramRouter) GetRules() []ProgramRule {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]ProgramRule, len(r.rules))
	copy(result, r.rules)
	return result
}

// AddOrUpdateRule kural ekler veya günceller.
// KURAL: Bir PID'ye yalnızca TEK bir kural atanabilir!
// Eğer aynı PID için zaten bir kural varsa, o kural güncellenir.
func (r *ProgramRouter) AddOrUpdateRule(rule ProgramRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if rule.ID == "" {
		rule.ID = fmt.Sprintf("rule-%d", time.Now().UnixNano())
	}
	if rule.CreatedAt == "" {
		rule.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
	}

	found := false
	for i, existing := range r.rules {
		// Eğer rule.PID belirtilmişse ve bu PID zaten listede varsa -> güncelle (1 PID = 1 Kural)
		if (rule.PID > 0 && existing.PID == rule.PID) || (rule.ID != "" && existing.ID == rule.ID) {
			rule.ID = existing.ID // Var olan kural ID'sini koru
			r.rules[i] = rule
			found = true
			break
		}
	}
	if !found {
		r.rules = append(r.rules, rule)
	}

	return r.saveLocked()
}

func (r *ProgramRouter) DeleteRule(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	newRules := make([]ProgramRule, 0, len(r.rules))
	for _, rule := range r.rules {
		if rule.ID != id {
			newRules = append(newRules, rule)
		}
	}
	r.rules = newRules
	return r.saveLocked()
}

func (r *ProgramRouter) ToggleRule(id string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i := range r.rules {
		if r.rules[i].ID == id {
			r.rules[i].Enabled = enabled
			return r.saveLocked()
		}
	}
	return fmt.Errorf("kural bulunamadı: %s", id)
}

// RouteAccount gelen istemcinin PID, exeName ve displayName bilgilerine göre atanmış hesabı tespit eder.
func (r *ProgramRouter) RouteAccount(pid int, exeName, displayName string) (*Account, string) {
	r.mu.RLock()
	rulesCopy := make([]ProgramRule, len(r.rules))
	copy(rulesCopy, r.rules)
	r.mu.RUnlock()

	// 1. ÖNCELİK: Birebir PID Eşleşmesi (En yüksek öncelik)
	if pid > 0 {
		for _, rule := range rulesCopy {
			if rule.Enabled && rule.PID == pid {
				if GlobalAccountStore != nil {
					acc := GlobalAccountStore.GetAccountByID(rule.AccountID)
					if acc == nil && rule.AccountEmail != "" {
						acc = GlobalAccountStore.GetAccountByID(rule.AccountEmail)
					}
					if acc != nil {
						ruleLabel := rule.Name
						if ruleLabel == "" {
							ruleLabel = fmt.Sprintf("PID %d Özel Kuralı", pid)
						}
						return acc, ruleLabel
					}
				}
			}
		}
	}

	// 2. ÖNCELİK: İsim / Desen Eşleşmesi (PID tanımlı olmayan genel kurallar)
	lowerExe := strings.ToLower(exeName)
	lowerDisplay := strings.ToLower(displayName)

	for _, rule := range rulesCopy {
		if !rule.Enabled || rule.PID > 0 {
			continue // PID bazlı kurallar yukarıda kontrol edildi
		}
		pattern := strings.ToLower(strings.TrimSpace(rule.Pattern))
		if pattern == "" {
			continue
		}

		if strings.Contains(lowerDisplay, pattern) || strings.Contains(lowerExe, pattern) {
			if GlobalAccountStore != nil {
				acc := GlobalAccountStore.GetAccountByID(rule.AccountID)
				if acc == nil && rule.AccountEmail != "" {
					acc = GlobalAccountStore.GetAccountByID(rule.AccountEmail)
				}
				if acc != nil {
					return acc, rule.Name
				}
			}
		}
	}

	// 3. ÖNCELİK: Varsayılan aktif hesap
	if GlobalAccountStore != nil {
		active := GlobalAccountStore.GetActiveAccount()
		if active != nil {
			return active, "Varsayılan Aktif Hesap"
		}
	}

	return nil, "Tanımsız"
}
