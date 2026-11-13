package core

import (
	"context"
	"errors"
	"time"

	"pk95proxbackup/internal/prox"
)

// Mails about backup jobs: Proxmox VE sends its own plain mail after each backup job. The
// appliance sends one in its own design (every server with result, duration, data). Per
// Proxmox VE the user chooses which; with the appliance, the backup jobs in Proxmox VE are set
// to send nothing (notification-mode legacy-sendmail without recipient). All other
// notifications of Proxmox VE stay as they are. The previous setting of each job is kept so
// that it can be switched back.

// PVEJobMail is how one backup job notifies.
type PVEJobMail struct {
	ID       string `json:"id"`
	Comment  string `json:"comment"`
	Mode     string `json:"mode"`
	MailTo   string `json:"mailto,omitempty"`
	PVESends bool   `json:"pve_sends"`
}

// PVEMail is the mail setting of one Proxmox VE.
type PVEMail struct {
	Appliance     bool         `json:"appliance"` // backup mails only from the appliance
	Jobs          []PVEJobMail `json:"jobs"`
	PVESends      int          `json:"pve_sends"` // jobs for which Proxmox VE still sends mail
	ApplianceMail bool         `json:"appliance_mail"`
	ApplianceTo   string       `json:"appliance_to"`
	MinSeverity   string       `json:"min_severity"`
}

type jobMailPrev struct {
	Mode   string `json:"mode"`
	MailTo string `json:"mailto"`
}

// jobSendsMail reports whether Proxmox VE sends its own mail for a backup job.
func jobSendsMail(j prox.BackupJob) bool {
	switch j.NotificationMode {
	case "legacy-sendmail":
		return j.MailTo != ""
	default: // auto, notification-system: the notification system routes it (to mail by default)
		return true
	}
}

// PVEMailState returns how the backup jobs of a Proxmox VE notify.
func (c *Core) PVEMailState(ctx context.Context, clusterID string) (PVEMail, error) {
	pve, _, err := c.PVE(clusterID)
	if err != nil {
		return PVEMail{}, err
	}
	var on bool
	_ = c.St.GetSetting("pvemail:"+clusterID, &on)
	ns := c.NotifySettings()
	st := PVEMail{Appliance: on, Jobs: []PVEJobMail{}, ApplianceMail: ns.MailEnabled && ns.MailTo != "", ApplianceTo: ns.MailTo, MinSeverity: ns.MinSeverity}
	jobs, err := pve.BackupJobs(ctx)
	if err != nil {
		return st, err
	}
	for _, j := range jobs {
		jm := PVEJobMail{ID: j.ID, Comment: j.Comment, Mode: j.NotificationMode, MailTo: j.MailTo, PVESends: jobSendsMail(j)}
		if jm.PVESends {
			st.PVESends++
		}
		st.Jobs = append(st.Jobs, jm)
	}
	return st, nil
}

// SetPVEMail switches the backup mails of a Proxmox VE to the appliance (on) or back.
func (c *Core) SetPVEMail(ctx context.Context, clusterID string, on bool) (PVEMail, error) {
	pve, cl, err := c.PVE(clusterID)
	if err != nil {
		return PVEMail{}, err
	}
	if on {
		ns := c.NotifySettings()
		if !ns.MailEnabled || ns.MailTo == "" {
			return PVEMail{}, errors.New("Die Appliance verschickt noch keine Mails: zuerst unter Einstellungen → Benachrichtigungen E-Mail einrichten.")
		}
	}
	jobs, err := pve.BackupJobs(ctx)
	if err != nil {
		return PVEMail{}, err
	}
	prev := map[string]jobMailPrev{}
	_ = c.St.GetSetting("pvemail-prev:"+clusterID, &prev)
	for _, j := range jobs {
		if on {
			if !jobSendsMail(j) {
				continue
			}
			prev[j.ID] = jobMailPrev{Mode: j.NotificationMode, MailTo: j.MailTo}
			if err := quietJob(ctx, pve, j.ID); err != nil {
				return PVEMail{}, err
			}
			continue
		}
		p, ok := prev[j.ID]
		if !ok {
			p = jobMailPrev{Mode: "notification-system"}
		}
		params := prox.Params{}
		var del []string
		if p.Mode == "" {
			del = append(del, "notification-mode")
		} else {
			params["notification-mode"] = p.Mode
		}
		if p.MailTo != "" {
			params["mailto"] = p.MailTo
		}
		if len(del) > 0 {
			params["delete"] = del[0]
		}
		if err := pve.UpdateBackupJob(ctx, j.ID, params); err != nil {
			return PVEMail{}, err
		}
		delete(prev, j.ID)
	}
	_ = c.St.SetSetting("pvemail-prev:"+clusterID, prev)
	_ = c.St.SetSetting("pvemail:"+clusterID, on)
	if on {
		c.Event("info", cl.Name, "Mails zu Sicherungsjobs kommen jetzt von der Appliance", "Proxmox VE verschickt für die Sicherungsjobs keine eigene Mail mehr; die Appliance meldet jeden Lauf mit allen Servern.")
	} else {
		c.Event("info", cl.Name, "Mails zu Sicherungsjobs wieder von Proxmox VE", "Die Sicherungsjobs melden sich wieder wie vorher über Proxmox VE.")
	}
	return c.PVEMailState(ctx, clusterID)
}

// PVEMailOn reports whether the appliance sends the backup mails of a Proxmox VE.
func (c *Core) PVEMailOn(clusterID string) bool {
	var on bool
	_ = c.St.GetSetting("pvemail:"+clusterID, &on)
	return on
}

func quietJob(ctx context.Context, pve *prox.PVE, id string) error {
	return pve.UpdateBackupJob(ctx, id, prox.Params{"notification-mode": "legacy-sendmail", "delete": "mailto"})
}

// enforcePVEMail quiets backup jobs added in Proxmox VE itself while the appliance sends the
// mails (every 30 minutes).
func (c *Core) enforcePVEMail(now time.Time) {
	var last int64
	_ = c.St.GetSetting("pvemail-check", &last)
	if now.Unix()-last < 1800 {
		return
	}
	_ = c.St.SetSetting("pvemail-check", now.Unix())
	clusters, _ := c.Clusters()
	for _, cl := range clusters {
		if !c.PVEMailOn(cl.ID) {
			continue
		}
		go func(cl PVECluster) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			pve := c.PVEClient(cl)
			jobs, err := pve.BackupJobs(ctx)
			if err != nil {
				return
			}
			prev := map[string]jobMailPrev{}
			_ = c.St.GetSetting("pvemail-prev:"+cl.ID, &prev)
			changed := false
			for _, j := range jobs {
				if jobSendsMail(j) && quietJob(ctx, pve, j.ID) == nil {
					prev[j.ID] = jobMailPrev{Mode: j.NotificationMode, MailTo: j.MailTo}
					changed = true
				}
			}
			if changed {
				_ = c.St.SetSetting("pvemail-prev:"+cl.ID, prev)
			}
		}(cl)
	}
}
