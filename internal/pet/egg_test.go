package pet

import (
	"slices"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

// eggBagItem 拼一件带 egg_data 的 BagItem:gid(1)/id(2)/num(3)/update_time(4)/type(14)/egg_data(15)。
// 蛋不可堆叠,在包里就是 num=1(离包那条另见 eggChange)。
func eggBagItem(gid, id uint32, updated int32, brief []byte) []byte {
	return eggBagItemNum(gid, id, 1, updated, brief)
}

func eggBagItemNum(gid, id uint32, num uint64, updated int32, brief []byte) []byte {
	b := protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), uint64(gid))
	b = protowire.AppendVarint(protowire.AppendTag(b, 2, protowire.VarintType), uint64(id))
	if num > 0 {
		b = protowire.AppendVarint(protowire.AppendTag(b, 3, protowire.VarintType), num)
	}
	b = protowire.AppendVarint(protowire.AppendTag(b, 4, protowire.VarintType), uint64(uint32(updated)))
	b = protowire.AppendVarint(protowire.AppendTag(b, 14, protowire.VarintType), EggItemType)
	return protowire.AppendBytes(protowire.AppendTag(b, 15, protowire.BytesType), brief)
}

// eggChangeBody 把几条 GoodsChangeItem 包成一条带 ret_info 的消息:
// ret_info(1).goods_change_info(4).changes(1){op(2), num(3), bag_item(4)}。
func eggChangeBody(items ...[]byte) []byte {
	var chg []byte
	for _, it := range items {
		chg = protowire.AppendBytes(protowire.AppendTag(chg, 1, protowire.BytesType), it)
	}
	ret := protowire.AppendBytes(protowire.AppendTag(nil, 4, protowire.BytesType), chg)
	return protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), ret)
}

func eggChange(op, num uint64, item []byte) []byte {
	b := protowire.AppendVarint(protowire.AppendTag(nil, 2, protowire.VarintType), op)
	if num > 0 {
		b = protowire.AppendVarint(protowire.AppendTag(b, 3, protowire.VarintType), num)
	}
	return protowire.AppendBytes(protowire.AppendTag(b, 4, protowire.BytesType), item)
}

// eggBrief 拼 PetEggBrief:conf_id(1)/height(2)/weight(3)/hatched(4)/update(5)/max(6)/start(9)/src(10)。
func eggBrief(conf uint32, h, w, hatched, update, max, start, src int32) []byte {
	add := func(b []byte, n protowire.Number, v uint64) []byte {
		return protowire.AppendVarint(protowire.AppendTag(b, n, protowire.VarintType), v)
	}
	b := add(nil, 1, uint64(conf))
	b = add(b, 2, uint64(uint32(h)))
	b = add(b, 3, uint64(uint32(w)))
	b = add(b, 4, uint64(uint32(hatched)))
	b = add(b, 5, uint64(uint32(update)))
	b = add(b, 6, uint64(uint32(max)))
	b = add(b, 9, uint64(uint32(start)))
	return add(b, 10, uint64(uint32(src)))
}

func TestParseChangedEggs(t *testing.T) {
	item := eggBagItem(3093, 107028, 1786770791, eggBrief(3062001, 59, 21957, 0, 0, 57600, 0, 6))
	body := eggChangeBody(eggChange(OpTypeSet, 1, item))

	eggs, gone := ParseChangedEggs(body)
	if len(eggs) != 1 || len(gone) != 0 {
		t.Fatalf("蛋数 = %d, 离包 = %d, want 1/0", len(eggs), len(gone))
	}
	e := eggs[0]
	if e.Gid != 3093 || e.ItemID != 107028 || e.ConfID != 3062001 ||
		e.Height != 59 || e.Weight != 21957 || e.MaxSec != 57600 || e.Src != 6 {
		t.Errorf("蛋 = %+v", e)
	}
	if e.UpdateTime != 1786770791 {
		t.Errorf("获得时间 = %d", e.UpdateTime)
	}
	if e.Hatching() {
		t.Error("start_hatch_time=0 不该算在孵")
	}
	// 非蛋物品(无 egg_data)不该混进来
	plain := protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), 5)
	if got, _ := ParseChangedEggs(plain); len(got) != 0 {
		t.Errorf("非蛋消息解出了 %d 颗", len(got))
	}
}

