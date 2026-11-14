package api

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"pk95proxbackup/internal/prox"
)

// Cloud datastores (PBS 4): a datastore lies in a bucket of an S3 object store, with a local
// cache on the server. The appliance knows the address scheme of common providers, so the user
// only enters region, keys and bucket. The secret key goes straight to the Backup Server.

type s3Provider struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Endpoint  string   `json:"endpoint"` // {{bucket}}, {{region}} (PBS) and {{account}} (here)
	Regions   []string `json:"regions,omitempty"`
	Region    string   `json:"region,omitempty"` // default
	PathStyle bool     `json:"path_style,omitempty"`
	Account   bool     `json:"account,omitempty"` // needs an account ID (Cloudflare)
	Custom    bool     `json:"custom,omitempty"`  // own server: endpoint, port, fingerprint
	Help      string   `json:"help"`
}

var s3Providers = []s3Provider{
	{ID: "hetzner", Name: "Hetzner Object Storage", Endpoint: "{{bucket}}.{{region}}.your-objectstorage.com", Regions: []string{"fsn1", "nbg1", "hel1"}, Region: "fsn1",
		Help: "In der Hetzner Console unter Object Storage einen Bucket anlegen und unter „Zugangsdaten“ einen S3-Schlüssel erzeugen. Die Region ist der Standort des Buckets (fsn1 = Falkenstein)."},
	{ID: "backblaze", Name: "Backblaze B2", Endpoint: "{{bucket}}.s3.{{region}}.backblazeb2.com", Regions: []string{"eu-central-003", "us-west-004", "us-west-002", "us-east-005"}, Region: "eu-central-003",
		Help: "Einen privaten Bucket anlegen und unter Application Keys einen Schlüssel mit Lesen und Schreiben erzeugen (keyID = Access Key, applicationKey = Secret Key). Die Region steht beim Bucket im Endpoint s3.<region>.backblazeb2.com."},
	{ID: "wasabi", Name: "Wasabi", Endpoint: "{{bucket}}.s3.{{region}}.wasabisys.com", Regions: []string{"eu-central-1", "eu-central-2", "eu-west-1", "eu-west-2", "us-east-1", "us-east-2", "us-west-1"}, Region: "eu-central-1",
		Help: "Bucket anlegen und unter Access Keys einen Schlüssel erzeugen. Wasabi berechnet jedes Objekt mindestens 90 Tage – die Aufbewahrung nicht kürzer einstellen."},
	{ID: "aws", Name: "Amazon S3", Endpoint: "{{bucket}}.s3.{{region}}.amazonaws.com", Regions: []string{"eu-central-1", "eu-central-2", "eu-west-1", "eu-west-3", "eu-north-1", "us-east-1"}, Region: "eu-central-1",
		Help: "Bucket anlegen (Public Access blockiert) und einen IAM-Benutzer mit Zugriff nur auf diesen Bucket (Get, Put, List, Delete) und Access Key erzeugen."},
	{ID: "r2", Name: "Cloudflare R2", Endpoint: "{{account}}.r2.cloudflarestorage.com", Region: "auto", PathStyle: true, Account: true,
		Help: "Bucket anlegen und unter „R2 API-Token verwalten“ ein Token mit Object Read & Write erzeugen. Die Konto-ID steht rechts in der R2-Übersicht."},
	{ID: "custom", Name: "Eigener S3-Server", PathStyle: true, Custom: true,
		Help: "MinIO, Ceph RGW, Garage, TrueNAS oder ein NAS mit S3-Dienst. Nur HTTPS; bei selbst signiertem Zertifikat den Fingerabdruck angeben."},
}

func s3ProviderByID(id string) (s3Provider, bool) {
	for _, p := range s3Providers {
		if p.ID == id {
			return p, true
		}
	}
	return s3Provider{}, false
}

// s3ProviderOf guesses the provider of a stored endpoint (for the list).
func s3ProviderOf(e prox.S3Endpoint) string {
	for _, p := range s3Providers {
		if p.Custom {
			continue
		}
		suffix := p.Endpoint[strings.LastIndex(p.Endpoint, "}}")+2:]
		if strings.HasSuffix(e.Endpoint, suffix) {
			return p.Name
		}
	}
	return "S3-Server " + e.Endpoint
}

var (
	bucketRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	regionRe   = regexp.MustCompile(`^[a-z0-9-]{2,32}$`)
	accountRe  = regexp.MustCompile(`^[a-f0-9]{32}$`)
	s3HostRe   = regexp.MustCompile(`^[A-Za-z0-9.{}\[\]:-]+$`) // may hold {{bucket}}
	s3KeyClean = regexp.MustCompile(`\s+`)
)

