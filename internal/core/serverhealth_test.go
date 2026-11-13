package core_test

import (
	"context"
	"strings"
	"testing"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/prox"
)

// As PBS01 answered on 13.11.2026: 4.2.6 installed and running, 4.2.10 available.
func TestServerHealthAvailableNotInstalled(t *testing.T) {
	versions := []prox.AptPackage{
		{Package: "proxmox-backup", Version: "4.2.0", OldVersion: "4.2.0", ExtraInfo: "running kernel: 7.0.14-17-pve"},
		{Package: "proxmox-backup-server", Version: "4.2.10-1", OldVersion: "4.2.6-1", ExtraInfo: "running version: 4.2.6"},
		{Package: "proxmox-kernel-7.0.14-17-pve-signed", Version: "7.0.14-17", OldVersion: "7.0.14-17"},
		{Package: "proxmox-kernel-7.0", Version: "7.0.14-26", OldVersion: "7.0.14-17"},
		{Package: "proxmox-kernel-6.14.11-9-pve-signed", Version: "6.14.11-9", OldVersion: "6.14.11-9"},
	}
	updates := []prox.AptPackage{{Package: "tzdata", OldVersion: "a", Version: "b"}, {Package: "proxmox-backup-server", OldVersion: "4.2.6-1", Version: "4.2.10-1"}}
	h := core.EvaluateServerHealth(versions, updates, "7.0.14-17-pve")
	if h.RestartNeeded || h.RebootNeeded {
		t.Fatalf("nothing to restart: %+v", h)
	}
	if h.Running != "4.2.6" || h.Installed != "4.2.6-1" || h.Available != "4.2.10-1" || h.Updates != 2 {
		t.Fatalf("%+v", h)
	}
	if !strings.HasPrefix(h.UpdateList[0], "proxmox-backup-server") {
		t.Fatalf("PBS update first: %v", h.UpdateList)
	}
}

func TestServerHealthRestartAndReboot(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	h, err := c.CheckServerHealth(ctx, "buero") // upgraded to 4.2.10, still running 4.2.6, new kernel
	if err != nil {
		t.Fatal(err)
	}
	if !h.RestartNeeded || !h.RebootNeeded || h.NewestKernel != "7.0.14-26-pve" {
		t.Fatalf("%+v", h)
	}
	titles := eventTitles(t, c)
	if !strings.Contains(titles, "Dienste laufen noch mit der alten Version") || !strings.Contains(titles, "Neustart nötig") {
		t.Fatalf("not reported:\n%s", titles)
	}
	h, err = c.ReloadServerServices(ctx, "buero")
	if err != nil {
		t.Fatal(err)
	}
	if h.RestartNeeded || h.Running != "4.2.10" || !h.RebootNeeded {
		t.Fatalf("after reload: %+v", h)
	}
	if l, _ := c.CheckServerHealth(ctx, "local"); l.RestartNeeded || l.RebootNeeded || l.Updates != 2 {
		t.Fatalf("local: %+v", l)
	}
}
