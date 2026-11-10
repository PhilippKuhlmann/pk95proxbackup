package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"pk95proxbackup/internal/core"
)

// One-off archive copies onto USB disks that are taken away or kept for years.

func (s *Server) listArchives(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, map[string]any{"archives": s.C.ArchiveStates(ctx)})
}

func (s *Server) createArchive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		core.Archive
		Confirm string `json:"confirm"` // disk name typed again
	}
	if !decode(w, r, &req) {
		return
	}
	a := req.Archive
	a.Store = strings.TrimSpace(a.Store)
	errs := s.C.ValidateArchive(&a)
	if !storeRe.MatchString(a.Store) && errs["store"] == "" {
		errs["store"] = "Datastore-Name: 3–32 Zeichen, Buchstaben, Ziffern, - und _."
	}
	if a.Disk != "" && req.Confirm != a.Disk {
		errs["confirm"] = "Zur Bestätigung den Namen der Platte (" + a.Disk + ") eintippen."
	}
	if _, err := s.C.Server(a.ServerID); err != nil && errs["server_id"] == "" {
		errs["server_id"] = "Backup-Server wählen."
	}
	if fieldErrs(w, errs) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	pbs, _, err := s.C.PBS(a.ServerID)
	if err != nil {
		fail(w, err)
		return
	}
	cfgs, err := pbs.Datastores(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	var exists, removable bool
	for _, c := range cfgs {
		if c.Name == a.Store {
			exists, removable = true, c.BackingDevice != ""
		}
	}
	if a.Disk != "" {
		if exists {
			fieldErrs(w, map[string]string{"store": "Einen Datastore mit diesem Namen gibt es schon."})
			return
		}
		disks, err := pbs.Disks(ctx)
		if err != nil {
			fail(w, err)
			return
		}
		found := false
		for _, d := range disks {
			if d.Name != a.Disk {
				continue
			}
			found = true
			for _, p := range d.Partitions {
				if p.Mounted {
					fieldErrs(w, map[string]string{"disk": "Auf " + a.Disk + " ist " + p.Name + " eingehängt – diese Platte wird benutzt."})
					return
				}
			}
		}
		if !found {
			fieldErrs(w, map[string]string{"disk": "Platte " + a.Disk + " nicht gefunden – steckt sie noch?"})
			return
		}
		if a.FS != "xfs" {
			a.FS = "ext4"
		}
	} else if !exists || !removable {
		fieldErrs(w, map[string]string{"store": "Kein Wechsel-Datastore mit diesem Namen auf dem Server."})
		return
	}
	a, err = s.C.StartArchive(a)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": a.ID})
}

func (s *Server) updateArchive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Location string `json:"location"`
		Note     string `json:"note"`
	}
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		fieldErrs(w, map[string]string{"name": "Bitte einen Namen angeben."})
		return
	}
	if _, err := s.C.UpdateArchiveInfo(r.PathValue("id"), req.Name, req.Location, req.Note); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) checkArchive(w http.ResponseWriter, r *http.Request) {
	if err := s.C.CheckArchive(r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) deleteArchive(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	a, err := s.C.GetArchive(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	forget := r.URL.Query().Get("store") == "1"
	if err := s.C.ForgetArchive(ctx, a.ID, forget); err != nil {
		fail(w, err)
		return
	}
	body := "Die Daten auf der Festplatte bleiben erhalten."
	if forget {
		body += " Der Wechsel-Datastore " + a.Store + " wurde aus der Konfiguration des Backup-Servers genommen."
	}
	s.C.Event("info", "Archiv-Sicherung", "Archiv „"+a.Name+"“ aus der Liste entfernt", body)
	ok(w)
}
