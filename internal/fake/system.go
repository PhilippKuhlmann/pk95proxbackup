package fake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"pk95proxbackup/internal/prox"
)

// Simulated SMART values, GitHub releases and configuration backups for the demo and tests.

// SmartATA builds the PBS answer of /disks/smart for an ATA disk.
func SmartATA(status string, reallocated, pending, hours, temp int) map[string]any {
	attr := func(id int, name string, v int) map[string]any {
		return map[string]any{"id": id, "name": name, "value": strconv.Itoa(v), "normalized": 100, "worst": 100, "threshold": 10}
	}
	return map[string]any{"status": status, "attributes": []map[string]any{
		attr(5, "Reallocated_Sector_Ct", reallocated), attr(9, "Power_On_Hours", hours),
		{"id": 194, "name": "Temperature_Celsius", "value": strconv.Itoa(temp) + " (Min/Max 18/45)", "normalized": 64},
		attr(197, "Current_Pending_Sector", pending), attr(198, "Offline_Uncorrectable", 0),
	}}
}

// SetSmart sets the SMART answer for a disk (not set = SMART not available).
func (p *PBS) SetSmart(disk string, v map[string]any) {
	p.mu.Lock()
	p.smart[disk] = v
	p.mu.Unlock()
}

func (p *PBS) serveSmart(w http.ResponseWriter, disk string) {
	v, ok := p.smart[disk]
	if !ok {
		data(w, map[string]any{"status": "unknown", "attributes": []any{}})
		return
	}
	data(w, v)
}

// AddBlob stores a backup that consists of one file (as proxmox-backup-client does with
// "name.conf:path").
func (p *PBS) AddBlob(st, typ, id string, t int64, file string, b []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.stores[st]
	if s == nil || !s.mounted {
		return false
	}
	s.snaps = append(s.snaps, prox.Snapshot{Type: typ, ID: id, Time: t, Size: int64(len(b)), Owner: "root@pam!pk95proxbackup"})
	p.blobs[st+"/"+typ+"/"+id+"/"+strconv.FormatInt(t, 10)+"/"+file] = b
	return true
}

func (p *PBS) blob(st, typ, id, t, file string) ([]byte, bool) {
	b, ok := p.blobs[st+"/"+typ+"/"+id+"/"+t+"/"+file]
	return b, ok
}

// NodeDisk is a disk of a simulated Proxmox VE node with its SMART answer.
type NodeDisk struct {
	Disk  prox.NodeDisk
	Smart map[string]any
}

// AddNodeDisk adds a physical disk to a node.
func (p *PVE) AddNodeDisk(node string, d prox.NodeDisk, smart map[string]any) {
	p.mu.Lock()
	if p.disks == nil {
		p.disks = map[string][]NodeDisk{}
	}
	p.disks[node] = append(p.disks[node], NodeDisk{Disk: d, Smart: smart})
	p.mu.Unlock()
}

func (p *PVE) serveDisks(w http.ResponseWriter, node string) {
	out := []prox.NodeDisk{}
	for _, d := range p.disks[node] {
		out = append(out, d.Disk)
	}
	data(w, out)
}

func (p *PVE) serveSmart(w http.ResponseWriter, node, dev string) {
	for _, d := range p.disks[node] {
		if d.Disk.DevPath == dev {
			data(w, d.Smart)
			return
		}
	}
	apiErr(w, 400, "no such disk")
}

// PVESmartATA builds the PVE answer of /disks/smart for an ATA disk.
func PVESmartATA(reallocated, pending, hours, temp int) map[string]any {
	attr := func(id int, name string, v int) map[string]any {
		return map[string]any{"id": strconv.Itoa(id), "name": name, "value": 100, "worst": 100, "threshold": 10, "raw": strconv.Itoa(v), "flags": "PO--CK"}
	}
	return map[string]any{"health": "PASSED", "type": "ata", "attributes": []map[string]any{
		attr(5, "Reallocated_Sector_Ct", reallocated), attr(9, "Power_On_Hours", hours), attr(194, "Temperature_Celsius", temp),
		attr(197, "Current_Pending_Sector", pending), attr(198, "Offline_Uncorrectable", 0),
	}}
}

// PVESmartNVMe builds the PVE answer of /disks/smart for an NVMe disk (smartctl text).
func PVESmartNVMe(used, mediaErrors, hours, temp int) map[string]any {
	return map[string]any{"health": "PASSED", "type": "text", "text": "SMART/Health Information (NVMe Log 0x02)\n" +
		"Critical Warning:                   0x00\n" +
		"Temperature:                        " + strconv.Itoa(temp) + " Celsius\n" +
		"Available Spare:                    100%\n" +
		"Percentage Used:                    " + strconv.Itoa(used) + "%\n" +
		"Power On Hours:                     " + strconv.Itoa(hours) + "\n" +
		"Media and Data Integrity Errors:    " + strconv.Itoa(mediaErrors) + "\n"}
}

// GitHub simulates the releases of the project: the newest one is Version.
type GitHub struct {
	*httptest.Server
	Version string
	Binary  []byte
	BadSum  bool // SHA256SUMS does not match the binary
}

// NewGitHub starts the simulated release server.
func NewGitHub(version string) *GitHub {
	g := &GitHub{Version: version, Binary: []byte("#!/bin/sh\necho " + version + "\n")}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tag := "v" + g.Version
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "html_url": g.URL + "/releases/tag/" + tag,
				"published_at": time.Now().Add(-26 * time.Hour).UTC().Format(time.RFC3339),
				"body":         "## Neu\n- Festplatten werden per SMART überwacht\n- Update per Klick\n- Die Konfiguration der Appliance wird täglich gesichert"})
		case strings.HasSuffix(r.URL.Path, "/releases/download/"+tag+"/SHA256SUMS"):
			sum := sha256.Sum256(g.Binary)
			if g.BadSum {
				sum = sha256.Sum256([]byte("something else"))
			}
			h := hex.EncodeToString(sum[:])
			_, _ = w.Write([]byte(h + "  pk95proxbackup-linux-amd64\n" + h + "  pk95proxbackup-linux-arm64\n"))
		case strings.Contains(r.URL.Path, "/releases/download/"+tag+"/pk95proxbackup-linux-"):
			_, _ = w.Write(g.Binary)
		default:
			http.NotFound(w, r)
		}
	}))
	return g
}

// NextMinor returns the version after v ("1.4.0" → "1.5.0").
func NextMinor(v string) string {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) < 2 {
		return "9.9.0"
	}
	maj, _ := strconv.Atoi(parts[0])
	min, _ := strconv.Atoi(parts[1])
	return strconv.Itoa(maj) + "." + strconv.Itoa(min+1) + ".0"
}