// 送人(2026-09-10 pcap):同一份 bag_item 带着 op=OT_SET、两处 num 归零发下来,
// 认成离包而不是一次普通更新,否则送出去的蛋会一直留在精灵蛋页上。
func TestParseChangedEggsRemoved(t *testing.T) {
	brief := eggBrief(3736001, 24, 11667, 0, 0, 72000, 0, 3)
	body := eggChangeBody(
		eggChange(OpTypeSet, 0, eggBagItemNum(3498, 107226, 0, 1789043877, brief)),
		eggChange(OpTypeSet, 1, eggBagItem(3499, 107226, 1789043877, brief)),
	)
	eggs, gone := ParseChangedEggs(body)
	if len(gone) != 1 || gone[0] != 3498 {
		t.Errorf("离包 = %v, want [3498]", gone)
	}
	if len(eggs) != 1 || eggs[0].Gid != 3499 {
		t.Errorf("在包 = %+v, want gid 3499", eggs)
	}

	// 数量没归零的(入孵/取出/进度更新)照旧是更新;op 不是 OT_SET 时也不认离包。
	keep := eggChangeBody(eggChange(0, 0, eggBagItemNum(3500, 107226, 0, 1789043877, brief)))
	if eggs, gone := ParseChangedEggs(keep); len(gone) != 0 || len(eggs) != 1 {
		t.Errorf("op=OT_ADD 时 = %d 在包 / %d 离包, want 1/0", len(eggs), len(gone))
	}
}

func TestParseBagEggsPaging(t *testing.T) {
	// bag_info(4).item_list(3){type(1), items(2)};另放一组非蛋类型确认被整组跳过。
	eggList := protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), EggItemType)
	eggList = protowire.AppendBytes(protowire.AppendTag(eggList, 2, protowire.BytesType),
		eggBagItem(3017, 310049, 1786408271, eggBrief(0, 22, 1709, 20060, 1786738056, 43200, 1786736669, 0)))
	other := protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), 3)
	other = protowire.AppendBytes(protowire.AppendTag(other, 2, protowire.BytesType),
		eggBagItem(1, 2, 3, eggBrief(1, 1, 1, 0, 0, 0, 0, 0)))

	bag := protowire.AppendBytes(protowire.AppendTag(nil, 3, protowire.BytesType), eggList)
	bag = protowire.AppendBytes(protowire.AppendTag(bag, 3, protowire.BytesType), other)
	body := protowire.AppendVarint(protowire.AppendTag(nil, 2, protowire.VarintType), 3) // total_page
	body = protowire.AppendVarint(protowire.AppendTag(body, 3, protowire.VarintType), 2) // req_page
	body = protowire.AppendBytes(protowire.AppendTag(body, 4, protowire.BytesType), bag)

	eggs, page, total := ParseBagEggs(body)
	if page != 2 || total != 3 {
		t.Errorf("分页 = %d/%d, want 2/3", page, total)
	}
	if len(eggs) != 1 || eggs[0].Gid != 3017 {
		t.Fatalf("蛋 = %+v", eggs)
	}
	if !eggs[0].Hatching() || eggs[0].HatchedSec != 20060 {
		t.Errorf("孵化状态 = %+v", eggs[0])
	}
}

