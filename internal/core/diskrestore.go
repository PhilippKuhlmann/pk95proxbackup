package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/prox"
)

// Single disk restore: Proxmox VE restores only whole VMs. The appliance restores the backup
// under a free ID on the node of the target VM, hands the chosen disk over to the target VM
// (move_disk with target-vmid, the disk keeps its data) and deletes the helper VM with the
// other disks again. The disk is added – nothing of the target VM is overwritten.

const diskRestoreTag = "pk95-disk"

// DiskRestoreRequest names the backup, the disk and where it goes.
type DiskRestoreRequest struct {
	Cluster string `json:"cluster"`
	Server  string `json:"pbs"`
	Store   string `json:"store"`
	Source  int    `json:"bid"`
	Time    int64  `json:"time"`
	Drive   string `json:"drive"`  // scsi1, virtio0, …
	Target  int    `json:"target"` // VM that gets the disk
	Storage string `json:"storage"`
	By      string `json:"-"`
}

// DiskRestoreJob is a running or finished single disk restore.
type DiskRestoreJob struct {
	ID          string  `json:"id"`
	ClusterName string  `json:"cluster_name"`
	Source      int     `json:"source"`
	Time        int64   `json:"time"`
	Drive       string  `json:"drive"`
	Size        int64   `json:"size"`
	Target      int     `json:"target"`
	TargetName  string  `json:"target_name"`
	Node        string  `json:"node"`
	Storage     string  `json:"storage"`
	TempID      int     `json:"temp_id"`
	Phase       string  `json:"phase"` // restore, attach, cleanup, done, error
	Percent     float64 `json:"percent"`
	Detail      string  `json:"detail,omitempty"`
	NewDisk     string  `json:"new_disk,omitempty"`
	Error       string  `json:"error,omitempty"`
	Note        string  `json:"note,omitempty"`
	Started     int64   `json:"started"`
	Ended       int64   `json:"ended,omitempty"`
}

type diskRestoreRuntime struct {
	mu   sync.Mutex
	jobs map[string]*DiskRestoreJob
}

// DiskRestore returns a copy of a job.
func (c *Core) DiskRestore(id string) (DiskRestoreJob, bool) {
	c.diskRestore.mu.Lock()
	defer c.diskRestore.mu.Unlock()
	j, ok := c.diskRestore.jobs[id]
	if !ok {
		return DiskRestoreJob{}, false
	}
	return *j, true
}

func (c *Core) setDiskJob(j *DiskRestoreJob, f func(*DiskRestoreJob)) {
	c.diskRestore.mu.Lock()
	f(j)
	c.diskRestore.mu.Unlock()
}

var driveRe = regexp.MustCompile(`^(scsi|virtio|sata|ide)(\d+)$`)

// busSlots is how many disks each bus takes.
var busSlots = map[string]int{"scsi": 31, "virtio": 16, "sata": 6, "ide": 4}

// freeDisk is the first free key on the bus of drive in a VM configuration (scsi if that bus is full).
func freeDisk(cfg map[string]any, drive string) string {
	bus := "scsi"
	if m := driveRe.FindStringSubmatch(drive); m != nil {
		bus = m[1]
	}
	for _, b := range []string{bus, "scsi"} {
		for i := 0; i < busSlots[b]; i++ {
			if _, used := cfg[b+strconv.Itoa(i)]; !used {
				return b + strconv.Itoa(i)
			}
		}
	}
	return ""
}

// BackupDisks lists the disks of a VM backup (drive → size); EFI and TPM state are left out.
func BackupDisks(files []prox.SnapshotFile) map[string]int64 {
	out := map[string]int64{}
	for _, f := range files {
		name, ok := strings.CutSuffix(f.Filename, ".img.fidx")
		if !ok {
			continue
		}
		name = strings.TrimPrefix(name, "drive-")
		if driveRe.MatchString(name) {
			out[name] = f.Size
		}
	}
	return out
}