// s3Backend returns endpoint id and bucket of an S3 datastore backend ("" for a normal one).
func s3Backend(backend string) (client, bucket string) {
	if !strings.Contains(backend, "type=s3") {
		return "", ""
	}
	for _, kv := range strings.Split(backend, ",") {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "client":
			client = v
		case "bucket":
			bucket = v
		}
	}
	return client, bucket
}

// s3Error words the answers of S3 providers.
func s3Error(err error) string {
	m := err.Error()
	switch {
	case strings.Contains(m, "SignatureDoesNotMatch"), strings.Contains(m, "InvalidAccessKeyId"), strings.Contains(m, "InvalidToken"), strings.Contains(m, "InvalidSecret"):
		return "Access Key oder Secret Key stimmt nicht."
	case strings.Contains(m, "NoSuchBucket"):
		return "Diesen Bucket gibt es bei diesem Anbieter und in dieser Region nicht."
	case strings.Contains(m, "AccessDenied"), strings.Contains(m, "Forbidden"), strings.Contains(m, "403"):
		return "Der Schlüssel darf diesen Bucket nicht beschreiben – Rechte zum Lesen, Schreiben, Auflisten und Löschen von Objekten nötig."
	case strings.Contains(m, "dns"), strings.Contains(m, "resolve"), strings.Contains(m, "lookup"):
		return "Adresse des S3-Servers nicht gefunden – Region oder Endpoint prüfen."
	case strings.Contains(m, "certificate"), strings.Contains(m, "fingerprint"):
		return "Zertifikat des S3-Servers nicht vertrauenswürdig – bei selbst signiertem Zertifikat den Fingerabdruck angeben."
	}
	return "S3 antwortet: " + m
}

type s3View struct {
	prox.S3Endpoint
	Provider string   `json:"provider"`
	Stores   []string `json:"stores"`
}

// listS3 returns the S3 endpoints of a server, the providers and the free space for the cache.
func (s *Server) listS3(w http.ResponseWriter, r *http.Request) {
	pbs, _, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ctx := r.Context()
	eps, err := pbs.S3Endpoints(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Der Backup-Server kennt keine S3-Ziele (erst ab Proxmox Backup Server 4): "+err.Error())
		return
	}
	used := map[string][]string{}
	if cfgs, err := pbs.Datastores(ctx); err == nil {
		for _, c := range cfgs {
			if client, _ := s3Backend(c.Backend); client != "" {
				used[client] = append(used[client], c.Name)
			}
		}
	}
	out := []s3View{}
	for _, e := range eps {
		e.AccessKey = ""
		v := s3View{S3Endpoint: e, Provider: s3ProviderOf(e), Stores: used[e.ID]}
		if v.Stores == nil {
			v.Stores = []string{}
		}
		out = append(out, v)
	}
	var rootFree int64 = -1
	if ns, err := pbs.NodeStatus(ctx); err == nil && ns.Root.Total > 0 {
		rootFree = ns.Root.Total - ns.Root.Used
	}
	writeJSON(w, http.StatusOK, map[string]any{"endpoints": out, "providers": s3Providers, "root_free": rootFree})
}

