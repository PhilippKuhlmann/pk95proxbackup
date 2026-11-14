package api_test

import (
	"strings"
	"testing"
)

func TestS3Datastore(t *testing.T) {
	cl := login(t)
	code, out := cl.do("GET", "/api/pbs/local/s3", nil, false)
	if code != 200 || len(out["providers"].([]any)) < 5 || len(out["endpoints"].([]any)) != 0 {
		t.Fatalf("list: %d %v", code, out)
	}
	// wrong keys: refused, nothing left behind
	code, out = cl.do("POST", "/api/pbs/local/s3", map[string]any{"provider": "hetzner", "region": "fsn1", "access_key": "AKDEMO", "secret_key": "falsch"}, true)
	if f, _ := out["fields"].(map[string]any); code != 422 || !strings.Contains(f["secret_key"].(string), "stimmt nicht") {
		t.Fatalf("wrong keys: %d %v", code, out)
	}
	if _, out = cl.do("GET", "/api/pbs/local/s3", nil, false); len(out["endpoints"].([]any)) != 0 {
		t.Fatalf("endpoint with wrong keys kept: %v", out)
	}
	// R2 needs the account ID
	if code, out = cl.do("POST", "/api/pbs/local/s3", map[string]any{"provider": "r2", "access_key": "a", "secret_key": "b"}, true); code != 422 {
		t.Fatalf("r2 without account: %d %v", code, out)
	}
	code, out = cl.do("POST", "/api/pbs/local/s3", map[string]any{"provider": "hetzner", "region": "fsn1", "access_key": " AK DEMO ", "secret_key": "geheim"}, true)
	if code != 200 || out["id"] != "hetzner-fsn1" || len(out["buckets"].([]any)) != 2 || out["list_error"] != "" {
		t.Fatalf("create: %d %v", code, out)
	}
	_, out = cl.do("GET", "/api/pbs/local/s3", nil, false)
	ep := out["endpoints"].([]any)[0].(map[string]any)
	if ep["endpoint"] != "{{bucket}}.{{region}}.your-objectstorage.com" || ep["provider"] != "Hetzner Object Storage" || ep["access-key"] != nil {
		t.Fatalf("endpoint (no key in the answer): %v", ep)
	}
	// a key for one bucket may not list buckets: kept, bucket typed in
	code, out = cl.do("POST", "/api/pbs/local/s3", map[string]any{"provider": "hetzner", "region": "fsn1", "access_key": "nurbucket", "secret_key": "geheim"}, true)
	if code != 200 || out["id"] != "hetzner-fsn1-2" || out["list_error"] == "" {
		t.Fatalf("key without list right: %d %v", code, out)
	}
	if code, out = cl.do("POST", "/api/pbs/local/s3/hetzner-fsn1/check", map[string]string{"bucket": "gibtsnicht", "store": "cloud"}, true); code != 422 {
		t.Fatalf("missing bucket: %d %v", code, out)
	}
	if code, out = cl.do("POST", "/api/pbs/local/s3/hetzner-fsn1/check", map[string]string{"bucket": "pbs-backup", "store": "cloud"}, true); code != 200 {
		t.Fatalf("check: %d %v", code, out)
	}
	ds := map[string]any{"name": "cloud", "kind": "s3", "s3": "hetzner-fsn1", "bucket": "pbs-backup", "path": "/mnt/datastore/cloud-cache",
		"keep_daily": 7, "keep_weekly": 4, "keep_monthly": 6, "prune_schedule": "daily", "gc_schedule": "daily"}
	if code, out = cl.do("POST", "/api/pbs/local/datastores", ds, true); code != 200 {
		t.Fatalf("create s3 datastore: %d %v", code, out)
	}
	_, out = cl.do("GET", "/api/pbs/local/datastores", nil, false)
	found := false
	for _, d := range out["datastores"].([]any) {
		m := d.(map[string]any)
		if m["name"] == "cloud" {
			found = m["backend"] == "type=s3,client=hetzner-fsn1,bucket=pbs-backup" && m["prune"] != nil
		}
	}
	if !found {
		t.Fatalf("s3 datastore: %v", out["datastores"])
	}
	// copy into the cloud every night
	if code, out = cl.do("POST", "/api/pbs/local/sync", map[string]any{"store": "cloud", "source_server": "local", "source_store": "backup", "schedule": "03:30", "transfer_last": 7}, true); code != 200 {
		t.Fatalf("copy job: %d %v", code, out)
	}
	if code, out = cl.do("DELETE", "/api/pbs/local/s3/hetzner-fsn1", nil, true); code != 409 {
		t.Fatalf("delete used endpoint: %d %v", code, out)
	}
	if code, out = cl.do("DELETE", "/api/pbs/local/s3/hetzner-fsn1-2", nil, true); code != 200 {
		t.Fatalf("delete: %d %v", code, out)
	}
}

