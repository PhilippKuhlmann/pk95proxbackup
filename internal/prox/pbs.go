package prox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// PBS is a client for Proxmox Backup Server.
type PBS struct{ *Client }

// NewPBS connects with an API token (authID = user@realm!token).
func NewPBS(host string, port int, fingerprint, authID, secret string) *PBS {
	return &PBS{newClient(host, port, fingerprint, "PBSAPIToken="+authID+":"+secret)}
}

// PBSVersion is GET /version.
type PBSVersion struct {
	Version string `json:"version"`
	Release string `json:"release"`
}

// Version returns the server version.
func (p *PBS) Version(ctx context.Context) (PBSVersion, error) {
	var v PBSVersion
	err := p.Do(ctx, http.MethodGet, "/version", nil, &v)
	return v, err
}

// NodeStatus is GET /nodes/localhost/status (the parts we show).
type NodeStatus struct {
	CPU    float64 `json:"cpu"`
	Uptime int64   `json:"uptime"`
	Memory struct {
		Total int64 `json:"total"`
		Used  int64 `json:"used"`
	} `json:"memory"`
	Root struct {
		Total int64 `json:"total"`
		Used  int64 `json:"used"`
	} `json:"root"`
	Info struct {
		Fingerprint string `json:"fingerprint"`
	} `json:"info"`
}

// NodeStatus returns load and memory of the server.
func (p *PBS) NodeStatus(ctx context.Context) (NodeStatus, error) {
	var s NodeStatus
	err := p.Do(ctx, http.MethodGet, "/nodes/localhost/status", nil, &s)
	return s, err
}

// DatastoreConfig is an entry of /config/datastore.
type DatastoreConfig struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	Comment       string `json:"comment,omitempty"`
	GCSchedule    string `json:"gc-schedule,omitempty"`
	BackingDevice string `json:"backing-device,omitempty"`
	Maintenance   string `json:"maintenance-mode,omitempty"`
	Backend       string `json:"backend,omitempty"` // type=s3,client=<endpoint>,bucket=<bucket>
}

// Datastores lists the configured datastores.
func (p *PBS) Datastores(ctx context.Context) ([]DatastoreConfig, error) {
	var out []DatastoreConfig
	err := p.Do(ctx, http.MethodGet, "/config/datastore", nil, &out)
	return out, err
}

// DatastoreUsage is an entry of /status/datastore-usage.
type DatastoreUsage struct {
	Store       string `json:"store"`
	Total       int64  `json:"total"`
	Used        int64  `json:"used"`
	Avail       int64  `json:"avail"`
	Error       string `json:"error,omitempty"`
	MountStatus string `json:"mount-status,omitempty"` // mounted, notmounted, nonremovable
	FullDate    int64  `json:"estimated-full-date,omitempty"`
}

// Usage returns the usage of all datastores.
func (p *PBS) Usage(ctx context.Context) ([]DatastoreUsage, error) {
	var out []DatastoreUsage
	err := p.Do(ctx, http.MethodGet, "/status/datastore-usage", nil, &out)
	return out, err
}

// CreateDatastore adds a datastore (synchronous in PBS 4; older versions return a task).
func (p *PBS) CreateDatastore(ctx context.Context, params Params) error {
	var upid string
	if err := p.Do(ctx, http.MethodPost, "/config/datastore", params, &upid); err != nil {
		return err
	}
	return p.waitIfTask(ctx, upid, 10*time.Minute)
}

// UpdateDatastore changes datastore options (e.g. gc-schedule, comment).
func (p *PBS) UpdateDatastore(ctx context.Context, name string, params Params) error {
	return p.Do(ctx, http.MethodPut, "/config/datastore/"+url.PathEscape(name), params, nil)
}

// DeleteDatastore removes a datastore; destroy also deletes its data.
func (p *PBS) DeleteDatastore(ctx context.Context, name string, destroy bool) error {
	var upid string
	if err := p.Do(ctx, http.MethodDelete, "/config/datastore/"+url.PathEscape(name), Params{"destroy-data": destroy}, &upid); err != nil {
		return err
	}
	return p.waitIfTask(ctx, upid, 10*time.Minute)
}