func TestParseCrackEgg(t *testing.T) {
	// c2s 破壳请求:6 字节子头 + egg_gid(1) + select_ball_gid(2)
	req := append([]byte{0xc0, 0x50, 0, 0, 0, 0x21},
		protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), 3083)...)
	req = protowire.AppendVarint(protowire.AppendTag(req, 2, protowire.VarintType), 2092)
	if got := ParseCrackEggReq(req); got != 3083 {
		t.Errorf("egg_gid = %d, want 3083", got)
	}
	rsp := protowire.AppendVarint(protowire.AppendTag(nil, 2, protowire.VarintType), 39322)
	if got := ParseCrackEggRsp(rsp); got != 39322 {
		t.Errorf("hatched_pet_gid = %d, want 39322", got)
	}
}

func TestParseFlowReason(t *testing.T) {
	body := protowire.AppendVarint(protowire.AppendTag(nil, 3, protowire.VarintType), FlowReasonHomeLay)
	if got := ParseFlowReason(body); got != FlowReasonHomeLay {
		t.Errorf("flow_reason = %d, want %d", got, FlowReasonHomeLay)
	}
}

func TestSortEggs(t *testing.T) {
	// 复刻游戏内「品质排序」的键:品类升 → 品质降 → 物品排序号升 → 获得时间降。
	mk := func(name string, order, quality, sortID int32, at int64) *EggView {
		return &EggView{Name: name, TypeOrder: order, Quality: quality, SortID: sortID, ObtainedAt: at}
	}
	eggs := []*EggView{
		mk("普通-新", 100000, 4, 650100, 300),
		mk("异色-小号", 104, 5, 630082, 100),
		mk("普通-旧", 100000, 4, 650100, 200),
		mk("异色-大号", 104, 5, 630105, 500),
		mk("唯一", 101, 5, 640001, 50),
	}
	SortEggs(eggs, "quality", false)
	var got []string
	for _, e := range eggs {
		got = append(got, e.Name)
	}
	want := []string{"唯一", "异色-小号", "异色-大号", "普通-新", "普通-旧"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("品质排序 = %v, want %v", got, want)
		}
	}
	// 获取时间:只看 update_time 降;asc 即游戏里那个反向开关。
	SortEggs(eggs, "obtained", false)
	if eggs[0].Name != "异色-大号" || eggs[len(eggs)-1].Name != "唯一" {
		t.Errorf("时间排序 = %s … %s", eggs[0].Name, eggs[len(eggs)-1].Name)
	}
	SortEggs(eggs, "obtained", true)
	if eggs[0].Name != "唯一" {
		t.Errorf("反向时间排序 = %s", eggs[0].Name)
	}
}

func TestSortHatchingEggs(t *testing.T) {
	// 槽位顺序就是 egg_gid[] 的数组次序:同秒入孵的几颗也有固定先后,start_hatch_time 排不出来。
	// 取自 2026-11-08 pcap 的现场:4057/4056 同秒、4171/4175/4174 同秒,游戏内仍是列表次序。
	gids := []uint32{4057, 4056, 3990, 4171, 4175, 4174, 4179, 3999, 4181}
	start := map[uint32]int64{
		4057: 1793941050, 4056: 1793941050, 3990: 1793941255,
		4171: 1794035423, 4175: 1794035423, 4174: 1794035423,
		4179: 1794081674, 3999: 1794081686, 4181: 1794081716,
	}
	eggs := make([]*EggView, 0, len(gids))
	for i, gid := range gids {
		eggs = append(eggs, &EggView{Gid: gid, HatchSlot: i + 1, StartHatch: start[gid]})
	}
	slices.Reverse(eggs) // 页面从库里按背包次序取,传入次序与槽位无关
	SortHatchingEggs(eggs)
	for i, gid := range gids {
		if eggs[i].Gid != gid {
			got := make([]uint32, len(eggs))
			for j, e := range eggs {
				got[j] = e.Gid
			}
			t.Fatalf("槽位顺序 = %v, want %v", got, gids)
		}
	}
}

