package prox

import "testing"

func TestRawNumber(t *testing.T) {
	for in, want := range map[string]int64{"0": 0, "35 (Min/Max 20/45)": 35, "1,234": 1234, "8760h+12m": 8760, "3%": 3, "": -1, "n/a": -1} {
		if got := rawNumber(in); got != want {
			t.Errorf("rawNumber(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestSmartApplyNVMe(t *testing.T) {
	r := NewSmartReport()
	for _, line := range []string{"Percentage Used:                    7%", "Media and Data Integrity Errors:    2", "Power On Hours:                     1,234", "Temperature:                        41 Celsius"} {
		m := nvmeLine.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("no match: %q", line)
		}
		r.apply(0, m[1], m[2])
	}
	if r.Wearout != 93 || r.MediaErrors != 2 || r.PowerOnHours != 1234 || r.Temperature != 41 {
		t.Fatalf("%+v", r)
	}
	// ATA ids win over names.
	r = NewSmartReport()
	r.apply(5, "Reallocated_Sector_Ct", "12")
	r.apply(197, "Current_Pending_Sector", "0")
	if r.Reallocated != 12 || r.Pending != 0 || r.Uncorrectable != -1 {
		t.Fatalf("%+v", r)
	}
}