func (p *PBS) waitIfTask(ctx context.Context, upid string, timeout time.Duration) error {
	if upid == "" {
		return nil
	}
	_, err := p.WaitTask(ctx, upid, timeout)
	return err
}

// WaitTask waits for a PBS task.
func (p *PBS) WaitTask(ctx context.Context, upid string, timeout time.Duration) (TaskStatus, error) {
	return p.waitTask(ctx, "/nodes/localhost/tasks/"+url.PathEscape(upid)+"/status", timeout)
}

// StartTask posts to an endpoint that returns a UPID.
func (p *PBS) StartTask(ctx context.Context, path string, params Params) (string, error) {
	var upid string
	err := p.Do(ctx, http.MethodPost, path, params, &upid)
	return upid, err
}

// Mount mounts a removable datastore.
func (p *PBS) Mount(ctx context.Context, store string) (string, error) {
	return p.StartTask(ctx, "/admin/datastore/"+url.PathEscape(store)+"/mount", nil)
}

// Unmount unmounts a removable datastore (the disk can be pulled afterwards).
func (p *PBS) Unmount(ctx context.Context, store string) (string, error) {
	return p.StartTask(ctx, "/admin/datastore/"+url.PathEscape(store)+"/unmount", nil)
}

// GC starts garbage collection.
func (p *PBS) GC(ctx context.Context, store string) (string, error) {
	return p.StartTask(ctx, "/admin/datastore/"+url.PathEscape(store)+"/gc", nil)
}

// RRDPoint is a sample of the usage history of a datastore (values are averages, nil = no data).
type RRDPoint struct {
	Time  int64    `json:"time"`
	Total *float64 `json:"total"`
	Used  *float64 `json:"used"`
}

// DatastoreRRD returns the usage history of a datastore (timeframe hour, day, week, month,
// year, decade; the year has a value every six hours).
func (p *PBS) DatastoreRRD(ctx context.Context, store, timeframe string) ([]RRDPoint, error) {
	var out []RRDPoint
	err := p.Do(ctx, http.MethodGet, "/admin/datastore/"+url.PathEscape(store)+"/rrd", Params{"timeframe": timeframe, "cf": "AVERAGE"}, &out)
	return out, err
}

// GCStatus is the result of the last garbage collection of a datastore.
type GCStatus struct {
	IndexDataBytes int64 `json:"index-data-bytes"` // logical size of all backups
	DiskBytes      int64 `json:"disk-bytes"`       // what the chunks take on disk
	PendingBytes   int64 `json:"pending-bytes"`    // waiting to be removed (younger than a day)
	RemovedBytes   int64 `json:"removed-bytes"`
}

// GCStatus returns the result of the last garbage collection.
func (p *PBS) GCStatus(ctx context.Context, store string) (GCStatus, error) {
	var out GCStatus
	err := p.Do(ctx, http.MethodGet, "/admin/datastore/"+url.PathEscape(store)+"/gc", nil, &out)
	return out, err
}

// GCJob is the garbage collection plan of a datastore with its last run (GET /admin/gc).
type GCJob struct {
	Store        string `json:"store"`
	Schedule     string `json:"schedule,omitempty"`
	NextRun      int64  `json:"next-run,omitempty"`
	LastRunState string `json:"last-run-state,omitempty"`
	LastRunUPID  string `json:"last-run-upid,omitempty"`
	LastRunEnd   int64  `json:"last-run-endtime,omitempty"`
	Duration     int64  `json:"duration,omitempty"` // seconds
}

// GCJobs lists the garbage collection of every datastore (PBS 3.2 and newer).
func (p *PBS) GCJobs(ctx context.Context) ([]GCJob, error) {
	var out []GCJob
	err := p.Do(ctx, http.MethodGet, "/admin/gc", nil, &out)
	return out, err
}

