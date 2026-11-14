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
