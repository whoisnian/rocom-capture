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
	if !mr.StopMove {
		t.Error("StopMove 应为 true")
	}
	if mr.TimeStamp != 1700000000 {
		t.Errorf("TimeStamp = %d", mr.TimeStamp)
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
	var body []byte
	body = protowire.AppendTag(body, 4, protowire.BytesType) // from_pt 干扰
	body = protowire.AppendBytes(body, pos(1, 2, 3))
	body = protowire.AppendTag(body, 11, protowire.VarintType)
	body = protowire.AppendVarint(body, 103)
	body = protowire.AppendTag(body, 12, protowire.VarintType)
	body = protowire.AppendVarint(body, 10003)
	body = protowire.AppendTag(body, 31, protowire.VarintType) // home_room_level
	body = protowire.AppendVarint(body, 2)

	cfg, res, room, ok := ParseTeleport(body)
	if !ok || cfg != 103 || res != 10003 || room != 2 {
		t.Fatalf("ParseTeleport = (%d,%d,%d,%v), 期望 (103,10003,2,true)", cfg, res, room, ok)
	}
}