func TestSortHatchingEggsFallback(t *testing.T) {
	// 权威快照还没到(抓包从半途开始,没有登录数据也没开过孵蛋器)时,退回按入孵时刻升序。
	eggs := []*EggView{
		{Gid: 3104, StartHatch: 1786858404},
		{Gid: 3109, StartHatch: 1786858369},
		{Gid: 3110, StartHatch: 1786855793},
	}
	SortHatchingEggs(eggs)
	want := []uint32{3110, 3109, 3104}
	for i := range want {
		if eggs[i].Gid != want[i] {
			t.Fatalf("兜底顺序 = %d,%d,%d, want %v", eggs[0].Gid, eggs[1].Gid, eggs[2].Gid, want)
		}
	}
}

func TestSortEggsTie(t *testing.T) {
	// 同一时刻入包的两颗同种蛋:所有键都分不出高低,谁在前由排序算法决定——我们复刻的是
	// 客户端那套 Lua table.sort(见 luasort.go 与其对拍测试)。只有两颗时它不会动相等项,
	// 故正反两个方向都应保持传入的背包原始次序(元素更多时会像游戏里那样出现换位)。
	mk := func(gid uint32) *EggView {
		return &EggView{Gid: gid, Name: "神奇的蛋", TypeOrder: 100000, Quality: 4, SortID: 606100, ObtainedAt: 100}
	}
	for _, by := range []string{"quality", "obtained"} {
		for _, asc := range []bool{false, true} {
			eggs := []*EggView{mk(2994), mk(2996)} // 背包次序:2994 在前
			SortEggs(eggs, by, asc)
			if eggs[0].Gid != 2994 || eggs[1].Gid != 2996 {
				t.Errorf("%s asc=%v = %d,%d,应保持背包次序 2994,2996", by, asc, eggs[0].Gid, eggs[1].Gid)
			}
		}
	}
}

// TestHatchingNeedsProgress:取出孵蛋器的蛋 start_hatch_time 不清零,只清进度
// (2026-08-26 pcap:背包 5 颗带 start_hatch_time,登录数据的 egg_gid 只有 3 颗)。
func TestHatchingNeedsProgress(t *testing.T) {
	cases := []struct {
		name string
		e    Egg
		want bool
	}{
		{"在孵", Egg{StartHatch: 1787739077, HatchedSec: 16456, HatchUpdate: 1787755508}, true},
		{"刚满 0 秒但服务器已在计", Egg{StartHatch: 1787739077, HatchUpdate: 1787739077}, true},
		{"取出后(进度清零、入孵时刻残留)", Egg{StartHatch: 1787739086}, false},
		{"从没进过孵蛋器", Egg{}, false},
	}
	for _, c := range cases {
		if got := c.e.Hatching(); got != c.want {
			t.Errorf("%s: Hatching() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestHatchSlots(t *testing.T) {
	// ret_info(1){result(1)=0} + egg_gid(2) 逐个下发
	ret := protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), 0)
	body := protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), ret)
	for _, g := range []uint64{3259, 3262, 3264} {
		body = protowire.AppendVarint(protowire.AppendTag(body, 2, protowire.VarintType), g)
	}
	body = protowire.AppendVarint(protowire.AppendTag(body, 3, protowire.VarintType), 16456)
	gids, ok := HatchSlots(body)
	if !ok || len(gids) != 3 || gids[0] != 3259 || gids[2] != 3264 {
		t.Fatalf("egg_gid = %v (ok=%v)", gids, ok)
	}

	// packed 形式同样认;孵蛋器空着(只有 ret_info)也是一份有效快照
	packed := protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), ret)
	packed = protowire.AppendBytes(protowire.AppendTag(packed, 2, protowire.BytesType),
		protowire.AppendVarint(protowire.AppendVarint(nil, 3259), 3262))
	if gids, ok := HatchSlots(packed); !ok || len(gids) != 2 || gids[1] != 3262 {
		t.Errorf("packed egg_gid = %v (ok=%v)", gids, ok)
	}
	empty := protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), ret)
	if gids, ok := HatchSlots(empty); !ok || len(gids) != 0 {
		t.Errorf("空孵蛋器 = %v (ok=%v), want 空快照且 ok", gids, ok)
	}

	// 失败回包(result!=0)不能拿来订正
	bad := protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType),
		protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), 1))
	if _, ok := HatchSlots(bad); ok {
		t.Error("result!=0 不该判为有效快照")
	}
}

