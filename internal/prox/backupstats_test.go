package prox

import "testing"

func TestBackupStats(t *testing.T) {
	head := lines(`starting new backup on datastore 'local' from ::ffff:10.10.250.10: "ct/250254/2026-11-19T11:16:17Z"`,
		"protocol upgrade done",
		`created new dynamic index 1 ("ct/250254/2026-11-19T11:16:17Z/catalog.pcat1.didx")`,
		`created new dynamic index 2 ("ct/250254/2026-11-19T11:16:17Z/root.pxar.didx")`)
	var st BackupStats
	if !ParseBackupHead(head, &st) || st.Type != "ct" || st.ID != "250254" || st.Time != 1795086977 || st.Indexes != 2 || st.NS != "" {
		t.Fatalf("head: %+v", st)
	}
	tail := lines("Upload statistics for 'root.pxar.didx'", "Size: 10372811503", "Chunk count: 2651", "Upload size: 164167965 (1%)",
		"successfully added chunk 1 to dynamic index 1", "Upload statistics for 'catalog.pcat1.didx'", "Size: 2474193", "Upload size: 2474193 (100%)", "TASK OK")
	ParseBackupTail(tail, &st)
	if st.Archives != 2 || st.Upload != 164167965+2474193 || st.Size != 10372811503+2474193 {
		t.Fatalf("tail: %+v", st)
	}
	// as the API delivers it: with the time in front of every line
	var api BackupStats
	ParseBackupHead(lines(`2026-11-19T12:16:18+01:00: starting new backup on datastore 'local' from ::ffff:10.10.250.10: "vm/250200/2026-11-19T11:16:13Z"`,
		`2026-11-19T12:16:18+01:00: created new fixed index 1 ("vm/250200/2026-11-19T11:16:13Z/drive-scsi0.img.fidx")`), &api)
	ParseBackupTail(lines("2026-11-19T12:16:40+01:00: Upload statistics for 'drive-scsi0.img.fidx'", "2026-11-19T12:16:40+01:00: Size: 713031680",
		"2026-11-19T12:16:40+01:00: Upload size: 700448768 (98%)"), &api)
	if api.Type != "vm" || api.Indexes != 1 || api.Archives != 1 || api.Upload != 700448768 {
		t.Fatalf("with time stamps: %+v", api)
	}
	var ns BackupStats
	ParseBackupHead(lines(`starting new backup on datastore 'b' from x: "ns/kunde/ns/a/vm/100/2026-11-19T11:16:17Z"`), &ns)
	if ns.NS != "kunde/a" || ns.Type != "vm" || ns.ID != "100" {
		t.Fatalf("namespace: %+v", ns)
	}
}