func TestBandwidth(t *testing.T) {
	cl := login(t)
	// Daytime limit for everything
	code, out := cl.do("POST", "/api/pbs/local/traffic", map[string]any{"days": "mon..fri", "from": "07:00", "to": "19:00", "rate_in": 20, "rate_out": 10, "comment": "Tagsüber"}, true)
	name, _ := out["name"].(string)
	if code != 200 || name == "" {
		t.Fatalf("create rule: %d %v", code, out)
	}
	_, out = cl.do("GET", "/api/pbs/local/traffic", nil, false)
	r := out["rules"].([]any)[0].(map[string]any)
	if r["rate-in"] != "20MB" || r["rate-out"] != "10MB" || r["timeframe"].([]any)[0] != "mon..fri 07:00-19:00" || len(r["network"].([]any)) != 2 {
		t.Fatalf("rule: %v", r)
	}
	// only for the office, always, only sending
	if code, out = cl.do("PUT", "/api/pbs/local/traffic/"+name, map[string]any{"days": "always", "networks": "10.20.0.5", "rate_out": 5}, true); code != 200 {
		t.Fatalf("update: %d %v", code, out)
	}
	_, out = cl.do("GET", "/api/pbs/local/traffic", nil, false)
	r = out["rules"].([]any)[0].(map[string]any)
	if r["rate-in"] != nil || r["timeframe"] != nil || r["network"].([]any)[0] != "10.20.0.5/32" {
		t.Fatalf("updated rule: %v", r)
	}
	for _, bad := range []map[string]any{{"days": "always"}, {"days": "mon..fri", "from": "7", "to": "19:00", "rate_in": 5}, {"days": "always", "networks": "büro", "rate_in": 5}} {
		if code, _ := cl.do("POST", "/api/pbs/local/traffic", bad, true); code != 422 {
			t.Errorf("accepted: %v", bad)
		}
	}
	if code, _ = cl.do("DELETE", "/api/pbs/local/traffic/"+name, nil, true); code != 200 {
		t.Fatalf("delete: %d", code)
	}

	// copy job with limit and window
	code, out = cl.do("POST", "/api/pbs/local/sync", map[string]any{"store": "nas-backup", "source_server": "local", "source_store": "backup", "schedule": "22:00", "rate_in": 30, "window_end": "06:30"}, true)
	if code != 200 {
		t.Fatalf("copy job: %d %v", code, out)
	}
	_, out = cl.do("GET", "/api/pbs/local/sync", nil, false)
	var job map[string]any
	for _, j := range out["jobs"].([]any) {
		if m := j.(map[string]any); m["schedule"] == "22:00" {
			job = m
		}
	}
	if job == nil || job["rate-in"] != "30MB" || out["windows"].(map[string]any)[job["id"].(string)] != "06:30" {
		t.Fatalf("job %v, windows %v", job, out["windows"])
	}
	if code, out = cl.do("POST", "/api/pbs/local/sync", map[string]any{"store": "nas-backup", "source_server": "local", "source_store": "backup", "window_end": "6 Uhr"}, true); code != 422 {
		t.Fatalf("bad window: %d %v", code, out)
	}
	if code, _ = cl.do("DELETE", "/api/pbs/local/sync/"+job["id"].(string), nil, true); code != 200 {
		t.Fatal("delete job")
	}
	if _, out = cl.do("GET", "/api/pbs/local/sync", nil, false); len(out["windows"].(map[string]any)) != 0 {
		t.Fatalf("window of a deleted job kept: %v", out["windows"])
	}

	// upload limit of a cloud target
	cl.do("POST", "/api/pbs/local/s3", map[string]any{"provider": "wasabi", "region": "eu-central-1", "access_key": "AK", "secret_key": "s"}, true)
	if code, out = cl.do("PUT", "/api/pbs/local/s3/wasabi-eu-central-1/limit", map[string]any{"rate_out": 8}, true); code != 200 {
		t.Fatalf("s3 limit: %d %v", code, out)
	}
	_, out = cl.do("GET", "/api/pbs/local/s3", nil, false)
	if ep := out["endpoints"].([]any)[0].(map[string]any); ep["rate-out"] != "8MB" {
		t.Fatalf("s3 limit: %v", ep)
	}
}
