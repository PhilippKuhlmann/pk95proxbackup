package fake

import (
	"archive/zip"
	"encoding/base64"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"
)

// fileTree is the content of every simulated backup: dirs end with "/".
var fileTree = []string{
	"etc/hostname", "etc/hosts", "etc/fstab", "etc/nginx/nginx.conf", "etc/nginx/sites-enabled/default",
	"home/anna/Dokumente/Rechnung-2026-10.pdf", "home/anna/Dokumente/Angebot Müller.docx", "home/anna/Dokumente/Projekte/",
	"home/anna/Bilder/Firmenfeier.jpg", "home/anna/.bashrc",
	"srv/daten/Buchhaltung/2026/Oktober.xlsx", "srv/daten/Buchhaltung/2026/November.xlsx", "srv/daten/Vorlagen/Briefkopf.dotx",
	"var/log/syslog", "var/www/html/index.html", "root/.bashrc",
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
	prefix := "/root.pxar.didx"
	if isVM {
		prefix = "/drive-scsi0.img.fidx/part/2"
		time.Sleep(1500 * time.Millisecond) // the helper VM boots
	}
	entry := func(full, text, typ string, size int64) map[string]any {
		return map[string]any{"filepath": base64.StdEncoding.EncodeToString([]byte(full)), "text": text, "type": typ,
			"leaf": typ == "f", "size": size, "mtime": time.Now().Add(-26 * time.Hour).Unix()}
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
	case dir == "" && !isVM:
		out = append(out, entry("/root.pxar.didx", "root.pxar.didx", "v", 0))
	case strings.HasPrefix(dir, prefix):
		rel := strings.TrimPrefix(strings.TrimPrefix(dir, prefix), "/")
		seen := map[string]bool{}
		for _, f := range fileTree {
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
				out = append(out, entry(full, name, "f", int64(len(name))*7919))
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
