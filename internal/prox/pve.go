package prox

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// PVE is a client for Proxmox VE.
type PVE struct{ *Client }

// NewPVE connects with an API token (tokenID = user@realm!token).
func NewPVE(host string, port int, fingerprint, tokenID, secret string) *PVE {
	return &PVE{newClient(host, port, fingerprint, "PVEAPIToken="+tokenID+"="+secret)}
}

// PVEVersion is GET /version.
type PVEVersion struct {
	Version string `json:"version"`
	Release string `json:"release"`
}

// Version returns the version.
func (p *PVE) Version(ctx context.Context) (PVEVersion, error) {
	var v PVEVersion
	err := p.Do(ctx, http.MethodGet, "/version", nil, &v)
	return v, err
}

// Node is an entry of /nodes.
type Node struct {
	Node   string  `json:"node"`
	Status string  `json:"status"`
	CPU    float64 `json:"cpu"`
	MaxCPU int     `json:"maxcpu"`
	Mem    int64   `json:"mem"`
	MaxMem int64   `json:"maxmem"`
	Uptime int64   `json:"uptime"`
}

// Nodes lists the cluster nodes.
func (p *PVE) Nodes(ctx context.Context) ([]Node, error) {
	var out []Node
	err := p.Do(ctx, http.MethodGet, "/nodes", nil, &out)
	return out, err
}

// Guest is a VM or container from /cluster/resources.
type Guest struct {
	VMID     int     `json:"vmid"`
	Name     string  `json:"name"`
	Node     string  `json:"node"`
	Type     string  `json:"type"` // qemu, lxc
	Status   string  `json:"status"`
	Template int     `json:"template"`
	MaxDisk  int64   `json:"maxdisk"`
	MaxMem   int64   `json:"maxmem"`
	CPU      float64 `json:"cpu"`
	Tags     string  `json:"tags"`
}

// Guests lists all VMs and containers of the cluster.
func (p *PVE) Guests(ctx context.Context) ([]Guest, error) {
	var out []Guest
	err := p.Do(ctx, http.MethodGet, "/cluster/resources", Params{"type": "vm"}, &out)
	return out, err
}

// NotBackedUp lists guests that are not part of any backup job.
func (p *PVE) NotBackedUp(ctx context.Context) ([]Guest, error) {
	var out []Guest
	err := p.Do(ctx, http.MethodGet, "/cluster/backup-info/not-backed-up", nil, &out)
	return out, err
}

// Storage is an entry of /storage (cluster storage configuration).
type Storage struct {
	Storage     string `json:"storage"`
	Type        string `json:"type"`
	Content     string `json:"content"`
	Server      string `json:"server,omitempty"`
	Port        int    `json:"port,omitempty"`
	Datastore   string `json:"datastore,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	Username    string `json:"username,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	// EncryptionKey is the fingerprint of the key backups are encrypted with ("1" for a key
	// without fingerprint, empty: not encrypted).
	EncryptionKey string `json:"encryption-key,omitempty"`
	PruneBackups  string `json:"prune-backups,omitempty"` // retention Proxmox VE applies after each backup
	Disable       int    `json:"disable,omitempty"`
	Nodes         string `json:"nodes,omitempty"`
	Shared        int    `json:"shared,omitempty"`
}

// Storages lists the storage configuration.
func (p *PVE) Storages(ctx context.Context) ([]Storage, error) {
	var out []Storage
	err := p.Do(ctx, http.MethodGet, "/storage", nil, &out)
	return out, err
}

