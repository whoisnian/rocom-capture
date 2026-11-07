package store

import (
	"testing"

	"github.com/whoisnian/rocom-capture/internal/pet"
)

// TestSetHatchingEggs:孵蛋器占用列表一到就整体订正在孵标记,读取时以该列为准。
// 复刻 2026-08-26 pcap 的现场:5 颗蛋带 start_hatch_time,真在孵的只有 3 颗。
func TestSetHatchingEggs(t *testing.T) {
	st := newTestStore(t)
	sc := st.For(testAcc)

	eggs := []*pet.EggView{
		{Gid: 3259, StartHatch: 1787739077, HatchedSecs: 16456, HatchUpdate: 1787755508, Hatching: true},
		{Gid: 3262, StartHatch: 1787739170, HatchedSecs: 16363, HatchUpdate: 1787755508, Hatching: true},
		{Gid: 3264, StartHatch: 1787739176, HatchedSecs: 16357, HatchUpdate: 1787755508, Hatching: true},
		{Gid: 3250, StartHatch: 1787739086, Hatching: true}, // 取出过:标记曾判错
		{Gid: 3252, StartHatch: 1787739082, Hatching: true},
	}
	if err := sc.UpsertEggs(eggs, 1787755508); err != nil {
		t.Fatalf("入库: %v", err)
	}
	if err := sc.SetHatchingEggs([]uint32{3259, 3262, 3264}); err != nil {
		t.Fatalf("订正: %v", err)
	}

	got, err := sc.ListEggs(EggFilter{})
	if err != nil {
		t.Fatalf("读取: %v", err)
	}
	want := map[uint32]bool{3259: true, 3262: true, 3264: true, 3250: false, 3252: false}
	wantSlot := map[uint32]int{3259: 1, 3262: 2, 3264: 3} // egg_gid[] 的数组次序就是槽位
	if len(got) != len(want) {
		t.Fatalf("蛋数 = %d, want %d", len(got), len(want))
	}
	for _, e := range got {
		if e.Hatching != want[e.Gid] {
			t.Errorf("蛋 %d 在孵 = %v, want %v", e.Gid, e.Hatching, want[e.Gid])
		}
		if e.HatchSlot != wantSlot[e.Gid] {
			t.Errorf("蛋 %d 槽位 = %d, want %d", e.Gid, e.HatchSlot, wantSlot[e.Gid])
		}
	}

	// 孵蛋器清空:全部清 0,槽位一并清掉
	if err := sc.SetHatchingEggs(nil); err != nil {
		t.Fatalf("清空: %v", err)
	}
	got, _ = sc.ListEggs(EggFilter{})
	for _, e := range got {
		if e.Hatching {
			t.Errorf("蛋 %d 仍标为在孵", e.Gid)
		}
		if e.HatchSlot != 0 {
			t.Errorf("蛋 %d 仍带槽位 %d", e.Gid, e.HatchSlot)
		}
	}
}
