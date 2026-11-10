package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
)

// Reading in a disk that holds a datastore of another Backup Server (another appliance, a
// customer's rotation disk, an archive from years ago): the datastore is added to this
// server with reuse-datastore, so its backups can be restored and tested like any other.

// ForeignPartition is a partition no datastore of the server uses.
type ForeignPartition struct {
	Disk      string `json:"disk"`
	Partition string `json:"partition"`
	DevPath   string `json:"devpath"`
	UUID      string `json:"uuid"`
	FS        string `json:"fs"`
	Size      int64  `json:"size"`
	Vendor    string `json:"vendor"`
	Model     string `json:"model"`
	USB       bool   `json:"usb"`
}

// FoundStore is a datastore directory found on a partition.
type FoundStore struct {
	Path string `json:"path"` // relative to the partition
	VMs  int    `json:"vms"`
	CTs  int    `json:"cts"`
}

// ForeignPartitions lists the partitions that could hold a foreign datastore: a filesystem
// PBS can use, not mounted and not the backing device of a configured datastore.
func (c *Core) ForeignPartitions(ctx context.Context, serverID string) ([]ForeignPartition, error) {
	pbs, _, err := c.PBS(serverID)
	if err != nil {
		return nil, err
	}
	disks, err := pbs.Disks(ctx)
	if err != nil {
		return nil, err
	}
	cfgs, err := pbs.Datastores(ctx)
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, d := range cfgs {
		if d.BackingDevice != "" {
			used[strings.ToLower(d.BackingDevice)] = true
		}
	}
	out := []ForeignPartition{}
	for _, d := range disks {
		for _, p := range d.Partitions {
			if p.UUID == "" || p.Mounted || used[strings.ToLower(p.UUID)] || (p.Filesystem != "ext4" && p.Filesystem != "xfs") {
				continue
			}
			out = append(out, ForeignPartition{Disk: d.Name, Partition: p.Name, DevPath: p.DevPath, UUID: p.UUID, FS: p.Filesystem,
				Size: p.Size, Vendor: strings.TrimSpace(d.Vendor), Model: strings.TrimSpace(d.Model), USB: d.DiskType == "usb"})
		}
	}
	return out, nil
}

var devPathRe = regexp.MustCompile(`^/dev/[A-Za-z0-9/_.-]+$`)

// scanScript mounts a partition read-only, lists the datastores on it (directories with a
// .chunks folder) with their number of VMs and containers, and unmounts it again.
const scanScript = `set -u
d=$(mktemp -d /tmp/pk95scan.XXXXXX) || exit 1
trap 'umount "$d" 2>/dev/null; rmdir "$d"' EXIT
mount -o ro "$1" "$d" || exit 2
find "$d" -maxdepth 4 -type d -name .chunks -prune -print 2>/dev/null | while read -r c; do
  h=${c%/.chunks}; r=${h#"$d"}; r=${r#/}
  printf 'STORE\t%s\t%s\t%s\n' "$r" "$(ls -1 "$h/vm" 2>/dev/null | wc -l)" "$(ls -1 "$h/ct" 2>/dev/null | wc -l)"
done`

// ScanPartition looks for datastores on a foreign partition (needs the server's shell).
func (c *Core) ScanPartition(ctx context.Context, serverID, uuid string) ([]FoundStore, error) {
	srv, err := c.Server(serverID)
	if err != nil {
		return nil, err
	}
	part, err := c.foreignPartition(ctx, serverID, uuid)
	if err != nil {
		return nil, err
	}
	if !devPathRe.MatchString(part.DevPath) {
		return nil, fmt.Errorf("ungewöhnlicher Gerätepfad %q", part.DevPath)
	}
	r, err := c.Runner(srv)
	if err != nil {
		return nil, err
	}
	out, err := r.Run(ctx, "sh", "-c", scanScript, "sh", part.DevPath)
	if err != nil {
		return nil, fmt.Errorf("Platte lesen: %w", err)
	}
	found := []FoundStore{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), "\t")
		if len(f) != 4 || f[0] != "STORE" {
			continue
		}
		vms, _ := strconv.Atoi(strings.TrimSpace(f[2]))
		cts, _ := strconv.Atoi(strings.TrimSpace(f[3]))
		found = append(found, FoundStore{Path: f[1], VMs: vms, CTs: cts})
	}
	return found, nil
}

func (c *Core) foreignPartition(ctx context.Context, serverID, uuid string) (ForeignPartition, error) {
	parts, err := c.ForeignPartitions(ctx, serverID)
	if err != nil {
		return ForeignPartition{}, err
	}
	for _, p := range parts {
		if strings.EqualFold(p.UUID, uuid) {
			return p, nil
		}
	}
	return ForeignPartition{}, errors.New("Partition nicht gefunden oder schon als Datastore eingetragen – steckt die Platte noch?")
}

// ImportRequest describes a foreign datastore to read in.
type ImportRequest struct {
	ServerID string `json:"server_id"`
	UUID     string `json:"uuid"`
	Path     string `json:"path"`  // datastore directory on the partition
	Store    string `json:"store"` // name on this server
	Name     string `json:"name"`  // shown name, e.g. "Platte von Müller GmbH"
	Location string `json:"location"`
	Note     string `json:"note"`
}