// UPIDStart is the start time a task ID carries, 0 if none. Proxmox VE writes
// UPID:node:pid:pstart:starttime:type:…, the Backup Server has a task counter before the start
// (UPID:node:pid:pstart:taskid:starttime:type:…).
func UPIDStart(upid string) int64 {
	f := strings.Split(upid, ":")
	if len(f) < 6 || f[0] != "UPID" {
		return 0
	}
	field := f[4]
	if hex8(f[5]) {
		field = f[5] // the Backup Server's form: f[4] is the task counter
	}
	v, err := strconv.ParseInt(field, 16, 64)
	if err != nil {
		return 0
	}
	return v
}

func hex8(s string) bool {
	if len(s) != 8 {
		return false
	}
	_, err := strconv.ParseUint(s, 16, 32)
	return err == nil
}

// Verify starts a verification of all snapshots.
func (p *PBS) Verify(ctx context.Context, store string) (string, error) {
	return p.StartTask(ctx, "/admin/datastore/"+url.PathEscape(store)+"/verify", nil)
}

// Snapshot is an entry of /admin/datastore/{store}/snapshots.
type Snapshot struct {
	Type         string         `json:"backup-type"`
	ID           string         `json:"backup-id"`
	Time         int64          `json:"backup-time"`
	Size         int64          `json:"size"`
	Owner        string         `json:"owner"`
	Protected    bool           `json:"protected"`
	Comment      string         `json:"comment"`
	Fingerprint  string         `json:"fingerprint,omitempty"` // key of an encrypted backup
	Files        []SnapshotFile `json:"files,omitempty"`
	Verification *struct {
		State string `json:"state"`
	} `json:"verification,omitempty"`
}

// SnapshotFile is an archive of a snapshot (drive-scsi0.img.fidx, root.pxar.didx, …).
type SnapshotFile struct {
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
	CryptMode string `json:"crypt-mode,omitempty"`
}

// Snapshots lists all snapshots of a datastore (root namespace or ns).
func (p *PBS) Snapshots(ctx context.Context, store, ns string) ([]Snapshot, error) {
	var out []Snapshot
	err := p.Do(ctx, http.MethodGet, "/admin/datastore/"+url.PathEscape(store)+"/snapshots", Params{"ns": emptyNil(ns)}, &out)
	return out, err
}

// DeleteSnapshot removes one snapshot.
func (p *PBS) DeleteSnapshot(ctx context.Context, store, ns, typ, id string, t int64) error {
	return p.Do(ctx, http.MethodDelete, "/admin/datastore/"+url.PathEscape(store)+"/snapshots",
		Params{"ns": emptyNil(ns), "backup-type": typ, "backup-id": id, "backup-time": t}, nil)
}

// DeleteGroup removes all snapshots of a guest from a datastore. With protected snapshots the
// Backup Server removes the others and fails.
func (p *PBS) DeleteGroup(ctx context.Context, store, ns, typ, id string) error {
	return p.Do(ctx, http.MethodDelete, "/admin/datastore/"+url.PathEscape(store)+"/groups",
		Params{"ns": emptyNil(ns), "backup-type": typ, "backup-id": id}, nil)
}

// Group is an entry of /admin/datastore/{store}/groups.
type Group struct {
	Type       string `json:"backup-type"`
	ID         string `json:"backup-id"`
	LastBackup int64  `json:"last-backup"`
	Count      int    `json:"backup-count"`
	Owner      string `json:"owner"`
	Comment    string `json:"comment"`
}

// Groups lists the backup groups of a datastore.
func (p *PBS) Groups(ctx context.Context, store string) ([]Group, error) {
	var out []Group
	err := p.Do(ctx, http.MethodGet, "/admin/datastore/"+url.PathEscape(store)+"/groups", nil, &out)
	return out, err
}

