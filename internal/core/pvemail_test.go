package core_test

import (
	"context"
	"testing"

	"pk95proxbackup/internal/notify"
)

func TestPVEMailSwitch(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	st, err := c.PVEMailState(ctx, "cluster")
	if err != nil || st.Appliance || st.PVESends != 1 {
		t.Fatalf("start: %+v %v", st, err)
	}
	// Not without mail on the appliance: nobody would hear about backups any more.
	if _, err := c.SetPVEMail(ctx, "cluster", true); err == nil {
		t.Fatal("switch without appliance mail must fail")
	}
	ns := notify.Defaults()
	ns.MailEnabled, ns.MailTo, ns.SMTPHost = true, "it@example.org", "smtp.example.org"
	_ = c.St.SetSetting("notify", ns)
	st, err = c.SetPVEMail(ctx, "cluster", true)
	if err != nil || !st.Appliance || st.PVESends != 0 || st.Jobs[0].Mode != "legacy-sendmail" {
		t.Fatalf("on: %+v %v", st, err)
	}
	st, err = c.SetPVEMail(ctx, "cluster", false)
	if err != nil || st.Appliance || st.PVESends != 1 || st.Jobs[0].Mode != "notification-system" {
		t.Fatalf("back: %+v %v", st, err)
	}
}
