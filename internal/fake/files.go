package fake

import (
	"archive/zip"
	"encoding/base64"
	"net/http"
	"path"
	"slices"
	"sort"
	"strings"
	"time"
)

// fileTree is the content of every simulated backup: dirs end with "/".
var fileTree = []string{
	"etc/hostname", "etc/hosts", "etc/fstab", "etc/nginx/nginx.conf", "etc/nginx/sites-enabled/default",
	"home/anna/Dokumente/Rechnung-2026-10.pdf", "home/anna/Dokumente/Angebot Müller.docx",
	"home/anna/Bilder/Firmenfeier.jpg", "home/anna/.bashrc",
	"srv/daten/Buchhaltung/2026/Oktober.xlsx", "srv/daten/Buchhaltung/2026/November.xlsx", "srv/daten/Vorlagen/Briefkopf.dotx",
	"var/log/syslog", "var/www/html/index.html", "root/.bashrc",
}

// changeEvery makes some files change between backups: every n days a new version (size and
// modification time); all other files stay the same in every backup.
var changeEvery = map[string]int{
	"home/anna/Dokumente/Angebot Müller.docx":       3,
	"home/anna/Dokumente/Projekte/Kalkulation.xlsx": 2,
	"srv/daten/Buchhaltung/2026/November.xlsx":      1,
	"var/log/syslog":                  1,
	"etc/nginx/sites-enabled/default": 9,
}

// backupTime is the time of the backup a volume id names (zero if unknown).
func backupTime(volume string) time.Time {
	if i := strings.LastIndex(volume, "/"); i >= 0 {
		if t, err := time.Parse("2006-01-02T15:04:05Z", volume[i+1:]); err == nil {
			return t
		}
	}
	return time.Time{}
}

// treeFor varies the content by backup day, so a folder can be missing in some backups:
// "Projekte" exists only in backups taken on even days.
func treeFor(volume string) []string {
	if t := backupTime(volume); !t.IsZero() && t.Day()%2 == 0 {
		return append(slices.Clone(fileTree), "home/anna/Dokumente/Projekte/Neubau/Grundriss.pdf", "home/anna/Dokumente/Projekte/Kalkulation.xlsx")
	}
	return fileTree
}

// fileVersion is size and modification time of a file in the backup taken at bt.
func fileVersion(rel string, bt time.Time) (int64, int64) {
	size := int64(len(path.Base(rel))) * 7919
	n := changeEvery[rel]
	if n == 0 || bt.IsZero() {
		return size, time.Date(2026, 1, 15, 9, 12, 0, 0, time.UTC).Unix()
	}
	// The version changes every n days; the file was saved on that day at 10:00.
	day := bt.Truncate(24 * time.Hour)
	k := day.Unix() / 86400 / int64(n)
	changed := time.Unix(k*int64(n)*86400, 0).UTC().Add(10 * time.Hour)
	if changed.After(bt) {
		changed = changed.Add(-time.Duration(n) * 24 * time.Hour)
	}
	return size + (k%5)*4096, changed.Unix()
}

// fileList answers GET …/file-restore/list like Proxmox VE: VMs show disk → partition → files,
// containers the archive → files.
func (p *PVE) fileList(w http.ResponseWriter, volume, b64 string) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		apiErr(w, 400, "invalid filepath")
		return
	}
	dir := strings.TrimSuffix(string(raw), "/")
	isVM := strings.Contains(volume, ":backup/vm/")
	isHost := strings.Contains(volume, ":backup/host/")
	prefix := "/root.pxar.didx"
	tree := treeFor(volume)
	if isHost && dir != "" {
		arch, _, _ := strings.Cut(strings.TrimPrefix(dir, "/"), "/")
		prefix, tree = "/"+arch, hostTree[strings.TrimSuffix(arch, ".pxar.didx")]
	}
	if isVM {
		prefix = "/drive-scsi0.img.fidx/part/2"
		time.Sleep(1500 * time.Millisecond) // the helper VM boots
	}
	bt := backupTime(volume)
	mtime := time.Now().Add(-26 * time.Hour).Unix()
	entry := func(full, text, typ string, size int64) map[string]any {
		return map[string]any{"filepath": base64.StdEncoding.EncodeToString([]byte(full)), "text": text, "type": typ,
			"leaf": typ == "f", "size": size, "mtime": mtime}
	}
	out := []map[string]any{}
	switch {
	case dir == "" && isVM:
		out = append(out, entry("/drive-scsi0.img.fidx", "drive-scsi0.img.fidx", "v", 64<<30))
	case dir == "/drive-scsi0.img.fidx":
		out = append(out, entry(dir+"/part", "part", "d", 0))
	case dir == "/drive-scsi0.img.fidx/part":
		out = append(out, entry(dir+"/1", "1", "d", 0), entry(dir+"/2", "2", "d", 0))
	case dir == "/drive-scsi0.img.fidx/part/1":
		out = append(out, entry(dir+"/EFI", "EFI", "d", 0))
	case dir == "" && isHost:
		for _, a := range []string{"etc", "info", "root"} {
			out = append(out, entry("/"+a+".pxar.didx", a+".pxar.didx", "v", 0))
		}
	case dir == "" && !isVM:
		out = append(out, entry("/root.pxar.didx", "root.pxar.didx", "v", 0))
	case strings.HasPrefix(dir, prefix) && tree != nil:
		rel := strings.TrimPrefix(strings.TrimPrefix(dir, prefix), "/")
		seen := map[string]bool{}
		for _, f := range tree {
			if rel != "" && !strings.HasPrefix(f, rel+"/") {
				continue
			}
			rest := strings.TrimPrefix(f, rel+"/")
			if rel == "" {
				rest = f
			}
			name, more, _ := strings.Cut(rest, "/")
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			full := path.Join(dir, name)
			if more != "" || strings.HasSuffix(rest, "/") {
				out = append(out, entry(full, name, "d", 0))
			} else {
				e := entry(full, name, "f", 0)
				e["size"], e["mtime"] = fileVersion(strings.TrimPrefix(f, "/"), bt)
				out = append(out, e)
			}
		}
		if len(out) == 0 && rel != "" {
			apiErr(w, 500, "unable to open directory "+rel+": No such file or directory")
			return
		}
	default:
		apiErr(w, 500, "path not found")
		return
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["text"].(string) < out[j]["text"].(string) })
	data(w, out)
}

// fileDownload answers GET …/file-restore/download: a file as is, a directory as zip.
func (p *PVE) fileDownload(w http.ResponseWriter, b64 string) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		apiErr(w, 400, "invalid filepath")
		return
	}
	name := path.Base(string(raw))
	if strings.Contains(name, ".") {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("Demo-Inhalt von " + string(raw) + "\n"))
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	zw := zip.NewWriter(w)
	for _, f := range []string{"liesmich.txt", "daten/beispiel.csv"} {
		fw, _ := zw.Create(name + "/" + f)
		_, _ = fw.Write([]byte("Demo-Inhalt aus " + string(raw) + "\n"))
	}
	_ = zw.Close()
}
