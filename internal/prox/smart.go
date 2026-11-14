package prox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// SmartReport is the health of one disk, read from SMART and reduced to what matters.
// Counters are -1 when the disk does not report them.
type SmartReport struct {
	Health        string `json:"health"`  // passed, failed, unknown
	Wearout       int    `json:"wearout"` // remaining life in percent (SSD), -1 = unknown
	Reallocated   int64  `json:"reallocated"`
	Pending       int64  `json:"pending"`
	Uncorrectable int64  `json:"uncorrectable"`
	MediaErrors   int64  `json:"media_errors"` // NVMe
	PowerOnHours  int64  `json:"power_on_hours"`
	Temperature   int64  `json:"temperature"`
}

// NewSmartReport returns a report where nothing is known.
func NewSmartReport() SmartReport {
	return SmartReport{Health: "unknown", Wearout: -1, Reallocated: -1, Pending: -1, Uncorrectable: -1, MediaErrors: -1, PowerOnHours: -1, Temperature: -1}
}

// flexString accepts a JSON string or number.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = flexString(s)
		return nil
	}
	*f = flexString(strings.Trim(string(b), `"`))
	return nil
}

type smartAttr struct {
	ID    flexString `json:"id"`
	Name  string     `json:"name"`
	Value flexString `json:"value"`
	Raw   flexString `json:"raw"`
}

var leadingInt = regexp.MustCompile(`^\s*(-?[\d,.]+)`)

// rawNumber reads the leading number of a raw SMART value ("35 (Min/Max 20/45)", "1,234", "8760h+12m").
func rawNumber(s string) int64 {
	m := leadingInt.FindStringSubmatch(s)
	if m == nil {
		return -1
	}
	v := strings.ReplaceAll(m[1], ",", "")
	v, _, _ = strings.Cut(v, ".")
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// apply reads the counters from ATA attributes (by id) or NVMe health values (by name, id 0).
func (r *SmartReport) apply(id int, name, raw string) {
	n := rawNumber(raw)
	if n < 0 {
		return
	}
	key := strings.ToLower(strings.NewReplacer(" ", "_", "-", "_").Replace(strings.TrimSpace(name)))
	switch {
	case id == 5:
		r.Reallocated = n
	case id == 197:
		r.Pending = n
	case id == 198:
		r.Uncorrectable = n
	case id == 187 && r.Uncorrectable < 0:
		r.Uncorrectable = n
	case id == 9:
		r.PowerOnHours = n
	case id == 194 || (id == 190 && r.Temperature < 0):
		r.Temperature = n
	case id == 0 && (key == "media_errors" || key == "media_and_data_integrity_errors"):
		r.MediaErrors = n
	case id == 0 && key == "power_on_hours":
		r.PowerOnHours = n
	case id == 0 && key == "temperature":
		r.Temperature = n
	case id == 0 && key == "percentage_used" && r.Wearout < 0:
		w := 100 - n
		if w < 0 {
			w = 0
		}
		r.Wearout = int(w)
	}
}

func normHealth(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "PASSED", "OK":
		return "passed"
	case "FAILED":
		return "failed"
	}
	return "unknown"
}

// Smart reads the SMART values of a disk of the Backup Server ("sdc").
func (p *PBS) Smart(ctx context.Context, disk string) (SmartReport, error) {
	var out struct {
		Status     string      `json:"status"`
		Wearout    *float64    `json:"wearout"`
		Attributes []smartAttr `json:"attributes"`
	}
	r := NewSmartReport()
	if err := p.Do(ctx, http.MethodGet, "/nodes/localhost/disks/smart", Params{"disk": disk}, &out); err != nil {
		return r, err
	}
	r.Health = normHealth(out.Status)
	if out.Wearout != nil {
		r.Wearout = int(*out.Wearout)
	}
	for _, a := range out.Attributes {
		id, _ := strconv.Atoi(strings.TrimSpace(string(a.ID)))
		// PBS puts the raw value into "value".
		r.apply(id, a.Name, string(a.Value))
	}
	return r, nil
}