// StartDiskRestore checks the request and starts the restore in the background.
func (c *Core) StartDiskRestore(ctx context.Context, req DiskRestoreRequest) (DiskRestoreJob, map[string]string, error) {
	errs := map[string]string{}
	cl, err := c.Cluster(req.Cluster)
	if err != nil {
		return DiskRestoreJob{}, nil, err
	}
	srv, err := c.Server(req.Server)
	if err != nil {
		return DiskRestoreJob{}, nil, err
	}
	pve := c.PVEClient(cl)
	if perms, err := pve.Permissions(ctx); err == nil {
		if _, ok := perms["/"]["VM.Config.Disk"]; !ok {
			return DiskRestoreJob{}, nil, errors.New("Dem Token von " + cl.Name + " fehlt das Recht VM.Config.Disk (Disks an VMs hängen). In Proxmox VE unter Rechenzentrum → Berechtigungen ergänzen.")
		}
	}
	// The backup and its disks.
	snaps, err := c.PBSClient(srv).Snapshots(ctx, req.Store, "")
	if err != nil {
		return DiskRestoreJob{}, nil, err
	}
	var snap *prox.Snapshot
	for i := range snaps {
		if snaps[i].Type == "vm" && snaps[i].ID == strconv.Itoa(req.Source) && snaps[i].Time == req.Time {
			snap = &snaps[i]
		}
	}
	if snap == nil {
		return DiskRestoreJob{}, nil, errors.New("Diese Sicherung gibt es nicht (mehr).")
	}
	disks := BackupDisks(snap.Files)
	size, ok := disks[req.Drive]
	if !ok {
		errs["drive"] = "Diese Disk ist nicht in der Sicherung."
	}
	var all int64
	for _, s := range disks {
		all += s
	}
	// The target VM and its node.
	guests, err := pve.Guests(ctx)
	if err != nil {
		return DiskRestoreJob{}, nil, err
	}
	var target *prox.Guest
	for i := range guests {
		if guests[i].VMID == req.Target && guests[i].Type == "qemu" {
			target = &guests[i]
		}
	}
	if target == nil {
		errs["target"] = "Ziel-VM wählen."
	} else if target.Template == 1 {
		errs["target"] = "Eine Vorlage kann keine Disk bekommen."
	}
	if req.Storage == "" {
		errs["storage"] = "Speicher wählen."
	}
	if len(errs) > 0 {
		return DiskRestoreJob{}, errs, nil
	}
	sts, err := pve.NodeStorages(ctx, target.Node, "images")
	if err != nil {
		return DiskRestoreJob{}, nil, err
	}
	found := false
	for _, s := range sts {
		if s.Storage == req.Storage {
			found = true
			if s.Avail > 0 && s.Avail < all {
				errs["storage"] = fmt.Sprintf("Zu wenig Platz: für den Umweg über die ganze VM braucht es %s, frei sind %s.", humanSize(all), humanSize(s.Avail))
			}
		}
	}
	if !found {
		errs["storage"] = "Dieser Speicher nimmt auf " + target.Node + " keine VM-Disks auf."
	}
	if len(errs) > 0 {
		return DiskRestoreJob{}, errs, nil
	}
	if c.EnsureStorage == nil {
		return DiskRestoreJob{}, nil, errors.New("Speicher für die Sicherung nicht verfügbar")
	}
	volStorage, err := c.EnsureStorage(ctx, pve, cl, srv, req.Store, snap.Fingerprint)
	if err != nil {
		return DiskRestoreJob{}, nil, fmt.Errorf("Datastore %s in Proxmox VE: %w", req.Store, err)
	}
	tempID, err := pve.NextID(ctx)
	if err != nil {
		return DiskRestoreJob{}, nil, err
	}
	j := &DiskRestoreJob{ID: NewID(6), ClusterName: cl.Name, Source: req.Source, Time: req.Time, Drive: req.Drive, Size: size,
		Target: target.VMID, TargetName: target.Name, Node: target.Node, Storage: req.Storage, TempID: tempID, Phase: "restore",
		Percent: -1, Started: time.Now().Unix()}
	c.diskRestore.mu.Lock()
	if c.diskRestore.jobs == nil {
		c.diskRestore.jobs = map[string]*DiskRestoreJob{}
	}
	c.diskRestore.jobs[j.ID] = j
	c.diskRestore.mu.Unlock()
	volume := fmt.Sprintf("%s:backup/vm/%d/%s", volStorage, req.Source, time.Unix(req.Time, 0).UTC().Format("2006-01-02T15:04:05Z"))
	c.Event("info", cl.Name, fmt.Sprintf("Disk %s aus der Sicherung von %d wird zurückgeholt", req.Drive, req.Source),
		fmt.Sprintf("Stand %s, Ziel VM %d %s, Umweg über VM %d (von %s).", fmtTime(req.Time), target.VMID, target.Name, tempID, req.By))
	go c.runDiskRestore(pve, j, volume)
	return *j, nil, nil
}

func humanSize(n int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	return strings.Replace(fmt.Sprintf("%.1f %s", f, units[i]), ".", ",", 1)
}

