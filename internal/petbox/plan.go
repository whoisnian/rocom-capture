package petbox

import "fmt"

// Plan 把「这些格位上的宠物应当排成 want 这个顺序」拆成一串两两交换。
//
// slots 是当前**已占用**的格位(带着各自当前的 Gid),须已按盒号/格号升序;want 必须是
// slots 里全部 gid 的一个排列。只在已占用格位之间重排是刻意的:每一步都是「宠物 ↔ 宠物」,
// 与抓到的真包语义完全一致,不依赖「拖进空格」(tar_info.pet_gid=0)这条尚未验证的行为。
//
// 返回的交换次数 = n - 置换环数,是达成目标的最少次数(每个长度 k 的环要 k-1 次)。
func Plan(slots []Slot, want []uint32) ([]Swap, error) {
	if len(slots) != len(want) {
		return nil, fmt.Errorf("petbox: 格位数 %d 与目标顺序长度 %d 不等", len(slots), len(want))
	}
	cur := make([]uint32, len(slots))
	at := make(map[uint32]int, len(slots)) // gid → 它当前在第几个格位
	for i, s := range slots {
		if s.Gid == 0 {
			return nil, fmt.Errorf("petbox: 第 %d 个格位是空的,只支持在已占用格位之间重排", i)
		}
		if _, dup := at[s.Gid]; dup {
			return nil, fmt.Errorf("petbox: gid %d 出现在多个格位", s.Gid)
		}
		cur[i], at[s.Gid] = s.Gid, i
	}
	for _, g := range want {
		if _, ok := at[g]; !ok {
			return nil, fmt.Errorf("petbox: 目标顺序里的 gid %d 不在这批格位上", g)
		}
	}

	var out []Swap
	for i := range cur {
		if cur[i] == want[i] {
			continue
		}
		j := at[want[i]]
		from, to := slots[i], slots[j]
		from.Gid, to.Gid = cur[i], cur[j]
		out = append(out, Swap{From: from, To: to})
		at[cur[i]], at[cur[j]] = j, i
		cur[i], cur[j] = cur[j], cur[i]
	}
	return out, nil
}