// Job is a sync, prune or verify job with its run state (GET /admin/sync etc.).
type Job struct {
	ID             string     `json:"id"`
	Store          string     `json:"store"`
	NS             string     `json:"ns,omitempty"`
	Remote         string     `json:"remote,omitempty"`
	RemoteStore    string     `json:"remote-store,omitempty"`
	RemoteNS       string     `json:"remote-ns,omitempty"`
	Schedule       string     `json:"schedule,omitempty"`
	Comment        string     `json:"comment,omitempty"`
	RemoveVanished bool       `json:"remove-vanished,omitempty"`
	RunOnMount     bool       `json:"run-on-mount,omitempty"`
	UnmountOnDone  bool       `json:"unmount-on-done,omitempty"`
	Direction      string     `json:"sync-direction,omitempty"`
	TransferLast   int        `json:"transfer-last,omitempty"`
	RateIn         flexString `json:"rate-in,omitempty"` // bandwidth limit of a sync job
	Disable        bool       `json:"disable,omitempty"`
	KeepLast       int        `json:"keep-last,omitempty"`
	KeepDaily      int        `json:"keep-daily,omitempty"`
	KeepWeekly     int        `json:"keep-weekly,omitempty"`
	KeepMonthly    int        `json:"keep-monthly,omitempty"`
	KeepYearly     int        `json:"keep-yearly,omitempty"`
	LastRunState   string     `json:"last-run-state,omitempty"`
	LastRunUPID    string     `json:"last-run-upid,omitempty"`
	LastRunEnd     int64      `json:"last-run-endtime,omitempty"`
	NextRun        int64      `json:"next-run,omitempty"`
}

// Jobs lists jobs of a kind ("sync", "prune", "verify") with their state.
func (p *PBS) Jobs(ctx context.Context, kind string) ([]Job, error) {
	var out []Job
	params := Params{}
	if kind == "sync" {
		params["sync-direction"] = "all"
	}
	err := p.Do(ctx, http.MethodGet, "/admin/"+kind, params, &out)
	return out, err
}

// CreateJob adds a job of a kind (POST /config/<kind>).
func (p *PBS) CreateJob(ctx context.Context, kind string, params Params) error {
	return p.Do(ctx, http.MethodPost, "/config/"+kind, params, nil)
}

// UpdateJob changes a job (PUT /config/<kind>/<id>).
func (p *PBS) UpdateJob(ctx context.Context, kind, id string, params Params) error {
	return p.Do(ctx, http.MethodPut, "/config/"+kind+"/"+url.PathEscape(id), params, nil)
}

// DeleteJob removes a job.
func (p *PBS) DeleteJob(ctx context.Context, kind, id string) error {
	return p.Do(ctx, http.MethodDelete, "/config/"+kind+"/"+url.PathEscape(id), nil, nil)
}

// RunJob starts a job now. PBS 4 returns nothing, older versions a UPID.
func (p *PBS) RunJob(ctx context.Context, kind, id string) error {
	return p.Do(ctx, http.MethodPost, "/admin/"+kind+"/"+url.PathEscape(id)+"/run", nil, nil)
}