// waitWithProgress waits for a task and copies its progress into the job.
func (c *Core) waitWithProgress(ctx context.Context, pve *prox.PVE, j *DiskRestoreJob, upid string) error {
	for {
		st, err := pve.TaskStatus(ctx, upid)
		if err == nil && st.Status == "stopped" {
			if st.ExitStatus != "OK" && !strings.HasPrefix(st.ExitStatus, "WARNINGS") {
				return errors.New(st.ExitStatus)
			}
			return nil
		}
		if lines, err := pve.TailLog(ctx, upid, 20); err == nil {
			p := prox.ParseProgress(lines)
			c.setDiskJob(j, func(j *DiskRestoreJob) { j.Percent, j.Detail = p.Percent, p.Detail })
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (c *Core) runDiskRestore(pve *prox.PVE, j *DiskRestoreJob, volume string) {
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()
	created := false
	fail := func(err error) {
		c.setDiskJob(j, func(j *DiskRestoreJob) { j.Phase, j.Error, j.Ended = "error", err.Error(), time.Now().Unix() })
		c.Event("warning", j.ClusterName, fmt.Sprintf("Disk %s aus der Sicherung von %d nicht zurückgeholt", j.Drive, j.Source), err.Error())
	}
	cleanup := func() error {
		if !created {
			return nil
		}
		cctx, ccancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer ccancel()
		upid, err := pve.DestroyGuest(cctx, j.Node, "qemu", j.TempID)
		if err == nil {
			_, err = pve.WaitTask(cctx, upid, 10*time.Minute)
		}
		return err
	}

	upid, err := pve.Restore(ctx, j.Node, "qemu", j.TempID, volume, j.Storage, prox.RestoreOptions{Unique: true})
	if err != nil {
		fail(fmt.Errorf("Wiederherstellung nicht gestartet: %w", err))
		return
	}
	created = true
	if err := c.waitWithProgress(ctx, pve, j, upid); err != nil {
		if cerr := cleanup(); cerr != nil {
			err = fmt.Errorf("%w – Hilfs-VM %d bitte in Proxmox löschen (%v)", err, j.TempID, cerr)
		}
		fail(fmt.Errorf("Wiederherstellung fehlgeschlagen: %w", err))
		return
	}
	// Mark the helper so that nobody starts it by mistake.
	_ = pve.SetGuestConfig(ctx, j.Node, "qemu", j.TempID, prox.Params{"name": fmt.Sprintf("%s-%d", diskRestoreTag, j.Source), "tags": diskRestoreTag, "onboot": false})

	c.setDiskJob(j, func(j *DiskRestoreJob) { j.Phase, j.Percent, j.Detail = "attach", -1, "" })
	attach := func() (string, error) {
		temp, err := pve.GuestConfig(ctx, j.Node, "qemu", j.TempID)
		if err != nil {
			return "", err
		}
		if _, ok := temp[j.Drive]; !ok {
			return "", fmt.Errorf("Disk %s fehlt in der wiederhergestellten VM", j.Drive)
		}
		cfg, err := pve.GuestConfig(ctx, j.Node, "qemu", j.Target)
		if err != nil {
			return "", err
		}
		key := freeDisk(cfg, j.Drive)
		if key == "" {
			return "", fmt.Errorf("VM %d hat keinen freien Anschluss mehr", j.Target)
		}
		upid, err := pve.ReassignDisk(ctx, j.Node, j.TempID, j.Drive, j.Target, key)
		if err != nil {
			return "", err
		}
		if _, err := pve.WaitTask(ctx, upid, 30*time.Minute); err != nil {
			return "", err
		}
		return key, nil
	}
	key, err := attach()
	if err != nil {
		if cerr := cleanup(); cerr != nil {
			err = fmt.Errorf("%w – Hilfs-VM %d bitte in Proxmox löschen (%v)", err, j.TempID, cerr)
		}
		fail(fmt.Errorf("Anhängen fehlgeschlagen: %w", err))
		return
	}
	c.setDiskJob(j, func(j *DiskRestoreJob) { j.Phase, j.NewDisk = "cleanup", key })
	note := ""
	if err := cleanup(); err != nil {
		note = fmt.Sprintf("Die Hilfs-VM %d (Name %s-%d) ließ sich nicht löschen: %v – bitte in Proxmox entfernen.", j.TempID, diskRestoreTag, j.Source, err)
	}
	c.setDiskJob(j, func(j *DiskRestoreJob) { j.Phase, j.Note, j.Ended = "done", note, time.Now().Unix() })
	c.Event("info", j.ClusterName, fmt.Sprintf("Disk %s aus der Sicherung von %d an VM %d angehängt", j.Drive, j.Source, j.Target),
		fmt.Sprintf("Stand %s, jetzt %s von VM %d %s auf %s. %s", fmtTime(j.Time), key, j.Target, j.TargetName, j.Storage, note))
	c.pruneDiskJobs()
}

// pruneDiskJobs forgets finished jobs older than a day.
func (c *Core) pruneDiskJobs() {
	c.diskRestore.mu.Lock()
	defer c.diskRestore.mu.Unlock()
	ids := make([]string, 0, len(c.diskRestore.jobs))
	for id, j := range c.diskRestore.jobs {
		if j.Ended > 0 && time.Since(time.Unix(j.Ended, 0)) > 24*time.Hour {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		delete(c.diskRestore.jobs, id)
	}
}
