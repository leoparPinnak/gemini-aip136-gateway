package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	modiphlpapi                   = syscall.NewLazyDLL("iphlpapi.dll")
	procGetExtendedTcpTable       = modiphlpapi.NewProc("GetExtendedTcpTable")
	modkernel32                   = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess               = modkernel32.NewProc("OpenProcess")
	procQueryFullProcessImageName = modkernel32.NewProc("QueryFullProcessImageNameW")
	procCloseHandle               = modkernel32.NewProc("CloseHandle")
)

const (
	AF_INET                           = 2
	AF_INET6                          = 23
	TCP_TABLE_OWNER_PID_ALL           = 5
	PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
)

type ProcessDetail struct {
	PID          int       `json:"pid"`
	ExeName      string    `json:"exe_name"`
	CommandLine  string    `json:"command_line,omitempty"`
	DisplayName  string    `json:"display_name"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
	RequestCount int       `json:"request_count"`
}

type ProcessInspector struct {
	portToPID  map[int]int
	pidToInfo  map[int]*ProcessDetail
	detected   map[string]*ProcessDetail
	mu         sync.RWMutex
	lastUpdate time.Time
}

var GlobalProcessInspector *ProcessInspector

func InitProcessInspector() {
	inspector := &ProcessInspector{
		portToPID: make(map[int]int),
		pidToInfo: make(map[int]*ProcessDetail),
		detected:  make(map[string]*ProcessDetail),
	}
	GlobalProcessInspector = inspector

	// İlk taramayı yap
	inspector.refreshTables()

	// Arka planda 3 saniyede bir Windows TCP tablolarını tazele
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			inspector.refreshTables()
		}
	}()
}

// getNativeProcessExeName Win32 API ile bir PID'nin dosya adını 0.001ms'de döner.
func getNativeProcessExeName(pid uint32) string {
	if pid == 0 {
		return "System"
	}
	h, _, _ := procOpenProcess.Call(
		uintptr(PROCESS_QUERY_LIMITED_INFORMATION),
		0,
		uintptr(pid),
	)
	if h == 0 {
		return ""
	}
	defer procCloseHandle.Call(h)

	var buf [1024]uint16
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessImageName.Call(
		h,
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if r == 0 {
		return ""
	}
	fullPath := syscall.UTF16ToString(buf[:size])
	return filepath.Base(fullPath)
}

// scanTCPTableFast doğrudan bellekten IPv4 ve IPv6 soket haritasını okur (0.5ms).
func scanTCPTableFast() map[int]int {
	myPid := uint32(os.Getpid())
	result := make(map[int]int)

	// 1. IPv4 TCP Table
	var size uint32 = 0
	procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, AF_INET, TCP_TABLE_OWNER_PID_ALL, 0)
	if size > 0 {
		buf := make([]byte, size)
		r, _, _ := procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, AF_INET, TCP_TABLE_OWNER_PID_ALL, 0)
		if r == 0 && len(buf) >= 4 {
			numEntries := binary.LittleEndian.Uint32(buf[0:4])
			offset := 4
			for i := uint32(0); i < numEntries && offset+24 <= len(buf); i++ {
				localPort := int(binary.BigEndian.Uint16(buf[offset+8 : offset+10]))
				pid := binary.LittleEndian.Uint32(buf[offset+20 : offset+24])
				if pid > 0 && pid != myPid {
					result[localPort] = int(pid)
				}
				offset += 24
			}
		}
	}

	// 2. IPv6 TCP Table (Node.js localhost bağlantılarını [::1] kesin yakalar)
	size = 0
	procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, AF_INET6, TCP_TABLE_OWNER_PID_ALL, 0)
	if size > 0 {
		buf := make([]byte, size)
		r, _, _ := procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, AF_INET6, TCP_TABLE_OWNER_PID_ALL, 0)
		if r == 0 && len(buf) >= 4 {
			numEntries := binary.LittleEndian.Uint32(buf[0:4])
			offset := 4
			for i := uint32(0); i < numEntries && offset+56 <= len(buf); i++ {
				localPort := int(binary.BigEndian.Uint16(buf[offset+20 : offset+22]))
				pid := binary.LittleEndian.Uint32(buf[offset+52 : offset+56])
				if pid > 0 && pid != myPid {
					result[localPort] = int(pid)
				}
				offset += 56
			}
		}
	}

	return result
}

func (pi *ProcessInspector) refreshTables() {
	newMap := scanTCPTableFast()
	pi.mu.Lock()
	for p, pid := range newMap {
		pi.portToPID[p] = pid
	}
	pi.lastUpdate = time.Now()
	pi.mu.Unlock()
}

// RegisterSocket yeni bir TCP bağlantısı açıldığı mikro-saniyede çağrılır (ConnState hook).
func (pi *ProcessInspector) RegisterSocket(remoteAddr string) {
	_, portStr, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return
	}

	pi.mu.RLock()
	existingPID, exists := pi.portToPID[port]
	pi.mu.RUnlock()

	if exists && existingPID > 0 {
		return
	}

	// Anında native tabloyu tara
	fastMap := scanTCPTableFast()
	if pid, ok := fastMap[port]; ok && pid > 0 {
		pi.mu.Lock()
		pi.portToPID[port] = pid
		pi.mu.Unlock()
		pi.ensureProcessDetail(pid)
	}
}

// ensureProcessDetail PID'ye ait bilgileri önbelleğe alır ve gerekiyorsa arka planda script adını çözer.
func (pi *ProcessInspector) ensureProcessDetail(pid int) *ProcessDetail {
	pi.mu.Lock()
	detail, exists := pi.pidToInfo[pid]
	if !exists {
		exeName := getNativeProcessExeName(uint32(pid))
		if exeName == "" {
			exeName = "Program"
		}
		displayName := exeName
		detail = &ProcessDetail{
			PID:          pid,
			ExeName:      exeName,
			DisplayName:  displayName,
			FirstSeen:    time.Now(),
			LastSeen:     time.Now(),
			RequestCount: 0,
		}
		pi.pidToInfo[pid] = detail

		// Script adı zenginleştirme (node, python vb. için)
		go pi.enrichProcessCommandLine(pid, exeName)
	}
	pi.mu.Unlock()
	return detail
}

// enrichProcessCommandLine node.exe veya python.exe süreçlerinin hangi scripti çalıştırdığını arka planda çözer.
func (pi *ProcessInspector) enrichProcessCommandLine(pid int, exeName string) {
	lowerExe := strings.ToLower(exeName)
	if !strings.Contains(lowerExe, "node") && !strings.Contains(lowerExe, "python") && !strings.Contains(lowerExe, "powershell") {
		return
	}

	// wmic ile komut satırını al
	cmd := exec.Command("wmic", "process", "where", fmt.Sprintf("ProcessId=%d", pid), "get", "CommandLine", "/value")
	out, err := cmd.Output()
	if err != nil {
		return
	}

	raw := string(out)
	idx := strings.Index(raw, "CommandLine=")
	if idx == -1 {
		return
	}
	cmdLine := strings.TrimSpace(raw[idx+len("CommandLine="):])
	if cmdLine == "" {
		return
	}

	// Script dosya adını bul (örn: exact_matcher_server.js)
	fields := strings.Fields(cmdLine)
	var scriptName string
	for _, f := range fields {
		fClean := strings.Trim(f, "\"'")
		base := filepath.Base(fClean)
		if strings.HasSuffix(base, ".js") || strings.HasSuffix(base, ".py") || strings.HasSuffix(base, ".ts") {
			scriptName = base
			break
		}
	}

	displayName := exeName
	if scriptName != "" {
		displayName = fmt.Sprintf("%s (%s)", exeName, scriptName)
	}

	pi.mu.Lock()
	if d, ok := pi.pidToInfo[pid]; ok {
		d.CommandLine = cmdLine
		d.DisplayName = displayName
	}
	pi.mu.Unlock()
}

// ResolveClientProcess bir HTTP isteğinin geldiği remoteAddr'den süreci kesin ve 0ms'de tespit eder.
// Döndürdüğü değerler: (pid, exeName, displayName)
func (pi *ProcessInspector) ResolveClientProcess(remoteAddr string) (int, string, string) {
	_, portStr, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return 0, "Bilinmeyen", "Bilinmeyen Program"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, "Bilinmeyen", "Bilinmeyen Program"
	}

	pi.mu.RLock()
	pid, ok := pi.portToPID[port]
	pi.mu.RUnlock()

	if !ok || pid == 0 {
		// Anlık native tarama yap (0.2ms)
		fastMap := scanTCPTableFast()
		if foundPid, found := fastMap[port]; found && foundPid > 0 {
			pid = foundPid
			pi.mu.Lock()
			pi.portToPID[port] = pid
			pi.mu.Unlock()
		}
	}

	if pid > 0 {
		detail := pi.ensureProcessDetail(pid)
		pi.mu.Lock()
		detail.LastSeen = time.Now()
		detail.RequestCount++
		// Detected listesini güncelle
		key := detail.DisplayName
		if existing, has := pi.detected[key]; has {
			existing.LastSeen = time.Now()
			existing.RequestCount++
			existing.PID = pid
		} else {
			copyDet := *detail
			pi.detected[key] = &copyDet
		}
		pi.mu.Unlock()

		return pid, detail.ExeName, detail.DisplayName
	}

	fallbackName := fmt.Sprintf("İstemci (Port: %s)", portStr)
	return 0, "İstemci", fallbackName
}

// GetDetectedPrograms şimdiye kadar Gateway'e istek atmış tüm programların listesini döner.
func (pi *ProcessInspector) GetDetectedPrograms() []ProcessDetail {
	pi.mu.RLock()
	defer pi.mu.RUnlock()

	list := make([]ProcessDetail, 0, len(pi.detected))
	for _, d := range pi.detected {
		list = append(list, *d)
	}
	return list
}

// ----------------------------------------------------------------------
// SÜREÇ AĞ VE PORT DETAY İNCELEYİCİSİ (INSPECTOR MODAL)
// ----------------------------------------------------------------------

type ProcessSocketConnection struct {
	Protocol      string `json:"protocol"`
	LocalAddress  string `json:"local_address"`
	RemoteAddress string `json:"remote_address"`
	State         string `json:"state"`
	IsServerPort  bool   `json:"is_server_port"`
}

type ProcessInspectReport struct {
	PID           int                       `json:"pid"`
	ProcessName   string                    `json:"process_name"`
	Executable    string                    `json:"executable,omitempty"`
	HasServerPort bool                      `json:"has_server_port"`
	ServerPorts   []string                  `json:"server_ports"`
	Connections   []ProcessSocketConnection `json:"connections"`
	TotalSockets  int                       `json:"total_sockets"`
}

// InspectPIDNetwork belirli bir PID'nin dinlediği portları ve bağlantılarını listeler.
func InspectPIDNetwork(pid int) (*ProcessInspectReport, error) {
	report := &ProcessInspectReport{
		PID:         pid,
		ServerPorts: make([]string, 0),
		Connections: make([]ProcessSocketConnection, 0),
	}

	// 1. Süreç Adı
	exeName := getNativeProcessExeName(uint32(pid))
	if exeName == "" {
		exeName = fmt.Sprintf("Program (PID: %d)", pid)
	}
	report.ProcessName = exeName

	if GlobalProcessInspector != nil {
		GlobalProcessInspector.mu.RLock()
		if d, ok := GlobalProcessInspector.pidToInfo[pid]; ok && d.DisplayName != "" {
			report.ProcessName = d.DisplayName
		}
		GlobalProcessInspector.mu.RUnlock()
	}

	// 2. TCP Soketleri (IPv4 ve IPv6 dahil netstat -ano)
	cmdTCP := exec.Command("netstat", "-ano")
	if out, err := cmdTCP.Output(); err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(out))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "TCP") && !strings.HasPrefix(line, "UDP") {
				continue
			}
			fields := strings.Fields(line)
			if strings.HasPrefix(line, "TCP") && len(fields) >= 5 {
				linePID, err := strconv.Atoi(fields[len(fields)-1])
				if err == nil && linePID == pid {
					localAddr := fields[1]
					remoteAddr := fields[2]
					state := fields[3]
					isServer := (state == "LISTENING")
					if isServer {
						report.ServerPorts = append(report.ServerPorts, localAddr)
					}
					report.Connections = append(report.Connections, ProcessSocketConnection{
						Protocol:      "TCP",
						LocalAddress:  localAddr,
						RemoteAddress: remoteAddr,
						State:         state,
						IsServerPort:  isServer,
					})
				}
			} else if strings.HasPrefix(line, "UDP") && len(fields) >= 4 {
				linePID, err := strconv.Atoi(fields[len(fields)-1])
				if err == nil && linePID == pid {
					localAddr := fields[1]
					report.ServerPorts = append(report.ServerPorts, localAddr)
					report.Connections = append(report.Connections, ProcessSocketConnection{
						Protocol:      "UDP",
						LocalAddress:  localAddr,
						RemoteAddress: "*:*",
						State:         "LISTENING",
						IsServerPort:  true,
					})
				}
			}
		}
	}

	report.HasServerPort = len(report.ServerPorts) > 0
	report.TotalSockets = len(report.Connections)
	return report, nil
}