// NodeDisk is an entry of /nodes/{node}/disks/list in Proxmox VE.
type NodeDisk struct {
	DevPath string     `json:"devpath"`
	Model   string     `json:"model"`
	Vendor  string     `json:"vendor"`
	Serial  string     `json:"serial"`
	Size    int64      `json:"size"`
	Type    string     `json:"type"` // hdd, ssd, nvme, usb
	Health  string     `json:"health"`
	Wearout flexString `json:"wearout"` // number or "N/A"
	Used    string     `json:"used"`
}

// NodeDisks lists the physical disks of a Proxmox VE node (with SMART health).
func (p *PVE) NodeDisks(ctx context.Context, node string) ([]NodeDisk, error) {
	var out []NodeDisk
	err := p.Do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/disks/list", nil, &out)
	return out, err
}

// VolumeGroup is an LVM volume group of a node with its physical volumes (/dev/sdb3 …).
type VolumeGroup struct {
	Name string
	PVs  []string
}

// NodeLVM lists the LVM volume groups of a node (GET /nodes/{node}/disks/lvm).
func (p *PVE) NodeLVM(ctx context.Context, node string) ([]VolumeGroup, error) {
	var tree struct {
		Children []struct {
			Name     string `json:"name"`
			Children []struct {
				Name string `json:"name"`
			} `json:"children"`
		} `json:"children"`
	}
	if err := p.Do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/disks/lvm", nil, &tree); err != nil {
		return nil, err
	}
	out := []VolumeGroup{}
	for _, vg := range tree.Children {
		g := VolumeGroup{Name: vg.Name}
		for _, pv := range vg.Children {
			g.PVs = append(g.PVs, pv.Name)
		}
		out = append(out, g)
	}
	return out, nil
}

type zfsNode struct {
	Name     string    `json:"name"`
	Children []zfsNode `json:"children"`
}

// NodeZFSDevices returns the devices of a ZFS pool (the leaves of its vdev tree, e.g. "sdb",
// "nvme-Samsung_SSD_…-part3").
func (p *PVE) NodeZFSDevices(ctx context.Context, node, pool string) ([]string, error) {
	var tree zfsNode
	if err := p.Do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/disks/zfs/"+url.PathEscape(pool), nil, &tree); err != nil {
		return nil, err
	}
	var out []string
	var walk func(n zfsNode, top bool)
	walk = func(n zfsNode, top bool) {
		if len(n.Children) == 0 && !top {
			out = append(out, n.Name)
		}
		for _, c := range n.Children {
			walk(c, false)
		}
	}
	walk(tree, true)
	return out, nil
}

// NodeDirectory is a mounted directory storage of a node (GET /nodes/{node}/disks/directory).
type NodeDirectory struct {
	Path   string `json:"path"`
	Device string `json:"device"`
}

// NodeDirectories lists the directory storages of a node with their device.
func (p *PVE) NodeDirectories(ctx context.Context, node string) ([]NodeDirectory, error) {
	var out []NodeDirectory
	err := p.Do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/disks/directory", nil, &out)
	return out, err
}

var nvmeLine = regexp.MustCompile(`^([A-Za-z][A-Za-z /-]+?):\s+(.+)$`)

// NodeSmart reads the SMART values of a disk of a Proxmox VE node.
func (p *PVE) NodeSmart(ctx context.Context, node string, d NodeDisk) (SmartReport, error) {
	var out struct {
		Health     string      `json:"health"`
		Text       string      `json:"text"`
		Attributes []smartAttr `json:"attributes"`
	}
	r := NewSmartReport()
	r.Health = normHealth(d.Health)
	if w, err := strconv.ParseFloat(strings.TrimSpace(string(d.Wearout)), 64); err == nil {
		r.Wearout = int(w)
	}
	if err := p.Do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/disks/smart", Params{"disk": d.DevPath}, &out); err != nil {
		return r, err
	}
	if h := normHealth(out.Health); h != "unknown" {
		r.Health = h
	}
	for _, a := range out.Attributes {
		id, _ := strconv.Atoi(strings.TrimSpace(string(a.ID)))
		// PVE puts the normalized value into "value" and the raw one into "raw".
		r.apply(id, a.Name, string(a.Raw))
	}
	// NVMe: smartctl text ("Media and Data Integrity Errors:    0").
	for _, line := range strings.Split(out.Text, "\n") {
		if m := nvmeLine.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			r.apply(0, m[1], m[2])
		}
	}
	return r, nil
}
