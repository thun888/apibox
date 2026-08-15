package starhistory

import "testing"

func TestAggregateDates(t *testing.T) {
	got := aggregateDates([]string{"2025-03-01", "2025-01-01", "2025-02-01", "2025-03-01"})
	want := []starRecord{
		{Date: "2025-01-01", Count: 1},
		{Date: "2025-02-01", Count: 2},
		{Date: "2025-03-01", Count: 4},
	}
	if len(got) != len(want) {
		t.Fatalf("len: got %d want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("record %d: got %v want %v", i, got[i], want[i])
		}
	}
	if aggregateDates(nil) != nil {
		t.Error("expect nil for empty input")
	}
}

func TestMergeTailRecords(t *testing.T) {
	old := []starRecord{
		{Date: "2025-01-01", Count: 10},
		{Date: "2025-02-01", Count: 15},
	}
	prevCount := 15

	// 正常合并：旧日期（含与 lastDate 相同的）被过滤，新日期继续累计
	got := mergeTailRecords(old, []string{
		"2025-01-01", "2025-02-01", // 旧条目（边界页重叠）→ 丢弃
		"2025-03-01", "2025-03-01", // 同天两个 → 累计 +2
		"2025-04-01",
	}, prevCount)
	want := []starRecord{
		{Date: "2025-01-01", Count: 10},
		{Date: "2025-02-01", Count: 15},
		{Date: "2025-03-01", Count: 17},
		{Date: "2025-04-01", Count: 18},
	}
	if len(got) != len(want) {
		t.Fatalf("len: got %d want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("record %d: got %v want %v", i, got[i], want[i])
		}
	}

	// 无新条目（全是旧日期）→ nil
	if got := mergeTailRecords(old, []string{"2025-02-01", "2025-01-01"}, prevCount); got != nil {
		t.Errorf("expect nil, got %v", got)
	}

	// 空旧记录 → nil
	if got := mergeTailRecords(nil, []string{"2025-01-01"}, 0); got != nil {
		t.Errorf("expect nil, got %v", got)
	}
}
