package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"sort"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/enckey"
	"pk95proxbackup/internal/prox"
	"pk95proxbackup/internal/totp"
)

// Encryption keys for backups: Proxmox VE encrypts on the client before anything reaches the
// Backup Server, so USB disks that leave the house carry only encrypted data. The appliance keeps
// every key (and helps to keep a copy outside), because without it the backups are lost.

type keyUse struct {
	Cluster     string `json:"cluster"`
	ClusterName string `json:"cluster_name"`
	Storage     string `json:"storage"`
	Target      string `json:"target"` // Backup Server / datastore
	KeyFP       string `json:"key_fp,omitempty"`
	KeyID       string `json:"key_id,omitempty"`
	KeyName     string `json:"key_name,omitempty"`
	State       string `json:"state"` // plain, ok, missing (key not in the appliance)
	Disabled    bool   `json:"disabled,omitempty"`
}

type keyView struct {
	ID          string   `json:"id"`
	Fingerprint string   `json:"fingerprint"`
	Short       string   `json:"short"`
	Name        string   `json:"name"`
	Created     int64    `json:"created"`
	Saved       int64    `json:"saved,omitempty"`
	Used        []keyUse `json:"used"`
}

// keyUsage reads the PBS storages of all clusters.
func (s *Server) keyUsage(ctx context.Context) ([]keyUse, []string) {
	clusters, _ := s.C.Clusters()
	var uses []keyUse
	var errs []string
	for _, cl := range clusters {
		pve, _, err := s.C.PVE(cl.ID)
		if err != nil {
			errs = append(errs, cl.Name+": "+err.Error())
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		list, err := pve.Storages(cctx)
		cancel()
		if err != nil {
			errs = append(errs, cl.Name+": "+err.Error())
			continue
		}
		for _, st := range list {
			if st.Type != "pbs" {
				continue
			}
			u := keyUse{Cluster: cl.ID, ClusterName: cl.Name, Storage: st.Storage, Target: st.Server + " / " + st.Datastore, State: "plain", Disabled: st.Disable != 0}
			if srv, ok := s.matchServer(st); ok {
				u.Target = srv.Name + " / " + st.Datastore
			}
			if st.EncryptionKey != "" {
				u.KeyFP, u.State = st.EncryptionKey, "missing"
				if k, ok := s.C.EncKey(st.EncryptionKey); ok {
					u.KeyID, u.KeyName, u.State = enckey.ID(k.Fingerprint), k.Name, "ok"
				}
			}
			uses = append(uses, u)
		}
	}
	return uses, errs
}

// listEncKeys lists the keys (without secret) and the PBS storages of all clusters with their
// encryption; ?usage=0 skips asking the clusters.
func (s *Server) listEncKeys(w http.ResponseWriter, r *http.Request) {
	var uses []keyUse
	var errs []string
	withUsage := r.URL.Query().Get("usage") != "0"
	if withUsage {
		uses, errs = s.keyUsage(r.Context())
	}
	keys := []keyView{}
	for _, k := range s.C.EncKeys() {
		v := keyView{ID: enckey.ID(k.Fingerprint), Fingerprint: k.Fingerprint, Short: enckey.Short(k.Fingerprint), Name: k.Name, Created: k.Created, Saved: k.Saved, Used: []keyUse{}}
		for _, u := range uses {
			if u.KeyID == v.ID {
				v.Used = append(v.Used, u)
			}
		}
		if !withUsage {
			v.Used = nil // unknown
		}
		keys = append(keys, v)
	}
	sort.SliceStable(keys, func(i, j int) bool { return keys[i].Created < keys[j].Created })
	if uses == nil {
		uses = []keyUse{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys, "storages": uses, "errors": errs})
}

// addEncKey creates a new key or imports one (JSON key file or paper key).
func (s *Server) addEncKey(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name, Key string }
	if !decode(w, r, &req) {
		return
	}
	k, existed, err := s.C.AddEncKey(req.Name, req.Key)
	if err != nil {
		fieldErrs(w, map[string]string{"key": err.Error()})
		return
	}
	if !existed {
		what := "erzeugt"
		if req.Key != "" {
			what = "importiert"
		}
		s.C.Event("info", "Appliance", "Schlüssel „"+k.Name+"“ "+what, "Fingerabdruck "+k.Fingerprint+" (von "+currentUser(r).Username+").")
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": enckey.ID(k.Fingerprint), "name": k.Name, "existed": existed})
}

// getEncKey returns a key with its secret, for saving it outside (download, paper).
func (s *Server) getEncKey(w http.ResponseWriter, r *http.Request) {
	k, found := s.C.EncKey(r.PathValue("key"))
	if !found {
		writeErr(w, http.StatusNotFound, "Schlüssel nicht gefunden.")
		return
	}
	paper := enckey.Paper(k.Key)
	qr, err := totp.QRSVG(paper)
	if err != nil {
		fail(w, err)
		return
	}
	s.C.Event("info", "Appliance", "Schlüssel „"+k.Name+"“ angezeigt", "Von "+currentUser(r).Username+" zum Sichern geöffnet.")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"id": enckey.ID(k.Fingerprint), "fingerprint": k.Fingerprint, "name": k.Name, "created": k.Created,
		"saved": k.Saved, "key": k.Key, "paper": paper, "qr": qr})
}

