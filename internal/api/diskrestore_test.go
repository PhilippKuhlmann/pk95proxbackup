package api_test

import (
	"fmt"
	"testing"
	"time"

	"pk95proxbackup/internal/fake"
)

// A single disk over the API: the backup lists its disks, the job runs to the end.
func TestRestoreDiskAPI(t *testing.T) {
	old := fake.RestoreDuration
	fake.RestoreDuration = 200 * time.Millisecond
	t.Cleanup(func() { fake.RestoreDuration = old })
	admin := login(t)
	_, out := admin.do("GET", "/api/guests/snapshots?pbs=local&store=backup&type=vm&bid=101", nil, false)
	snaps := out["snapshots"].([]any)
	sn := snaps[0].(map[string]any)
	if files := sn["files"].([]any); len(files) < 4 {
		t.Fatalf("files: %v", files)
	}
	body := map[string]any{"cluster": "cluster", "pbs": "local", "store": "backup", "bid": 101, "time": sn["backup-time"], "drive": "scsi1", "target": 111, "storage": "local-lvm"}
	code, job := admin.do("POST", "/api/restore/disk", body, true)
	if code != 200 || job["phase"] != "restore" {
		t.Fatalf("start: %d %v", code, job)
	}
	var j map[string]any
	for i := 0; i < 300; i++ {
		_, j = admin.do("GET", "/api/restore/disk/"+fmt.Sprint(job["id"]), nil, false)
		if j["phase"] == "done" || j["phase"] == "error" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if j["phase"] != "done" || j["new_disk"] != "scsi1" {
		t.Fatalf("job: %v", j)
	}
	body["drive"] = "scsi9"
	if code, out := admin.do("POST", "/api/restore/disk", body, true); code != 422 || out["fields"] == nil {
		t.Fatalf("field error: %d %v", code, out)
	}
	if code, _ := admin.do("GET", "/api/restore/disk/unbekannt", nil, false); code != 404 {
		t.Fatalf("unknown job: %d", code)
	}
}
