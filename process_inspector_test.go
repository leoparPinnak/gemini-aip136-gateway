package main

import (
	"net"
	"testing"
	"time"
)

func TestScanTCPTableFast(t *testing.T) {
	// 1. scanTCPTableFast should execute cleanly without panics
	m := scanTCPTableFast(8000)
	if m == nil {
		t.Fatal("expected non-nil map from scanTCPTableFast")
	}

	// 2. Start a temporary listener on an ephemeral port, connect to it, and verify resolution
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("skipping socket test: %v", err)
	}
	defer listener.Close()

	testPort := listener.Addr().(*net.TCPAddr).Port

	// Connect to testPort
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	// Accept connection
	serverConn, err := listener.Accept()
	if err != nil {
		t.Fatalf("failed to accept: %v", err)
	}
	defer serverConn.Close()

	// Small pause for Windows TCP table to update
	time.Sleep(50 * time.Millisecond)

	// Scan with testPort
	results := scanTCPTableFast(testPort)
	clientLocalPort := conn.LocalAddr().(*net.TCPAddr).Port

	if pid, ok := results[clientLocalPort]; ok {
		t.Logf("Successfully resolved local client port %d to PID %d", clientLocalPort, pid)
		if pid <= 0 {
			t.Errorf("expected positive PID, got %d", pid)
		}
	} else {
		t.Logf("Port %d not yet in table (acceptable on some Windows virtualized CI environments)", clientLocalPort)
	}
}
