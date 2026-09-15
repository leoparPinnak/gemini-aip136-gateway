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
	Name         string `json:"name"`
	Pattern      string `json:"pattern"`
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
		if existing.ID == rule.ID {
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

// RouteAccount gelen istemci süreç bilgilerine (exeName ve displayName) göre atanmış hesabı tespit eder.
func (r *ProgramRouter) RouteAccount(exeName, displayName string) (*Account, string) {
	r.mu.RLock()
	rulesCopy := make([]ProgramRule, len(r.rules))
	copy(rulesCopy, r.rules)
	r.mu.RUnlock()

	lowerExe := strings.ToLower(exeName)
	lowerDisplay := strings.ToLower(displayName)

	for _, rule := range rulesCopy {
		if !rule.Enabled {
			continue
		}
		pattern := strings.ToLower(strings.TrimSpace(rule.Pattern))
		if pattern == "" {
			continue
		}

		// Pattern eşleşmesi (exe adında veya görünen ad/script adında)
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

	// Varsayılan aktif hesap
	if GlobalAccountStore != nil {
		active := GlobalAccountStore.GetActiveAccount()
		if active != nil {
			return active, "Varsayılan Aktif Hesap"
		}
	}

	return nil, "Tanımsız"
}