func TestBackpackHatchSlots(t *testing.T) {
	// 登录数据里的 PetBackpackInfo:egg_gid(1) + boxes(3){box_id(1), pet_gid(3)}
	box := protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), 1)
	for _, g := range []uint64{6476, 12335, 11291, 18471, 266, 1503} {
		box = protowire.AppendVarint(protowire.AppendTag(box, 3, protowire.VarintType), g)
	}
	bp := protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), 3259)
	bp = protowire.AppendVarint(protowire.AppendTag(bp, 1, protowire.VarintType), 3262)
	bp = protowire.AppendVarint(protowire.AppendTag(bp, 1, protowire.VarintType), 3264)
	bp = protowire.AppendBytes(protowire.AppendTag(bp, 3, protowire.BytesType), box)
	// 实际藏在 player_info(2).pet_info(4).backpack_info(9) 里,故套两层再解
	pi := protowire.AppendBytes(protowire.AppendTag(nil, 9, protowire.BytesType), bp)
	body := protowire.AppendBytes(protowire.AppendTag(nil, 2, protowire.BytesType),
		protowire.AppendBytes(protowire.AppendTag(nil, 4, protowire.BytesType), pi))

	gids, ok := BackpackHatchSlots(body)
	if !ok || len(gids) != 3 || gids[0] != 3259 || gids[2] != 3264 {
		t.Fatalf("egg_gid = %v (ok=%v)", gids, ok)
	}
	// 没有背包的消息给不出快照(不能当成空孵蛋器去清标记)
	if _, ok := BackpackHatchSlots(protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), 7)); ok {
		t.Error("无 PetBackpackInfo 不该判为有效快照")
	}
}

func TestParentNatures(t *testing.T) {
	mom := func(n string, academy bool) *EggParent { return &EggParent{Nature: n, Academy: academy} }
	cases := []struct {
		name string
		p    *EggParents
		want []string
		sure bool
	}{
		{"非家园蛋", nil, nil, false},
		{"父本住学院小窝:必定随父", &EggParents{Mother: mom("固执", false),
			Fathers: []EggParent{{Nature: "胆小", Academy: true}}}, []string{"胆小"}, true},
		{"母本住学院小窝:必定随母", &EggParents{Mother: mom("固执", true),
			Fathers: []EggParent{{Nature: "胆小"}}}, []string{"固执"}, true},
		{"双亲同性格", &EggParents{Mother: mom("固执", false),
			Fathers: []EggParent{{Nature: "固执"}}}, []string{"固执"}, false},
		{"双亲不同性格:母本在前", &EggParents{Mother: mom("固执", false),
			Fathers: []EggParent{{Nature: "胆小"}}}, []string{"固执", "胆小"}, false},
		{"串窝:候选父本都列", &EggParents{Mother: mom("固执", false), Ambiguous: true,
			Fathers: []EggParent{{Nature: "胆小"}, {Nature: "急躁"}}}, []string{"固执", "胆小", "急躁"}, false},
		{"父本未知:只有母本", &EggParents{Mother: mom("固执", false)}, []string{"固执"}, false},
		{"学院那只没记下性格:退回概率口径", &EggParents{Mother: mom("固执", false),
			Fathers: []EggParent{{Academy: true}}}, []string{"固执"}, false},
	}
	for _, c := range cases {
		got, sure := parentNatures(c.p)
		if !slices.Equal(got, c.want) || sure != c.sure {
			t.Errorf("%s: parentNatures() = %v,%v, want %v,%v", c.name, got, sure, c.want, c.sure)
		}
	}
}
