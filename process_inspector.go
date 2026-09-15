package main

import (
	"bufio"
	"bytes"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ProcessInfo struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
}

type ProcessInspector struct {
	portToPID  map[int]int
	pidToName  map[int]string
	mu         sync.RWMutex
	lastUpdate time.Time
}

var GlobalProcessInspector *ProcessInspector

func InitProcessInspector() {
	inspector := &ProcessInspector{
		portToPID: make(map[int]int),
		pidToName: make(map[int]string),
	}
	GlobalProcessInspector = inspector

	// İlk taramayı hemen yap
	inspector.refresh()

	// Arka planda 2.5 saniyede bir güncelle (HTTP isteklerini hiç bekletmez, 0ms erişim sağlar)
	go func() {
		ticker := time.NewTicker(2500 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			inspector.refresh()
		}
	}()
}

func (pi *ProcessInspector) refresh() {
	// 1. Process listesi (tasklist)
	cmdTasklist := exec.Command("tasklist", "/FO", "CSV", "/NH")
	outTasklist, err := cmdTasklist.Output()
	newPidToName := make(map[int]string)
	if err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(outTasklist))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			parts := strings.Split(line, "\",\"")
			if len(parts) >= 2 {
				name := strings.Trim(parts[0], "\"")
				pidStr := strings.Trim(parts[1], "\"")
				if pid, err := strconv.Atoi(pidStr); err == nil {
					newPidToName[pid] = name
				}
			}
		}
	}

	// 2. TCP bağlantı listesi (netstat)
	cmdNetstat := exec.Command("netstat", "-ano", "-p", "tcp")
	outNetstat, err := cmdNetstat.Output()
	newPortToPID := make(map[int]int)
	if err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(outNetstat))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "TCP") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 5 {
				localAddr := fields[1]
				pidStr := fields[len(fields)-1]
				if pid, err := strconv.Atoi(pidStr); err == nil {
					if pid == os.Getpid() {
						continue // Gateway'in kendi soketlerini istemci haritasına yazmasın
					}
					// localAddr portu
					if _, portStr, err := net.SplitHostPort(localAddr); err == nil {
						if p, err := strconv.Atoi(portStr); err == nil {
							newPortToPID[p] = pid
						}
					}
				}
			}
		}
	}

	pi.mu.Lock()
	if len(newPidToName) > 0 {
		pi.pidToName = newPidToName
	}
	if len(newPortToPID) > 0 {
		pi.portToPID = newPortToPID
	}
	pi.lastUpdate = time.Now()
	pi.mu.Unlock()
}

func (pi *ProcessInspector) ResolveClientProcess(remoteAddr string) (int, string) {
	_, portStr, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return 0, "Bilinmeyen Program"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, "Bilinmeyen Program"
	}

	pi.mu.RLock()
	pid, ok := pi.portToPID[port]
	var name string
	if ok {
		name = pi.pidToName[pid]
	}
	pi.mu.RUnlock()

	if ok && pid > 0 {
		if name == "" {
			name = "Bilinmeyen (PID: " + strconv.Itoa(pid) + ")"
		}
		return pid, name
	}

	myPID := os.Getpid()

	// 1. Anlık port haritasında yoksa hemen aktif TCP tablosunda ara
	cmd := exec.Command("netstat", "-ano", "-p", "tcp")
	if out, err := cmd.Output(); err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(out))
		for scanner.Scan() {
			line := scanner.Text()
			fields := strings.Fields(line)
			if len(fields) >= 5 && strings.HasPrefix(line, "TCP") {
				localAddr := fields[1]
				// İstemcinin Local Address portu bizim aradığımız port mu?
				if strings.HasSuffix(localAddr, ":"+portStr) {
					if foundPid, err := strconv.Atoi(fields[len(fields)-1]); err == nil && foundPid > 0 {
						if foundPid == myPID {
							continue // Gateway'in kendi sunucu sürecini istemci olarak işaretlemesin
						}
						pi.mu.RLock()
						procName := pi.pidToName[foundPid]
						pi.mu.RUnlock()
						if procName == "" {
							cmdTask := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(foundPid), "/FO", "CSV", "/NH")
							if taskOut, err := cmdTask.Output(); err == nil {
								parts := strings.Split(string(taskOut), "\",\"")
								if len(parts) >= 2 {
									procName = strings.Trim(parts[0], "\"\r\n ")
								}
							}
						}
						if procName == "" {
							procName = "Program (PID: " + strconv.Itoa(foundPid) + ")"
						}
						pi.mu.Lock()
						pi.portToPID[port] = foundPid
						pi.pidToName[foundPid] = procName
						pi.mu.Unlock()
						return foundPid, procName
					}
				}
			}
		}
	}

	return 0, "İstemci Uygulama (Port: " + portStr + ")"
}