// Remote is an entry of /config/remote.
type Remote struct {
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port,omitempty"`
	AuthID      string `json:"auth-id"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Comment     string `json:"comment,omitempty"`
}

// Remotes lists the configured remotes.
func (p *PBS) Remotes(ctx context.Context) ([]Remote, error) {
	var out []Remote
	err := p.Do(ctx, http.MethodGet, "/config/remote", nil, &out)
	return out, err
}

// PutRemote creates or updates a remote.
func (p *PBS) PutRemote(ctx context.Context, r Remote, password string) error {
	params := Params{"host": r.Host, "auth-id": r.AuthID, "password": password, "fingerprint": r.Fingerprint, "comment": emptyNil(r.Comment)}
	if r.Port != 0 && r.Port != 8007 {
		params["port"] = r.Port
	}
	err := p.Do(ctx, http.MethodPut, "/config/remote/"+url.PathEscape(r.Name), params, nil)
	if err == nil {
		return nil
	}
	params["name"] = r.Name
	return p.Do(ctx, http.MethodPost, "/config/remote", params, nil)
}

// Disk is an entry of /nodes/localhost/disks/list.
type Disk struct {
	Name       string      `json:"name"`
	DevPath    string      `json:"devpath"`
	Size       int64       `json:"size"`
	Model      string      `json:"model"`
	Vendor     string      `json:"vendor"`
	Serial     string      `json:"serial"`
	DiskType   string      `json:"disk-type"` // hdd, ssd, usb, unknown
	Used       string      `json:"used"`
	GPT        bool        `json:"gpt"`
	Partitions []Partition `json:"partitions"`
}

// Partition is a partition of a disk.
type Partition struct {
	Name       string `json:"name"`
	DevPath    string `json:"devpath"`
	Size       int64  `json:"size"`
	Filesystem string `json:"filesystem"`
	UUID       string `json:"uuid"`
	Mounted    bool   `json:"mounted"`
	Used       string `json:"used"`
}

// Disks lists the disks with their partitions.
func (p *PBS) Disks(ctx context.Context) ([]Disk, error) {
	var out []Disk
	err := p.Do(ctx, http.MethodGet, "/nodes/localhost/disks/list", Params{"include-partitions": true, "skipsmart": true}, &out)
	return out, err
}

// WipeDisk deletes the partition table of a disk.
func (p *PBS) WipeDisk(ctx context.Context, disk string) error {
	var upid string
	if err := p.Do(ctx, http.MethodPut, "/nodes/localhost/disks/wipedisk", Params{"disk": disk}, &upid); err != nil {
		return err
	}
	return p.waitIfTask(ctx, upid, 5*time.Minute)
}

// CreateDirectoryStore formats a disk and adds it as (removable) datastore.
func (p *PBS) CreateDirectoryStore(ctx context.Context, disk, name, fs string, removable bool) error {
	upid, err := p.StartTask(ctx, "/nodes/localhost/disks/directory", Params{
		"disk": disk, "name": name, "filesystem": fs, "add-datastore": true, "removable-datastore": removable,
	})
	if err != nil {
		return err
	}
	return p.waitIfTask(ctx, upid, 30*time.Minute)
}

// Task is an entry of /nodes/localhost/tasks.
type Task struct {
	UPID      string `json:"upid"`
	Node      string `json:"node"`
	Type      string `json:"worker_type"`
	WorkerID  string `json:"worker_id"`
	User      string `json:"user"`
	StartTime int64  `json:"starttime"`
	EndTime   int64  `json:"endtime"`
	Status    string `json:"status"`
}

// StopTask aborts a running task.
func (p *PBS) StopTask(ctx context.Context, upid string) error {
	return p.Do(ctx, http.MethodDelete, "/nodes/localhost/tasks/"+url.PathEscape(upid), nil, nil)
}

// TrafficRule limits the bandwidth for clients from some networks, at some times (traffic control).
type TrafficRule struct {
	Name      string     `json:"name"`
	Network   []string   `json:"network"`
	RateIn    flexString `json:"rate-in,omitempty"`
	RateOut   flexString `json:"rate-out,omitempty"`
	Timeframe []string   `json:"timeframe,omitempty"`
	Comment   string     `json:"comment,omitempty"`
}

// TrafficRules lists the traffic control rules.
func (p *PBS) TrafficRules(ctx context.Context) ([]TrafficRule, error) {
	var out []TrafficRule
	err := p.Do(ctx, http.MethodGet, "/config/traffic-control", nil, &out)
	return out, err
}

// CreateTrafficRule adds a rule (name, network, rate-in, rate-out, timeframe, comment).
func (p *PBS) CreateTrafficRule(ctx context.Context, params Params) error {
	return p.Do(ctx, http.MethodPost, "/config/traffic-control", params, nil)
}

// UpdateTrafficRule changes a rule.
func (p *PBS) UpdateTrafficRule(ctx context.Context, name string, params Params) error {
	return p.Do(ctx, http.MethodPut, "/config/traffic-control/"+url.PathEscape(name), params, nil)
}

// DeleteTrafficRule removes a rule.
func (p *PBS) DeleteTrafficRule(ctx context.Context, name string) error {
	return p.Do(ctx, http.MethodDelete, "/config/traffic-control/"+url.PathEscape(name), nil, nil)
}

// Tasks lists recent tasks.
func (p *PBS) Tasks(ctx context.Context, limit int, errorsOnly bool) ([]Task, error) {
	var out []Task
	err := p.Do(ctx, http.MethodGet, "/nodes/localhost/tasks", Params{"limit": limit, "errors": boolNil(errorsOnly)}, &out)
	return out, err
}

// TaskLog returns the log lines of a task.
func (p *PBS) TaskLog(ctx context.Context, upid string) ([]TaskLine, error) {
	var out []TaskLine
	err := p.Do(ctx, http.MethodGet, "/nodes/localhost/tasks/"+url.PathEscape(upid)+"/log", Params{"limit": 5000}, &out)
	return out, err
}

// TaskStatus returns the state of a task.
func (p *PBS) TaskStatus(ctx context.Context, upid string) (TaskStatus, error) {
	var st TaskStatus
	err := p.Do(ctx, http.MethodGet, "/nodes/localhost/tasks/"+url.PathEscape(upid)+"/status", nil, &st)
	return st, err
}

// EnsureUser creates a PBS user if it does not exist yet.
func (p *PBS) EnsureUser(ctx context.Context, userid, comment string) error {
	err := p.Do(ctx, http.MethodPost, "/access/users", Params{"userid": userid, "comment": comment}, nil)
	if err != nil && (IsStatus(err, 400) || IsStatus(err, 500)) {
		var u map[string]any
		if p.Do(ctx, http.MethodGet, "/access/users/"+url.PathEscape(userid), nil, &u) == nil {
			return nil
		}
	}
	return err
}

// NewToken (re)creates an API token and returns its full id and secret.
func (p *PBS) NewToken(ctx context.Context, userid, name, comment string) (string, string, error) {
	path := "/access/users/" + url.PathEscape(userid) + "/token/" + url.PathEscape(name)
	_ = p.Do(ctx, http.MethodDelete, path, nil, nil)
	var out struct {
		TokenID string `json:"tokenid"`
		Value   string `json:"value"`
	}
	if err := p.Do(ctx, http.MethodPost, path, Params{"comment": comment}, &out); err != nil {
		return "", "", err
	}
	return out.TokenID, out.Value, nil
}

// SetACL grants a role on a path (and everything below it) to an auth id.
func (p *PBS) SetACL(ctx context.Context, path, role, authID string) error {
	return p.SetACLScope(ctx, path, role, authID, true)
}

// SetACLScope grants a role on a path to an auth id; without propagate the grant covers only
// that path (for a datastore: only its root namespace), not the namespaces below it.
func (p *PBS) SetACLScope(ctx context.Context, path, role, authID string, propagate bool) error {
	return p.Do(ctx, http.MethodPut, "/access/acl", Params{"path": path, "role": role, "auth-id": authID, "propagate": propagate}, nil)
}

// NamespacePath is the ACL path of one namespace of a datastore ("" = the root namespace).
func NamespacePath(store, ns string) string {
	ns = strings.Trim(ns, "/")
	if ns == "" {
		return "/datastore/" + store
	}
	return "/datastore/" + store + "/" + ns
}

// S3Endpoint is an S3 object store the server can put datastores into (/config/s3).
type S3Endpoint struct {
	ID          string     `json:"id"`
	Endpoint    string     `json:"endpoint"`
	Region      string     `json:"region,omitempty"`
	AccessKey   string     `json:"access-key,omitempty"`
	Port        int        `json:"port,omitempty"`
	PathStyle   bool       `json:"path-style,omitempty"`
	Fingerprint string     `json:"fingerprint,omitempty"`
	RateIn      flexString `json:"rate-in,omitempty"`  // download limit
	RateOut     flexString `json:"rate-out,omitempty"` // upload limit
}

// UpdateS3Endpoint changes options of an S3 endpoint (e.g. rate-out).
func (p *PBS) UpdateS3Endpoint(ctx context.Context, id string, params Params) error {
	return p.Do(ctx, http.MethodPut, "/config/s3/"+url.PathEscape(id), params, nil)
}

// S3Endpoints lists the S3 endpoints.
func (p *PBS) S3Endpoints(ctx context.Context) ([]S3Endpoint, error) {
	var out []S3Endpoint
	err := p.Do(ctx, http.MethodGet, "/config/s3", nil, &out)
	return out, err
}

// CreateS3Endpoint adds an S3 endpoint (id, endpoint, region, access-key, secret-key, …).
func (p *PBS) CreateS3Endpoint(ctx context.Context, params Params) error {
	return p.Do(ctx, http.MethodPost, "/config/s3", params, nil)
}

// DeleteS3Endpoint removes an S3 endpoint.
func (p *PBS) DeleteS3Endpoint(ctx context.Context, id string) error {
	return p.Do(ctx, http.MethodDelete, "/config/s3/"+url.PathEscape(id), nil, nil)
}

// S3Buckets lists the buckets the access key of an endpoint may see.
func (p *PBS) S3Buckets(ctx context.Context, id string) ([]string, error) {
	var raw []json.RawMessage
	if err := p.DoSlow(ctx, http.MethodGet, "/config/s3/"+url.PathEscape(id)+"/list-buckets", nil, &raw); err != nil {
		return nil, err
	}
	out := []string{}
	for _, r := range raw {
		var name string
		if json.Unmarshal(r, &name) == nil {
			out = append(out, name)
			continue
		}
		var obj struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(r, &obj) == nil && obj.Name != "" {
			out = append(out, obj.Name)
		}
	}
	return out, nil
}

// S3Check tests whether the endpoint can write to a bucket (with the datastore name as prefix).
func (p *PBS) S3Check(ctx context.Context, id, bucket, prefix string) error {
	return p.DoSlow(ctx, http.MethodPut, "/admin/s3/"+url.PathEscape(id)+"/check", Params{"bucket": bucket, "store-prefix": emptyNil(prefix)}, nil)
}

// RemoveACL takes a role on a path away from a user or token.
func (p *PBS) RemoveACL(ctx context.Context, path, role, authID string) error {
	return p.Do(ctx, http.MethodPut, "/access/acl", Params{"path": path, "role": role, "auth-id": authID, "delete": true}, nil)
}

// ACL is an entry of /access/acl.
type ACL struct {
	Path      string `json:"path"`
	UGID      string `json:"ugid"`
	UGIDType  string `json:"ugid_type"` // user, group
	Role      string `json:"roleid"`
	Propagate bool   `json:"propagate"`
}

// ACLs lists all access control entries.
func (p *PBS) ACLs(ctx context.Context) ([]ACL, error) {
	var out []ACL
	err := p.Do(ctx, http.MethodGet, "/access/acl", nil, &out)
	return out, err
}

// SetProtected protects a snapshot from pruning and deletion (or lifts the protection).
func (p *PBS) SetProtected(ctx context.Context, store, ns, typ, id string, t int64, on bool) error {
	return p.Do(ctx, http.MethodPut, "/admin/datastore/"+url.PathEscape(store)+"/protected",
		Params{"ns": emptyNil(ns), "backup-type": typ, "backup-id": id, "backup-time": t, "protected": on}, nil)
}

func emptyNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolNil(b bool) any {
	if !b {
		return nil
	}
	return true
}

// SnapshotFile downloads a decoded file of a snapshot, e.g. "qemu-server.conf.blob" or
// "pct.conf.blob" (the guest configuration stored with every backup).
func (p *PBS) SnapshotFile(ctx context.Context, store, ns, typ, id string, t int64, file string) ([]byte, error) {
	return p.SnapshotFileMax(ctx, store, ns, typ, id, t, file, 1<<20)
}

// SnapshotFileMax is SnapshotFile for files up to max bytes (larger ones are an error).
func (p *PBS) SnapshotFileMax(ctx context.Context, store, ns, typ, id string, t int64, file string, max int64) ([]byte, error) {
	resp, err := p.Stream(ctx, "/admin/datastore/"+url.PathEscape(store)+"/download-decoded", Params{
		"ns": emptyNil(ns), "backup-type": typ, "backup-id": id, "backup-time": t, "file-name": file,
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err == nil && int64(len(b)) > max {
		return nil, fmt.Errorf("%s ist größer als %d MiB", file, max>>20)
	}
	return b, err
}

// GuestNameFromConfig reads the name of a VM ("name: …") or container ("hostname: …")
// from a stored configuration (only the main section, not snapshots).
func GuestNameFromConfig(conf []byte) string {
	for _, line := range strings.Split(string(conf), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			break
		}
		for _, key := range []string{"name:", "hostname:"} {
			if strings.HasPrefix(line, key) {
				return strings.TrimSpace(strings.TrimPrefix(line, key))
			}
		}
	}
	return ""
}

// ReloadProxy reloads proxmox-backup-proxy gracefully: running tasks finish in the old
// process, open handles of datastores (which can keep a disk busy) are let go.
func (p *PBS) ReloadProxy(ctx context.Context) error {
	var upid string
	if err := p.Do(ctx, http.MethodPost, "/nodes/localhost/services/proxmox-backup-proxy/reload", nil, &upid); err != nil {
		return err
	}
	return p.waitIfTask(ctx, upid, 2*time.Minute)
}

// AptPackage is an entry of /nodes/localhost/apt/versions or /apt/update.
type AptPackage struct {
	Package    string `json:"Package"`
	Version    string `json:"Version"`    // candidate (newest available)
	OldVersion string `json:"OldVersion"` // installed
	ExtraInfo  string `json:"ExtraInfo"`  // "running version: 4.2.6", "running kernel: …"
}

// AptVersions lists the versions of the Proxmox packages.
func (p *PBS) AptVersions(ctx context.Context) ([]AptPackage, error) {
	var out []AptPackage
	err := p.Do(ctx, http.MethodGet, "/nodes/localhost/apt/versions", nil, &out)
	return out, err
}

// AptUpdates lists the packages that can be upgraded (from the last package list refresh).
func (p *PBS) AptUpdates(ctx context.Context) ([]AptPackage, error) {
	var out []AptPackage
	err := p.Do(ctx, http.MethodGet, "/nodes/localhost/apt/update", nil, &out)
	return out, err
}

// RefreshPackages reads the package lists again (apt update) and waits for it.
func (p *PBS) RefreshPackages(ctx context.Context) error {
	var upid string
	if err := p.Do(ctx, http.MethodPost, "/nodes/localhost/apt/update", Params{"quiet": true}, &upid); err != nil {
		return err
	}
	return p.waitIfTask(ctx, upid, 5*time.Minute)
}

// Reboot restarts the machine of the Backup Server.
func (p *PBS) Reboot(ctx context.Context) error {
	return p.Do(ctx, http.MethodPost, "/nodes/localhost/status", Params{"command": "reboot"}, nil)
}

// RunningKernel returns the release of the running kernel ("7.0.14-17-pve").
func (p *PBS) RunningKernel(ctx context.Context) (string, error) {
	var out struct {
		Kernel struct {
			Release string `json:"release"`
		} `json:"current-kernel"`
		KVersion string `json:"kversion"`
	}
	if err := p.Do(ctx, http.MethodGet, "/nodes/localhost/status", nil, &out); err != nil {
		return "", err
	}
	if out.Kernel.Release != "" {
		return out.Kernel.Release, nil
	}
	f := strings.Fields(out.KVersion) // "Linux 7.0.14-17-pve #1 …"
	if len(f) > 1 {
		return f[1], nil
	}
	return "", nil
}

// ReloadService reloads a service gracefully ("proxmox-backup", "proxmox-backup-proxy").
func (p *PBS) ReloadService(ctx context.Context, service string) error {
	var upid string
	if err := p.Do(ctx, http.MethodPost, "/nodes/localhost/services/"+url.PathEscape(service)+"/reload", nil, &upid); err != nil {
		return err
	}
	return p.waitIfTask(ctx, upid, 2*time.Minute)
}

// ScanRemote lets the server list the datastores it can see on a remote: this tests the
// connection from this server to the remote (network, certificate, login).
func (p *PBS) ScanRemote(ctx context.Context, name string) ([]string, error) {
	var out []struct {
		Store string `json:"store"`
	}
	if err := p.Do(ctx, http.MethodGet, "/config/remote/"+url.PathEscape(name)+"/scan", nil, &out); err != nil {
		return nil, err
	}
	stores := []string{}
	for _, s := range out {
		stores = append(stores, s.Store)
	}
	return stores, nil
}
