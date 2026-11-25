package core

import (
	"fmt"
	"testing"
)

func TestParseServices(t *testing.T) {
	m, err := ParseServices("100: 443, 3306\n# Kommentar\n\n110:53 80;80\n")
	if err != nil || fmt.Sprint(m["100"]) != "[443 3306]" || fmt.Sprint(m["110"]) != "[53 80]" {
		t.Fatalf("%v %v", m, err)
	}
	for _, bad := range []string{"100 443", "100: 70000", "100:", "abc: 80"} {
		if _, err := ParseServices(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestListeningPorts(t *testing.T) {
	proc := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1
   1: 0100007F:0CEA 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2 1
   2: 0100007F:9C40 0100007F:0016 01 00000000:00000000 00:00000000 00000000     0        0 3 1
   0: 00000000000000000000000000000000:01BB 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000 0 0 4 1`
	got := listeningProc(proc)
	if !got[22] || !got[3306] || !got[443] || got[40000] || len(got) != 3 {
		t.Fatalf("proc: %v", got)
	}
	netstat := "\r\nAktive Verbindungen\r\n\r\n  Proto  Lokale Adresse         Remoteadresse          Status\r\n" +
		"  TCP    0.0.0.0:135            0.0.0.0:0              LISTENING\r\n" +
		"  TCP    [::]:3389              [::]:0                 LISTENING\r\n" +
		"  TCP    10.0.0.5:49152         10.0.0.9:443           ESTABLISHED\r\n" +
		"  UDP    0.0.0.0:53             *:*                    \r\n"
	got = listeningNetstat(netstat)
	if !got[135] || !got[3389] || got[49152] || got[53] || len(got) != 2 {
		t.Fatalf("netstat: %v", got)
	}
	if s := portsText([]int{3306}, false); s != "Port 3306 lauscht nicht" {
		t.Fatal(s)
	}
	if s := portsText([]int{53, 389}, true); s != "Dienste antworten (Ports 53, 389)" {
		t.Fatal(s)
	}
}