// createS3 adds an S3 endpoint and lists its buckets. Wrong keys or an unreachable server undo it.
func (s *Server) createS3(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider    string `json:"provider"`
		Region      string `json:"region"`
		Account     string `json:"account"`
		Endpoint    string `json:"endpoint"`
		Port        int    `json:"port"`
		PathStyle   bool   `json:"path_style"`
		Fingerprint string `json:"fingerprint"`
		AccessKey   string `json:"access_key"`
		SecretKey   string `json:"secret_key"`
	}
	if !decode(w, r, &req) {
		return
	}
	pbs, srv, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	p, found := s3ProviderByID(req.Provider)
	errs := map[string]string{}
	if !found {
		errs["provider"] = "Anbieter wählen."
	}
	req.AccessKey, req.SecretKey = s3KeyClean.ReplaceAllString(req.AccessKey, ""), strings.TrimSpace(req.SecretKey)
	req.Region, req.Account = strings.TrimSpace(strings.ToLower(req.Region)), strings.TrimSpace(strings.ToLower(req.Account))
	if req.AccessKey == "" {
		errs["access_key"] = "Access Key eintragen."
	}
	if req.SecretKey == "" {
		errs["secret_key"] = "Secret Key eintragen."
	}
	endpoint, region, pathStyle, port, fp := p.Endpoint, req.Region, p.PathStyle, 0, ""
	switch {
	case p.Custom:
		endpoint = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(req.Endpoint), "https://"), "/")
		if !s3HostRe.MatchString(endpoint) {
			errs["endpoint"] = "Adresse des S3-Servers, z. B. 192.168.1.20 oder s3.example.org."
		}
		pathStyle, port, fp = req.PathStyle, req.Port, prox.NormalizeFingerprint(req.Fingerprint)
		if region == "" {
			region = "us-east-1"
		}
	case p.Account:
		if !accountRe.MatchString(req.Account) {
			errs["account"] = "Konto-ID: 32 Zeichen (0–9, a–f)."
		}
		endpoint, region = strings.ReplaceAll(endpoint, "{{account}}", req.Account), p.Region
	default:
		if region == "" {
			region = p.Region
		}
	}
	if !p.Account && !regionRe.MatchString(region) {
		errs["region"] = "Region angeben, z. B. " + p.Region + "."
	}
	if port < 0 || port > 65535 {
		errs["port"] = "Port 1–65535."
	}
	if fieldErrs(w, errs) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	existing := map[string]bool{}
	eps, err := pbs.S3Endpoints(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Der Backup-Server kennt keine S3-Ziele (erst ab Proxmox Backup Server 4): "+err.Error())
		return
	}
	for _, e := range eps {
		existing[e.ID] = true
	}
	base := p.ID
	if !p.Custom && !p.Account {
		base += "-" + region
	}
	base = strings.Trim(tokenNameRe.ReplaceAllString(strings.ToLower(base), "-"), "-.")
	if len(base) > 26 {
		base = base[:26]
	}
	id := base
	for i := 2; existing[id]; i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	params := prox.Params{"id": id, "endpoint": endpoint, "region": region, "access-key": req.AccessKey, "secret-key": req.SecretKey}
	if pathStyle {
		params["path-style"] = true
	}
	if port > 0 {
		params["port"] = port
	}
	if fp != "" {
		params["fingerprint"] = fp
	}
	if err := pbs.CreateS3Endpoint(ctx, params); err != nil {
		fail(w, fmt.Errorf("S3-Ziel anlegen: %w", err))
		return
	}
	buckets, err := pbs.S3Buckets(ctx, id)
	listErr := ""
	if err != nil {
		if !strings.Contains(err.Error(), "AccessDenied") && !strings.Contains(err.Error(), "403") {
			_ = pbs.DeleteS3Endpoint(context.WithoutCancel(ctx), id) // keys or address wrong: start again
			fieldErrs(w, map[string]string{"secret_key": s3Error(err)})
			return
		}
		listErr = "Der Schlüssel darf die Bucket-Liste nicht lesen (häufig bei Schlüsseln für einen einzelnen Bucket) – den Namen bitte eintippen."
		buckets = []string{}
	}
	sort.Strings(buckets)
	s.C.Event("info", srv.Name, "S3-Ziel "+id+" eingetragen", p.Name+" ("+endpoint+")")
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "buckets": buckets, "list_error": listErr})
}

// deleteS3 removes an S3 endpoint no datastore uses.
func (s *Server) deleteS3(w http.ResponseWriter, r *http.Request) {
	pbs, srv, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	id := r.PathValue("ep")
	if cfgs, err := pbs.Datastores(r.Context()); err == nil {
		for _, c := range cfgs {
			if client, _ := s3Backend(c.Backend); client == id {
				writeErr(w, http.StatusConflict, "Der Datastore „"+c.Name+"“ liegt in diesem S3-Ziel. Erst den Datastore entfernen.")
				return
			}
		}
	}
	if err := pbs.DeleteS3Endpoint(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	s.C.Event("info", srv.Name, "S3-Ziel "+id+" ausgetragen", "Die Daten im Bucket bleiben unverändert.")
	ok(w)
}

// checkS3 tests whether a bucket can be written (with the later datastore name as prefix).
func (s *Server) checkS3(w http.ResponseWriter, r *http.Request) {
	var req struct{ Bucket, Store string }
	if !decode(w, r, &req) {
		return
	}
	pbs, _, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if !bucketRe.MatchString(req.Bucket) {
		fieldErrs(w, map[string]string{"bucket": "Bucket-Name: 3–63 Zeichen, Kleinbuchstaben, Ziffern, Punkt und Bindestrich."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	if err := pbs.S3Check(ctx, r.PathValue("ep"), req.Bucket, req.Store); err != nil {
		fieldErrs(w, map[string]string{"bucket": s3Error(err)})
		return
	}
	ok(w)
}
