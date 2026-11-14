package api

import (
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/prox"
)

// Bandwidth: traffic control rules of a Backup Server (limits for everything that comes in –
// backups from Proxmox VE, copies fetched from here – or goes out, at chosen times) and the
// upload limit of a cloud target.

var (
	ruleNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{1,31}$`)
	hhmmRe     = regexp.MustCompile(`^([01]?\d|2[0-3]):[0-5]\d$`)
	daysRe     = regexp.MustCompile(`^(mon|tue|wed|thu|fri|sat|sun)(\.\.(mon|tue|wed|thu|fri|sat|sun))?(,(mon|tue|wed|thu|fri|sat|sun)(\.\.(mon|tue|wed|thu|fri|sat|sun))?)*$`)
)

type trafficReq struct {
	Name      string `json:"name"`
	Days      string `json:"days"` // always, mon..fri, mon..sun, or a day list (sat,sun)
	From      string `json:"from"` // 07:00
	To        string `json:"to"`   // 19:00
	Networks  string `json:"networks"`
	RateIn    int    `json:"rate_in"`  // MB/s, 0 = no limit
	RateOut   int    `json:"rate_out"` // MB/s
	Comment   string `json:"comment"`
	Timeframe string `json:"-"`
}

// params checks the request and turns it into a rule.
func (t *trafficReq) params(errs map[string]string) prox.Params {
	if t.RateIn < 0 || t.RateOut < 0 || t.RateIn > 100000 || t.RateOut > 100000 {
		errs["rate_in"] = "0 (unbegrenzt) bis 100000 MB/s."
	}
	if t.RateIn == 0 && t.RateOut == 0 {
		errs["rate_in"] = "Mindestens eine Grenze angeben."
	}
	if t.Days != "always" {
		if !daysRe.MatchString(t.Days) {
			errs["days"] = "Tage wählen."
		}
		if !hhmmRe.MatchString(t.From) {
			errs["from"] = "Uhrzeit wie 07:00."
		}
		if !hhmmRe.MatchString(t.To) {
			errs["to"] = "Uhrzeit wie 19:00."
		}
		t.Timeframe = t.Days + " " + t.From + "-" + t.To
	}
	nets := []string{}
	for _, n := range strings.FieldsFunc(t.Networks, func(r rune) bool { return r == ',' || r == ' ' || r == ';' || r == '\n' }) {
		if _, _, err := net.ParseCIDR(n); err == nil {
			nets = append(nets, n)
		} else if ip := net.ParseIP(n); ip != nil {
			if ip.To4() != nil {
				nets = append(nets, n+"/32")
			} else {
				nets = append(nets, n+"/128")
			}
		} else {
			errs["networks"] = "„" + n + "“ ist keine IP-Adresse und kein Netz (z. B. 10.0.0.0/8)."
		}
	}
	if len(nets) == 0 {
		nets = []string{"0.0.0.0/0", "::/0"}
	}
	p := prox.Params{"network": nets}
	if c := strings.TrimSpace(t.Comment); c != "" {
		p["comment"] = c
	}
	if t.RateIn > 0 {
		p["rate-in"] = strconv.Itoa(t.RateIn) + "MB"
	}
	if t.RateOut > 0 {
		p["rate-out"] = strconv.Itoa(t.RateOut) + "MB"
	}
	if t.Timeframe != "" {
		p["timeframe"] = []string{t.Timeframe}
	}
	return p
}

func (s *Server) listTraffic(w http.ResponseWriter, r *http.Request) {
	pbs, _, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	rules, err := pbs.TrafficRules(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Name < rules[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

func (s *Server) saveTraffic(w http.ResponseWriter, r *http.Request) {
	var req trafficReq
	if !decode(w, r, &req) {
		return
	}
	pbs, srv, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	errs := map[string]string{}
	p := req.params(errs)
	if fieldErrs(w, errs) {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		name = "pk95-" + core.NewID(3)
		if req.Name != "" && ruleNameRe.MatchString(req.Name) {
			name = req.Name
		}
		p["name"] = name
		err = pbs.CreateTrafficRule(r.Context(), p)
	} else {
		var del []string
		for _, k := range []string{"rate-in", "rate-out", "timeframe", "comment"} {
			if _, set := p[k]; !set {
				del = append(del, k)
			}
		}
		if len(del) > 0 {
			p["delete"] = del
		}
		err = pbs.UpdateTrafficRule(r.Context(), name, p)
	}
	if err != nil {
		fail(w, err)
		return
	}
	when := "immer"
	if req.Timeframe != "" {
		when = req.Timeframe
	}
	s.C.Event("info", srv.Name, "Bandbreite begrenzt: "+name, when+" – Empfang "+rateText(req.RateIn)+", Senden "+rateText(req.RateOut)+" (von "+currentUser(r).Username+")")
	writeJSON(w, http.StatusOK, map[string]any{"name": name})
}

func rateText(mb int) string {
	if mb == 0 {
		return "unbegrenzt"
	}
	return strconv.Itoa(mb) + " MB/s"
}

func (s *Server) deleteTraffic(w http.ResponseWriter, r *http.Request) {
	pbs, srv, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if err := pbs.DeleteTrafficRule(r.Context(), r.PathValue("name")); err != nil {
		fail(w, err)
		return
	}
	s.C.Event("info", srv.Name, "Bandbreiten-Regel "+r.PathValue("name")+" entfernt", "von "+currentUser(r).Username)
	ok(w)
}

// limitS3 sets the upload limit of a cloud target (0 = no limit).
func (s *Server) limitS3(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RateOut int `json:"rate_out"` // MB/s
	}
	if !decode(w, r, &req) {
		return
	}
	if req.RateOut < 0 || req.RateOut > 100000 {
		fieldErrs(w, map[string]string{"rate_out": "0 (unbegrenzt) bis 100000 MB/s."})
		return
	}
	pbs, srv, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	p := prox.Params{"delete": []string{"rate-out"}}
	if req.RateOut > 0 {
		p = prox.Params{"rate-out": strconv.Itoa(req.RateOut) + "MB"}
	}
	if err := pbs.UpdateS3Endpoint(r.Context(), r.PathValue("ep"), p); err != nil {
		fail(w, err)
		return
	}
	s.C.Event("info", srv.Name, "Upload zum S3-Ziel "+r.PathValue("ep")+": "+rateText(req.RateOut), "von "+currentUser(r).Username)
	ok(w)
}