var paperTmpl = template.Must(template.New("p").Parse(`<!doctype html>
<html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Schlüssel {{.Name}}</title>
<style>
* { box-sizing:border-box; }
body { margin:0; background:#f3f4f6; color:#111827; font:14px/1.5 "Space Grotesk",-apple-system,"Segoe UI",Roboto,Helvetica,Arial,sans-serif; }
.page { max-width:820px; margin:24px auto; background:#fff; border:1px solid #e5e7eb; border-radius:4px; padding:28px 32px; }
h1 { font-size:22px; margin:0 0 4px; } .muted { color:#6b7280; }
.warn { margin:16px 0; padding:12px 14px; border-left:5px solid #d97706; background:#fef3c7; color:#78350f; }
.grid { display:grid; grid-template-columns:240px 1fr; gap:24px; align-items:start; margin-top:18px; }
.qr svg { width:240px; height:240px; display:block; }
pre { margin:0; font:12px/1.45 Menlo,Consolas,monospace; white-space:pre-wrap; word-break:break-all; background:#f9fafb; border:1px solid #e5e7eb; padding:12px; border-radius:3px; }
dl { display:grid; grid-template-columns:max-content 1fr; gap:4px 14px; margin:14px 0 0; } dt { color:#6b7280; } dd { margin:0; font-family:Menlo,Consolas,monospace; font-size:12px; word-break:break-all; }
ol { padding-left:20px; } button { font:inherit; padding:8px 14px; border:0; border-radius:3px; background:#1f73d6; color:#fff; cursor:pointer; }
@media print { body { background:#fff; } .page { border:0; margin:0; padding:0; } .noprint { display:none; } }
@media (max-width:640px) { .grid { grid-template-columns:1fr; } }
</style></head><body><div class="page">
<div class="noprint" style="text-align:right"><button type="button" onclick="window.print()">Drucken</button></div>
<h1>Schlüssel für Sicherungen: {{.Name}}</h1>
<div class="muted">PK95 ProxBackup · {{.Host}} · gedruckt am {{.Now}}</div>
<div class="warn"><b>Wer diesen Zettel hat, kann die Sicherungen lesen.</b> Sicher aufbewahren (Tresor, versiegelter Umschlag) – getrennt von den Sicherungsplatten. Ohne diesen Schlüssel lassen sich die verschlüsselten Sicherungen nicht wiederherstellen.</div>
<dl><dt>Fingerabdruck</dt><dd>{{.Fingerprint}}</dd><dt>Erstellt</dt><dd>{{.Created}}</dd></dl>
<div class="grid"><div class="qr">{{.QR}}</div><pre>{{.Paper}}</pre></div>
<h2 style="font-size:16px;margin:22px 0 6px">Wiederherstellen mit diesem Zettel</h2>
<ol>
<li>In PK95 ProxBackup unter Einstellungen → Verschlüsselung → „Schlüssel importieren“ den QR-Code mit dem Telefon lesen oder den Text oben abtippen und einfügen. Die Appliance prüft den Fingerabdruck und meldet Tippfehler.</li>
<li>Ohne Appliance: in Proxmox VE unter Rechenzentrum → Speicher → PBS-Speicher bearbeiten → Verschlüsselung den vorhandenen Schlüssel hochladen – den Text von <code>{</code> bis <code>}</code> einfügen.</li>
</ol>
</div></body></html>`))

// paperEncKey renders the key as a printable page.
func (s *Server) paperEncKey(w http.ResponseWriter, r *http.Request) {
	k, found := s.C.EncKey(r.PathValue("key"))
	if !found {
		writeErr(w, http.StatusNotFound, "Schlüssel nicht gefunden.")
		return
	}
	paper := enckey.Paper(k.Key)
	qr, err := totp.QRSVG(paper)
	if err != nil {
		fail(w, err)
		return
	}
	host, _ := os.Hostname()
	var b bytes.Buffer
	err = paperTmpl.Execute(&b, map[string]any{"Name": k.Name, "Fingerprint": k.Fingerprint, "Paper": paper, "QR": template.HTML(qr), "Host": host,
		"Now": time.Now().Format("02.01.2006 15:04"), "Created": time.Unix(k.Created, 0).Format("02.01.2006 15:04")})
	if err != nil {
		fail(w, err)
		return
	}
	s.C.Event("info", "Appliance", "Schlüssel „"+k.Name+"“ als Papierschlüssel geöffnet", "Von "+currentUser(r).Username+".")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; script-src 'unsafe-inline'")
	_, _ = w.Write(b.Bytes())
}

