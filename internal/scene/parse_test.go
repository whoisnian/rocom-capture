package scene

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

// pos 构造一个 Position 子消息(field 1-3 = x/y/z)。
func pos(x, y, z int32) []byte {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(uint32(x)))
	b = protowire.AppendTag(b, 2, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(uint32(y)))
	b = protowire.AppendTag(b, 3, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(uint32(z)))
	return b
}

func TestParseMoveReq(t *testing.T) {
	// 合成 ZoneSceneMoveReq: field1 time_stamp, field2 to_pos, field3 to_rot(干扰),
	// field8 stop_move, field17 scene_cfg_id。
	var body []byte
	body = protowire.AppendTag(body, 1, protowire.VarintType)
	body = protowire.AppendVarint(body, 1700000000)
	body = protowire.AppendTag(body, 2, protowire.BytesType)
	body = protowire.AppendBytes(body, pos(387886, 608084, 5298))
	body = protowire.AppendTag(body, 3, protowire.BytesType) // to_rot,也是 Position 形状
	body = protowire.AppendBytes(body, pos(0, 0, 1800))
	body = protowire.AppendTag(body, 4, protowire.BytesType) // speed(速度向量),分量可为负
	body = protowire.AppendBytes(body, pos(211, -352, 0))
	body = protowire.AppendTag(body, 8, protowire.VarintType)
	body = protowire.AppendVarint(body, 1)
	body = protowire.AppendTag(body, 17, protowire.VarintType)
	body = protowire.AppendVarint(body, 103)

	// 前置一段假 c2s 子头 + 后附 tsf4g 尾,模拟真实 AppBody。
	appBody := append([]byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x11, 0x22, 0x33}, body...)
	appBody = append(appBody, []byte("tsf4g\x00\x01\x02")...)

	mr, ok := ParseMoveReq(appBody)
	if !ok {
		t.Fatal("ParseMoveReq 失败")
	}
	if mr.Pos != (Position{X: 387886, Y: 608084, Z: 5298}) {
		t.Errorf("Pos = %+v, 期望 {387886 608084 5298}", mr.Pos)
	}
	if mr.SceneCfgID != 103 {
		t.Errorf("SceneCfgID = %d, 期望 103", mr.SceneCfgID)
	}
	if mr.Yaw != 1800 { // to_rot 的 z(朝向角×10)
		t.Errorf("Yaw = %d, 期望 1800", mr.Yaw)
	}
	if mr.Speed != (Position{X: 211, Y: -352}) { // 前端据此在两包之间外推
		t.Errorf("Speed = %+v, 期望 {211 -352 0}", mr.Speed)
	}
	if !mr.StopMove {
		t.Error("StopMove 应为 true")
	}
	if mr.TimeStamp != 1700000000 {
		t.Errorf("TimeStamp = %d", mr.TimeStamp)
	}
	if mr.SegSpan() != 0 { // 无 move_seg_list
		t.Errorf("SegSpan = %v, 期望 0", mr.SegSpan())
	}
}

// TestParseMoveReqSegs:心跳包带 move_seg_list——那几秒空窗里唯一的真实轨迹(见 Seg 注释)。
func TestParseMoveReqSegs(t *testing.T) {
	seg := func(x, y, z int32, ts uint64) []byte {
		var s []byte
		s = protowire.AppendTag(s, 1, protowire.BytesType)
		s = protowire.AppendBytes(s, pos(x, y, z))
		s = protowire.AppendTag(s, 2, protowire.VarintType)
		s = protowire.AppendVarint(s, ts)
		return s
	}
	var body []byte
	body = protowire.AppendTag(body, 2, protowire.BytesType)
	body = protowire.AppendBytes(body, pos(461166, 621172, 7005))
	body = protowire.AppendTag(body, 6, protowire.VarintType) // move_mode = 6(SMT_FLY_UP)
	body = protowire.AppendVarint(body, 6)
	body = protowire.AppendTag(body, 12, protowire.BytesType) // move_seg_list(repeated)
	body = protowire.AppendBytes(body, seg(460794, 620707, 6900, 1783880000100))
	body = protowire.AppendTag(body, 12, protowire.BytesType)
	body = protowire.AppendBytes(body, seg(461166, 621172, 7005, 1783880000400))

	appBody := append([]byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x11}, body...)
	appBody = append(appBody, []byte("tsf4g\x00")...)

	mr, ok := ParseMoveReq(appBody)
	if !ok {
		t.Fatal("ParseMoveReq 失败")
	}
	if mr.MoveMode != 6 { // SceneMoveType.SMT_FLY_UP
		t.Errorf("MoveMode = %d, 期望 6", mr.MoveMode)
	}
	if len(mr.Segs) != 2 {
		t.Fatalf("Segs 数 = %d, 期望 2", len(mr.Segs))
	}
	if got := mr.SegSpan(); got != 0.3 { // 两点相隔 300ms
		t.Errorf("SegSpan = %v, 期望 0.3", got)
	}
	if mr.Segs[0].Pos != (Position{X: 460794, Y: 620707, Z: 6900}) || mr.Segs[0].TimeStamp != 1783880000100 {
		t.Errorf("Segs[0] = %+v", mr.Segs[0])
	}
	if mr.Segs[1].Pos != mr.Pos { // 末段即本包位置
		t.Errorf("Segs[1].Pos = %+v, 期望与 to_pos 相同 %+v", mr.Segs[1].Pos, mr.Pos)
	}
}