var relPathRe = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// Validate checks the request (the store name is checked by the API).
func (r *ImportRequest) Validate() map[string]string {
	errs := map[string]string{}
	r.Name, r.Path = strings.TrimSpace(r.Name), strings.Trim(strings.TrimSpace(r.Path), "/")
	if r.Name == "" {
		errs["name"] = "Bitte einen Namen angeben, unter dem die Platte hier erscheint."
	}
	if r.UUID == "" {
		errs["uuid"] = "Platte wählen."
	}
	if r.Path == "" {
		errs["path"] = "Ordner des Datastores auf der Platte angeben."
	} else if !relPathRe.MatchString(r.Path) || strings.Contains("/"+r.Path+"/", "/../") {
		errs["path"] = "Ordner relativ zur Platte, z. B. usb-montag."
	}
	return errs
}

// ImportDisk adds the foreign datastore, mounts it and records it as an archive disk. The
// disk stays mounted so that it can be restored from right away.
func (c *Core) ImportDisk(ctx context.Context, req ImportRequest) (Archive, error) {
	pbs, srv, err := c.PBS(req.ServerID)
	if err != nil {
		return Archive{}, err
	}
	part, err := c.foreignPartition(ctx, req.ServerID, req.UUID)
	if err != nil {
		return Archive{}, err
	}
	err = pbs.CreateDatastore(ctx, prox.Params{
		"name": req.Store, "path": req.Path, "backing-device": part.UUID, "reuse-datastore": true,
		"comment": "Eingelesen: " + req.Name,
	})
	if err != nil {
		return Archive{}, fmt.Errorf("Datastore einbinden: %w", err)
	}
	undo := func(cause error) (Archive, error) {
		if upid, err := pbs.Unmount(ctx, req.Store); err == nil {
			_, _ = pbs.WaitTask(ctx, upid, 2*time.Minute)
		}
		_ = pbs.DeleteDatastore(ctx, req.Store, false)
		return Archive{}, cause
	}
	if err := c.mountStore(ctx, pbs, req.Store); err != nil {
		return undo(fmt.Errorf("Platte einhängen: %w", err))
	}
	items, err := c.archiveContents(ctx, pbs, req.Store)
	if err != nil {
		return undo(fmt.Errorf("Sicherungen lesen: %w", err))
	}
	if len(items) == 0 {
		return undo(fmt.Errorf("im Ordner %s gibt es keine Sicherungen – ist das der richtige Ordner?", req.Path))
	}
	now := time.Now().Unix()
	a := Archive{ID: NewID(6), Name: req.Name, ServerID: req.ServerID, Store: req.Store, SourceServer: req.ServerID, SourceStore: req.Store,
		Groups: []string{}, Location: strings.TrimSpace(req.Location), Note: strings.TrimSpace(req.Note), Created: now,
		Imported: true, Status: "ok", Start: now, End: now, Contents: items, Checks: []ArchiveRun{}}
	if us, err := pbs.Usage(ctx); err == nil {
		for _, u := range us {
			if u.Store == req.Store {
				a.Used, a.Total = u.Used, u.Total
			}
		}
	}
	if err := c.St.Put(KindArchive, a.ID, a); err != nil {
		return a, err
	}
	det := &notify.Details{Status: "ok", Link: "/wechselplatten", RowsTitle: "Sicherungen auf der Platte"}
	det.Facts = []notify.Fact{{Label: "Festplatte", Value: "„" + a.Name + "“"}, {Label: "Backup-Server", Value: srv.Name},
		{Label: "Datastore", Value: req.Store + " (Ordner " + req.Path + ")"}}
	for _, it := range items {
		kind := strings.ToUpper(it.Type)
		if kind == "HOST" {
			kind = "Host"
		}
		det.Rows = append(det.Rows, notify.Row{Kind: kind, ID: it.ID, Name: it.Name, Status: "ok", Time: it.Time, Size: fmtBytes(it.Size)})
	}
	c.EventDetails("info", archiveSource, "Fremde Platte „"+a.Name+"“ eingelesen",
		fmt.Sprintf("Die Platte ist als Datastore %s eingebunden und eingehängt. Sie enthält Sicherungen von %d Servern, die jetzt wiederhergestellt und getestet werden können.", req.Store, len(items)), det)
	c.RequestRefresh()
	return a, nil
}

// EjectArchive unmounts an archive disk so it can be pulled.
func (c *Core) EjectArchive(ctx context.Context, id string) error {
	a, err := c.GetArchive(id)
	if err != nil {
		return err
	}
	if c.ArchiveBusy(id) {
		return errors.New("Für diese Platte läuft gerade etwas.")
	}
	pbs, _, err := c.PBS(a.ServerID)
	if err != nil {
		return err
	}
	upid, err := pbs.Unmount(ctx, a.Store)
	if err != nil {
		return err
	}
	_, err = pbs.WaitTask(ctx, upid, 10*time.Minute)
	c.RequestRefresh()
	return err
}
