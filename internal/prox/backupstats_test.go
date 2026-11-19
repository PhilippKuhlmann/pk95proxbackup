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
	var ns BackupStats
	ParseBackupHead(lines(`starting new backup on datastore 'b' from x: "ns/kunde/ns/a/vm/100/2026-11-19T11:16:17Z"`), &ns)
	if ns.NS != "kunde/a" || ns.Type != "vm" || ns.ID != "100" {
		t.Fatalf("namespace: %+v", ns)
	}
}
