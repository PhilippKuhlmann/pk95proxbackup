package core_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/fake"
)

// A USB disk that belongs to no datastore is listed and can be taken over as a disk of the
// set whose own disk is away; a stale mount of a pulled disk is released.
func TestForeignDisks(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	run := c.LocalRunner.(*fake.Runner)
	run.StaleMounts = []string{"/mnt/datastore/usb-b /dev/sdz1[/usb-b]", "/mnt/datastore/fremd /dev/sdy1"}
	c.ScanForeignForTest(ctx, time.Now())

	st := c.RotationStates(ctx)[0]
	var names []string
	for _, f := range st.Foreign {
		names = append(names, f.Name)
	}
	// sdd is new (blank), sde carries a filesystem of no datastore; sdb is no USB disk
	if strings.Join(names, ",") != "sdd,sde" || !st.Foreign[0].Blank || st.Foreign[1].UUID == "" {
		t.Fatalf("foreign: %+v", st.Foreign)
	}
	// only the stale mount of a rotation datastore is released
	if got := run.Unmounted(); !slices.Equal(got, []string{"/mnt/datastore/usb-b"}) {
		t.Fatalf("unmounted %v", got)
	}
	if !strings.Contains(eventTitles(t, c), "Verwaisten Mount von „usb-b“ gelöst") {
		t.Fatalf("not reported:\n%s", eventTitles(t, c))
	}

	r := st.Rotation
	a, b := r.Disks[0], r.Disks[1] // A is plugged in, B is away
	if err := c.CheckAdopt(ctx, r, a, "sde"); err == nil || !strings.Contains(err.Error(), "steckt gerade") {
		t.Fatalf("adopt for a plugged-in disk: %v", err)
	}
	if err := c.CheckAdopt(ctx, r, b, "sdb"); err == nil {
		t.Fatal("system disk taken over")
	}
	if err := c.AdoptDisk(ctx, r, b, "sde"); err != nil {
		t.Fatal(err)
	}
	c.ScanForeignForTest(ctx, time.Now())
	st = c.RotationStates(ctx)[0]
	if !st.Disks[1].Present || len(st.Foreign) != 1 || st.Foreign[0].Name != "sdd" {
		t.Fatalf("after adopt: disk %+v foreign %+v", st.Disks[1], st.Foreign)
	}
	if !strings.Contains(eventTitles(t, c), "Festplatte „Platte B (ungerade Tage)“ neu aufgesetzt") {
		t.Fatalf("not reported:\n%s", eventTitles(t, c))
	}
}