// NodeStorage is an entry of /nodes/{node}/storage.
type NodeStorage struct {
	Storage string `json:"storage"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Active  int    `json:"active"`
	Enabled int    `json:"enabled"`
	Total   int64  `json:"total"`
	Used    int64  `json:"used"`
	Avail   int64  `json:"avail"`
}

// NodeStorages lists the storages of a node, optionally filtered by content type.
func (p *PVE) NodeStorages(ctx context.Context, node, content string) ([]NodeStorage, error) {
	var out []NodeStorage
	err := p.Do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/storage", Params{"content": emptyNil(content), "enabled": 1}, &out)
	return out, err
}

// AddPBSStorage adds a PBS datastore as storage to the cluster. encKey is the key file (JSON)
// new backups are encrypted with, empty for none.
func (p *PVE) AddPBSStorage(ctx context.Context, name, server string, port int, datastore, username, password, fingerprint, namespace, encKey string) error {
	params := Params{
		"storage": name, "type": "pbs", "server": server, "datastore": datastore, "username": username,
		"password": password, "fingerprint": fingerprint, "content": "backup", "namespace": emptyNil(namespace),
		"encryption-key": emptyNil(encKey),
	}
	if port != 0 && port != 8007 {
		params["port"] = port
	}
	return p.Do(ctx, http.MethodPost, "/storage", params, nil)
}

// SetStorageKey sets the key file (JSON) a PBS storage encrypts new backups with; an empty key
// switches encryption off. Proxmox VE keeps the key in /etc/pve/priv/storage/<name>.enc.
func (p *PVE) SetStorageKey(ctx context.Context, name, encKey string) error {
	params := Params{"encryption-key": encKey}
	if encKey == "" {
		params = Params{"delete": "encryption-key"}
	}
	return p.Do(ctx, http.MethodPut, "/storage/"+url.PathEscape(name), params, nil)
}

// DeleteStorage removes a storage definition (the data stays on the PBS).
func (p *PVE) DeleteStorage(ctx context.Context, name string) error {
	return p.Do(ctx, http.MethodDelete, "/storage/"+url.PathEscape(name), nil, nil)
}

// BackupJob is an entry of /cluster/backup.
type BackupJob struct {
	ID            string `json:"id"`
	Schedule      string `json:"schedule"`
	Storage       string `json:"storage"`
	Enabled       *int   `json:"enabled,omitempty"`
	Mode          string `json:"mode,omitempty"`
	VMID          string `json:"vmid,omitempty"`
	All           int    `json:"all,omitempty"`
	Exclude       string `json:"exclude,omitempty"`
	Node          string `json:"node,omitempty"`
	Comment       string `json:"comment,omitempty"`
	NotesTemplate string `json:"notes-template,omitempty"`
	PruneBackups  string `json:"prune-backups,omitempty"`
	MailTo        string `json:"mailto,omitempty"`
	// NotificationMode: auto, legacy-sendmail (mail only to MailTo), notification-system.
	NotificationMode string `json:"notification-mode,omitempty"`
	NextRun          int64  `json:"next-run,omitempty"`
}

// IsEnabled reports whether the job is active (missing means enabled).
func (j BackupJob) IsEnabled() bool { return j.Enabled == nil || *j.Enabled != 0 }

// BackupJobs lists the backup jobs.
func (p *PVE) BackupJobs(ctx context.Context) ([]BackupJob, error) {
	var out []BackupJob
	err := p.Do(ctx, http.MethodGet, "/cluster/backup", nil, &out)
	return out, err
}

// CreateBackupJob adds a backup job.
func (p *PVE) CreateBackupJob(ctx context.Context, params Params) error {
	return p.Do(ctx, http.MethodPost, "/cluster/backup", params, nil)
}

// UpdateBackupJob changes a backup job.
func (p *PVE) UpdateBackupJob(ctx context.Context, id string, params Params) error {
	return p.Do(ctx, http.MethodPut, "/cluster/backup/"+url.PathEscape(id), params, nil)
}

// DeleteBackupJob removes a backup job.
func (p *PVE) DeleteBackupJob(ctx context.Context, id string) error {
	return p.Do(ctx, http.MethodDelete, "/cluster/backup/"+url.PathEscape(id), nil, nil)
}

// Vzdump starts a backup on a node and returns the task id.
func (p *PVE) Vzdump(ctx context.Context, node string, params Params) (string, error) {
	var upid string
	err := p.Do(ctx, http.MethodPost, "/nodes/"+url.PathEscape(node)+"/vzdump", params, &upid)
	return upid, err
}

// NextID returns a free VMID.
func (p *PVE) NextID(ctx context.Context) (int, error) {
	var s string
	if err := p.Do(ctx, http.MethodGet, "/cluster/nextid", nil, &s); err != nil {
		var n int
		if err2 := p.Do(ctx, http.MethodGet, "/cluster/nextid", nil, &n); err2 == nil {
			return n, nil
		}
		return 0, err
	}
	return strconv.Atoi(s)
}

// RestoreOptions control a restore.
type RestoreOptions struct {
	Start bool // start the guest afterwards
	Force bool // overwrite an existing guest with the same VMID
	Live  bool // VMs from PBS: start at once and restore in the background
	// Unique gives containers new MAC addresses (VMs always get them unless Force).
	Unique bool
}

// Restore restores a backup volume (storage:backup/vm/100/<time>) as a guest.
func (p *PVE) Restore(ctx context.Context, node, typ string, vmid int, archive, storage string, o RestoreOptions) (string, error) {
	var upid string
	var err error
	if typ == "lxc" || typ == "ct" {
		params := Params{"vmid": vmid, "ostemplate": archive, "restore": true, "storage": storage,
			"start": boolNil(o.Start), "force": boolNil(o.Force)}
		if o.Unique {
			params["unique"] = true
		}
		err = p.Do(ctx, http.MethodPost, "/nodes/"+url.PathEscape(node)+"/lxc", params, &upid)
	} else {
		params := Params{"vmid": vmid, "archive": archive, "storage": storage, "start": boolNil(o.Start || o.Live), "force": boolNil(o.Force), "live-restore": boolNil(o.Live)}
		if !o.Force {
			params["unique"] = true // new MAC addresses for a copy next to the original
		}
		err = p.Do(ctx, http.MethodPost, "/nodes/"+url.PathEscape(node)+"/qemu", params, &upid)
	}
	return upid, err
}

// FileEntry is an entry of a file-restore listing. Filepath is base64 encoded.
type FileEntry struct {
	Filepath string `json:"filepath"`
	Text     string `json:"text"`
	Type     string `json:"type"` // d dir, f file, l link, v virtual (disk, partition, archive), h hardlink
	Size     int64  `json:"size"`
	Mtime    int64  `json:"mtime"`
}

// FileRestoreList lists a directory inside a backup (volume = storage:backup/…).
// For VM backups Proxmox VE starts a small helper VM first, so this can take a minute.
func (p *PVE) FileRestoreList(ctx context.Context, node, storage, volume, filepathB64 string) ([]FileEntry, error) {
	var out []FileEntry
	err := p.DoSlow(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/storage/"+url.PathEscape(storage)+"/file-restore/list",
		Params{"volume": volume, "filepath": filepathB64}, &out)
	return out, err
}

// FileRestoreDownload streams a file, or a directory as zip (tar = false) or tar.zst.
func (p *PVE) FileRestoreDownload(ctx context.Context, node, storage, volume, filepathB64 string, tar bool) (*http.Response, error) {
	return p.Stream(ctx, "/nodes/"+url.PathEscape(node)+"/storage/"+url.PathEscape(storage)+"/file-restore/download",
		Params{"volume": volume, "filepath": filepathB64, "tar": boolNil(tar)})
}

// PVETask is an entry of /cluster/tasks.
type PVETask struct {
	UPID      string `json:"upid"`
	Node      string `json:"node"`
	Type      string `json:"type"`
	ID        string `json:"id"`
	User      string `json:"user"`
	StartTime int64  `json:"starttime"`
	EndTime   int64  `json:"endtime"`
	Status    string `json:"status"`
}

// Tasks lists recent cluster tasks.
func (p *PVE) Tasks(ctx context.Context) ([]PVETask, error) {
	var out []PVETask
	err := p.Do(ctx, http.MethodGet, "/cluster/tasks", nil, &out)
	return out, err
}

// TaskLog returns the log lines of a task.
func (p *PVE) TaskLog(ctx context.Context, upid string) ([]TaskLine, error) {
	var out []TaskLine
	err := p.Do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(UPIDNode(upid))+"/tasks/"+url.PathEscape(upid)+"/log", Params{"limit": 5000}, &out)
	return out, err
}

// TaskStatus returns the state of a task.
func (p *PVE) TaskStatus(ctx context.Context, upid string) (TaskStatus, error) {
	var st TaskStatus
	err := p.Do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(UPIDNode(upid))+"/tasks/"+url.PathEscape(upid)+"/status", nil, &st)
	return st, err
}

// WaitTask waits for a task.
func (p *PVE) WaitTask(ctx context.Context, upid string, timeout time.Duration) (TaskStatus, error) {
	return p.waitTask(ctx, "/nodes/"+url.PathEscape(UPIDNode(upid))+"/tasks/"+url.PathEscape(upid)+"/status", timeout)
}

// USBDevice is an entry of /nodes/{node}/hardware/usb.
type USBDevice struct {
	BusNum       int    `json:"busnum"`
	DevNum       int    `json:"devnum"`
	Port         int    `json:"port"`
	USBPath      string `json:"usbpath"`
	VendID       string `json:"vendid"`
	ProdID       string `json:"prodid"`
	Manufacturer string `json:"manufacturer"`
	Product      string `json:"product"`
	Speed        string `json:"speed"`
	Class        int    `json:"class"`
}

// USBDevices lists the USB devices of a node.
func (p *PVE) USBDevices(ctx context.Context, node string) ([]USBDevice, error) {
	var out []USBDevice
	err := p.Do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/hardware/usb", nil, &out)
	return out, err
}

// VMConfig returns the configuration of a VM as raw key/value map.
func (p *PVE) VMConfig(ctx context.Context, node string, vmid int) (map[string]any, error) {
	var out map[string]any
	err := p.Do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/qemu/"+strconv.Itoa(vmid)+"/config", nil, &out)
	return out, err
}

// SetVMConfig changes VM options.
func (p *PVE) SetVMConfig(ctx context.Context, node string, vmid int, params Params) error {
	return p.Do(ctx, http.MethodPut, "/nodes/"+url.PathEscape(node)+"/qemu/"+strconv.Itoa(vmid)+"/config", params, nil)
}

// Permissions returns the effective privileges of the token per ACL path (GET /access/permissions).
func (p *PVE) Permissions(ctx context.Context) (map[string]map[string]int, error) {
	var out map[string]map[string]int
	err := p.Do(ctx, http.MethodGet, "/access/permissions", nil, &out)
	return out, err
}

// PVERequiredPrivs are the privileges the appliance needs on "/" in Proxmox VE.
var PVERequiredPrivs = []string{
	"Sys.Audit",               // backup jobs, nodes, tasks
	"VM.Audit",                // see VMs and containers
	"VM.Backup",               // back up
	"VM.Allocate",             // restore as a new guest
	"VM.Config.HWType",        // pass a USB port through
	"Datastore.Audit",         // see storages
	"Datastore.Allocate",      // add the PBS as storage
	"Datastore.AllocateSpace", // write backups and restored disks
}

// MissingPrivs lists the required privileges the token lacks on "/".
func MissingPrivs(perms map[string]map[string]int) []string {
	root := perms["/"]
	var missing []string
	for _, p := range PVERequiredPrivs {
		if _, ok := root[p]; !ok {
			missing = append(missing, p)
		}
	}
	return missing
}