func TestParseMoveReqNegative(t *testing.T) {
	// 魔法学院坐标为负(ox=-886161),确认 int32 负值正确还原。
	var body []byte
	body = protowire.AppendTag(body, 2, protowire.BytesType)
	body = protowire.AppendBytes(body, pos(-800000, -790000, 200))
	mr, ok := ParseMoveReq(body)
	if !ok || mr.Pos != (Position{X: -800000, Y: -790000, Z: 200}) {
		t.Fatalf("负坐标解析错误: ok=%v pos=%+v", ok, mr.Pos)
	}
}

// TestParseAreaActs:区域进/出事件(选层的权威依据,见 ParseAreaActs)。
func TestParseAreaActs(t *testing.T) {
	catcher := func(actor uint64, areaID, funcID uint32) []byte {
		var c []byte
		c = protowire.AppendTag(c, 1, protowire.VarintType)
		c = protowire.AppendVarint(c, actor)
		c = protowire.AppendTag(c, 2, protowire.VarintType)
		c = protowire.AppendVarint(c, uint64(areaID))
		c = protowire.AppendTag(c, 3, protowire.VarintType)
		c = protowire.AppendVarint(c, uint64(funcID))
		return c
	}
	// acts(field 1)= SpaceActionCollection{ enterted_catcher(61), left_catcher(62) }
	var acts []byte
	acts = protowire.AppendTag(acts, 61, protowire.BytesType)
	acts = protowire.AppendBytes(acts, catcher(8126801490545213440, 541030265, 700540))
	acts = protowire.AppendTag(acts, 62, protowire.BytesType)
	acts = protowire.AppendBytes(acts, catcher(8126801490545213440, 541030266, 700546))

	var body []byte
	body = protowire.AppendTag(body, 1, protowire.BytesType)
	body = protowire.AppendBytes(body, acts)

	got := ParseAreaActs(body)
	if len(got) != 2 {
		t.Fatalf("事件数 = %d, 期望 2: %+v", len(got), got)
	}
	if got[0] != (AreaAct{AreaID: 541030265, FuncID: 700540, Enter: true}) {
		t.Errorf("进入事件 = %+v", got[0])
	}
	if got[1] != (AreaAct{AreaID: 541030266, FuncID: 700546, Enter: false}) {
		t.Errorf("离开事件 = %+v", got[1])
	}
}

func TestParseEnterScene(t *testing.T) {
	var body []byte
	body = protowire.AppendTag(body, 1, protowire.BytesType) // ret_info, 空=成功
	body = protowire.AppendBytes(body, nil)
	body = protowire.AppendTag(body, 2, protowire.VarintType)
	body = protowire.AppendVarint(body, 103)
	body = protowire.AppendTag(body, 3, protowire.VarintType)
	body = protowire.AppendVarint(body, 10018)
	body = protowire.AppendTag(body, 5, protowire.VarintType) // home_room_level
	body = protowire.AppendVarint(body, 3)
	body = append(body, []byte("tsf4g")...)

	cfg, res, room, ok := ParseEnterScene(body)
	if !ok || cfg != 103 || res != 10018 || room != 3 {
		t.Fatalf("ParseEnterScene = (%d,%d,%d,%v), 期望 (103,10018,3,true)", cfg, res, room, ok)
	}
}

func TestParseEnterSceneFail(t *testing.T) {
	// ret_info.ret_code != 0 → 失败,不更新场景。
	var ret []byte
	ret = protowire.AppendTag(ret, 1, protowire.VarintType)
	ret = protowire.AppendVarint(ret, 5)
	var body []byte
	body = protowire.AppendTag(body, 1, protowire.BytesType)
	body = protowire.AppendBytes(body, ret)
	body = protowire.AppendTag(body, 3, protowire.VarintType)
	body = protowire.AppendVarint(body, 10018)
	if _, _, _, ok := ParseEnterScene(body); ok {
		t.Fatal("ret_code != 0 时应返回 ok=false")
	}
}

func TestParseTeleport(t *testing.T) {
	// to_pt = Point{pos(1), dir(2)};dir 是旋转,只有 z=Yaw 有意义。
	var toPt []byte
	toPt = protowire.AppendTag(toPt, 1, protowire.BytesType)
	toPt = protowire.AppendBytes(toPt, pos(521886, 635011, 3878))
	toPt = protowire.AppendTag(toPt, 2, protowire.BytesType)
	toPt = protowire.AppendBytes(toPt, pos(0, 0, 600))

	var body []byte
	body = protowire.AppendTag(body, 4, protowire.BytesType) // from_pt 干扰(不能被当成落点)
	body = protowire.AppendBytes(body, pos(1, 2, 3))
	body = protowire.AppendTag(body, 11, protowire.VarintType)
	body = protowire.AppendVarint(body, 103)
	body = protowire.AppendTag(body, 12, protowire.VarintType)
	body = protowire.AppendVarint(body, 10003)
	body = protowire.AppendTag(body, 14, protowire.BytesType) // to_pt(落点)
	body = protowire.AppendBytes(body, toPt)
	body = protowire.AppendTag(body, 31, protowire.VarintType) // home_room_level
	body = protowire.AppendVarint(body, 2)

	tp, ok := ParseTeleport(body)
	if !ok || tp.CfgID != 103 || tp.ResID != 10003 || tp.Room != 2 {
		t.Fatalf("ParseTeleport = %+v (ok=%v), 期望 cfg=103 res=10003 room=2", tp, ok)
	}
	if tp.Pos != (Position{X: 521886, Y: 635011, Z: 3878}) { // 落点:传送一下发就能定位,不必等移动包
		t.Errorf("落点 Pos = %+v", tp.Pos)
	}
	if tp.Yaw != 600 {
		t.Errorf("落点 Yaw = %d, 期望 600", tp.Yaw)
	}
}