func (s *Server) putEncKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string `json:"name"`
		Saved bool   `json:"saved"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := s.C.SetEncKey(r.PathValue("key"), req.Name, req.Saved); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	ok(w)
}

// deleteEncKey removes a key from the appliance; not while a storage in Proxmox VE uses it.
func (s *Server) deleteEncKey(w http.ResponseWriter, r *http.Request) {
	var req struct{ Password string }
	if !decode(w, r, &req) {
		return
	}
	u := currentUser(r)
	if !checkPassword(w, u, req.Password) {
		return
	}
	k, found := s.C.EncKey(r.PathValue("key"))
	if !found {
		writeErr(w, http.StatusNotFound, "Schlüssel nicht gefunden.")
		return
	}
	uses, errs := s.keyUsage(r.Context())
	if len(errs) > 0 {
		writeErr(w, http.StatusConflict, "Nicht alle Proxmox-VE-Cluster sind erreichbar – ob der Schlüssel noch verwendet wird, lässt sich nicht prüfen.")
		return
	}
	for _, u := range uses {
		if u.KeyID == enckey.ID(k.Fingerprint) {
			writeErr(w, http.StatusConflict, fmt.Sprintf("Der Speicher „%s“ in %s verschlüsselt mit diesem Schlüssel. Erst dort die Verschlüsselung ausschalten.", u.Storage, u.ClusterName))
			return
		}
	}
	if err := s.C.DeleteEncKey(k.Fingerprint); err != nil {
		fail(w, err)
		return
	}
	s.C.Event("warning", "Appliance", "Schlüssel „"+k.Name+"“ gelöscht",
		"Fingerabdruck "+k.Fingerprint+" (von "+u.Username+"). Sicherungen, die damit verschlüsselt sind, lassen sich nur noch mit einer Kopie des Schlüssels wiederherstellen.")
	ok(w)
}

// setStorageKey switches encryption of a PVE storage on (new or stored key) or off.
func (s *Server) setStorageKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"` // "new", ID of a stored key, "" = off
	}
	if !decode(w, r, &req) {
		return
	}
	pve, cl, err := s.C.PVE(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	name := r.PathValue("storage")
	ctx := r.Context()
	var cur prox.Storage
	if list, err := pve.Storages(ctx); err == nil {
		for _, st := range list {
			if st.Storage == name {
				cur = st
			}
		}
	} else {
		fail(w, err)
		return
	}
	if cur.Type != "pbs" {
		writeErr(w, http.StatusNotFound, "Kein PBS-Speicher „"+name+"“ in "+cl.Name+".")
		return
	}
	if req.Key == "" {
		if err := pve.SetStorageKey(ctx, name, ""); err != nil {
			fail(w, err)
			return
		}
		s.C.Event("warning", cl.Name, "Verschlüsselung für "+name+" ausgeschaltet",
			"Neue Sicherungen auf "+name+" sind nicht mehr verschlüsselt (von "+currentUser(r).Username+"). Bisherige verschlüsselte Sicherungen brauchen zum Wiederherstellen weiter den Schlüssel.")
		ok(w)
		return
	}
	var key core.EncKey
	created := false
	if req.Key == "new" {
		if key, _, err = s.C.AddEncKey(cl.Name+" / "+name, ""); err != nil {
			fail(w, err)
			return
		}
		created = true
	} else {
		k, found := s.C.EncKey(req.Key)
		if !found {
			fieldErrs(w, map[string]string{"key": "Schlüssel nicht gefunden."})
			return
		}
		key = k
	}
	if err := pve.SetStorageKey(ctx, name, key.Key); err != nil {
		if created {
			_ = s.C.DeleteEncKey(key.Fingerprint)
		}
		fail(w, err)
		return
	}
	what := "eingeschaltet"
	if cur.EncryptionKey != "" {
		what = "Schlüssel gewechselt"
	}
	s.C.Event("info", cl.Name, "Verschlüsselung für "+name+": "+what,
		"Neue Sicherungen auf "+name+" werden mit „"+key.Name+"“ ("+enckey.Short(key.Fingerprint)+") verschlüsselt (von "+currentUser(r).Username+").")
	resp := map[string]any{"ok": true, "key": enckey.ID(key.Fingerprint)}
	if created {
		resp["new_key"] = enckey.ID(key.Fingerprint)
	}
	writeJSON(w, http.StatusOK, resp)
}

// missingKeyError reports an encrypted backup whose key the appliance does not have.
type missingKeyError struct{ msg string }

func (e missingKeyError) Error() string { return e.msg }

// storageErr words an error of ensurePVEStorage for the user.
func storageErr(err error, store, cluster string) error {
	var mk missingKeyError
	if errors.As(err, &mk) {
		return mk
	}
	return fmt.Errorf("Der Datastore %s ist in „%s“ nicht als Speicher eingetragen, und das Eintragen hat nicht geklappt: %w", store, cluster, err)
}
