// Package scene 解析场景移动与场景切换消息,供实时地图页跟踪登录账号自己的位置。
// 只解析「自己」:移动包是 c2s(0x0133),当前场景 res 从 s2c 的进入/传送通知跟踪。
// 不解析其他玩家/AOI。字段语义经当前版客户端 Scene luac 坐实,并已用真实 pcap 验证
// (卡洛西亚大陆→魔法学院,24/24 移动包解析成功,投影落点正确;见 docs/data.md 3.1、protocol.md)。
package scene

import (
	"bytes"

	"google.golang.org/protobuf/encoding/protowire"
)

// tsf4gMark 是应用层 protobuf body 之后的 tsf4g 尾标记;解码在其前停止(见 docs/protocol.md)。
var tsf4gMark = []byte("tsf4g")

// 场景相关 opcode(来自 ZoneSvrCmd,见 names.json opcodes,源自 nrc/all.pb)。
const (
	OpSceneMoveReq   = 0x0133 // ZONE_SCENE_MOVE_REQ(307), c2s,自己移动(to_pos/speed/scene_cfg_id)
	OpEnterSceneRsp  = 0x0152 // ZONE_ENTER_SCENE_RSP(338), s2c,进入场景(scene_cfg_id/scene_res_cfg_id)
	OpTeleportNotify = 0x015c // ZONE_SCENE_TELEPORT_NOTIFY(348), s2c,传送(to_scene_cfg_id/to_scene_res_cfg_id)
)

// Position 是场景世界坐标(UE 单位,1=1 厘米;玩家 z 为脚底高度,角色中心+85)。
type Position struct {
	X, Y, Z int32
}

// MoveReq 是一条自己移动上报(ZoneSceneMoveReq)里做地图定位所需的字段。
type MoveReq struct {
	Pos        Position // to_pos(field 2)
	Yaw        int32    // to_rot.z(field 3 的 z),朝向角×10(0.1 度单位);朝向角(度)=Yaw/10
	SceneCfgID int32    // scene_cfg_id(field 17);一个 cfg 可对应多个 res,故仅作校验,定位用当前 res
	StopMove   bool     // stop_move(field 8),停下时上报
	TimeStamp  uint64   // time_stamp(field 1),服务器时钟
}

// ParseMoveReq 从 c2s 移动包的 AppBody 解析 MoveReq。
//
// 实测(见 docs/protocol.md):c2s AppBody 结构为 6 字节子头 + protobuf + 变长 trailer + tsf4g 尾。
// 子头前 2 字节随包变化、余 4 字节为 0;protobuf 之后、tsf4g 之前还有一段变长 trailer(路由/校验)。
// 故不能要求「消费到 tsf4g」,而是贪婪解析已知字段、遇到非该消息的字段(即 trailer 起始)即停。
//
// 起点仍用扫描定位(子头虽实测恒为 6,仍容错):对每个候选起点贪婪解析,取「消费字节最多且解出
// field 2(Position)」者。错位起点会因字段号越界/wire type 不符而很快停下,消费远少于真起点。
func ParseMoveReq(appBody []byte) (MoveReq, bool) {
	body := appBody
	if i := bytes.Index(body, tsf4gMark); i >= 0 {
		body = body[:i] // tsf4g 之前即上限(trailer 仍在其内,靠贪婪解析在 protobuf 末尾停下)
	}
	var best MoveReq
	bestConsumed := -1
	for start := 0; start <= len(body) && start <= 16; start++ {
		mr, consumed, ok := decodeMoveReq(body[start:])
		if ok && consumed > bestConsumed {
			best, bestConsumed = mr, consumed
		}
	}
	return best, bestConsumed >= 0
}

// moveReqWire 是 ZoneSceneMoveReq 各字段的期望 wire type(据 nrc/all.pb 消息定义)。
// 用于锚定 protobuf 起点:错位起点几乎必然在某字段号上撞出不符的 wire type 而被否决。
var moveReqWire = map[protowire.Number]protowire.Type{
	1:  protowire.VarintType, // time_stamp
	2:  protowire.BytesType,  // to_pos
	3:  protowire.BytesType,  // to_rot
	4:  protowire.BytesType,  // speed
	5:  protowire.BytesType,  // acceleration
	6:  protowire.VarintType, // move_mode
	7:  protowire.VarintType, // custom_mode
	8:  protowire.VarintType, // stop_move
	12: protowire.BytesType,  // move_seg_list
	14: protowire.VarintType, // platform_actor_id
	15: protowire.BytesType,  // ctrl_rot
	17: protowire.VarintType, // scene_cfg_id
	18: protowire.VarintType, // ride_move
	19: protowire.BytesType,  // mate_point
	20: protowire.VarintType, // mate_move_mode
}

// decodeMoveReq 从 b 起贪婪解析 ZoneSceneMoveReq 字段,遇到不属于该消息的字段号或 wire type
// 不符即停(此处即 protobuf 末尾/trailer 起始)。返回解析结果、已消费字节数、以及是否解出
// field 2(to_pos,合法 Position)。consumed 供 ParseMoveReq 在多个候选起点间择优。
func decodeMoveReq(b []byte) (mr MoveReq, consumed int, ok bool) {
	var gotPos bool
	rest := b
loop:
	for len(rest) > 0 {
		num, typ, n := protowire.ConsumeTag(rest)
		if n < 0 {
			break // tag 解不出:到达 trailer
		}
		want, known := moveReqWire[num]
		if !known || want != typ {
			break // 字段号越界或 wire type 不符:到达 trailer
		}
		next := rest[n:]
		switch num {
		case 1:
			v, m := protowire.ConsumeVarint(next)
			if m < 0 {
				return mr, consumed, gotPos
			}
			mr.TimeStamp = v
			next = next[m:]
		case 2:
			sub, m := protowire.ConsumeBytes(next)
			if m < 0 {
				return mr, consumed, gotPos
			}
			p, pok := parsePosition(sub)
			if !pok {
				break loop // field 2 不是 Position:起点错位,停止(交由更优起点)
			}
			mr.Pos, gotPos = p, true
			next = next[m:]
		case 3: // to_rot(旋转,Position 形状:x=Roll y=Pitch z=Yaw);只取 z=朝向角×10
			sub, m := protowire.ConsumeBytes(next)
			if m < 0 {
				return mr, consumed, gotPos
			}
			if p, ok := parsePosition(sub); ok {
				mr.Yaw = p.Z
			}
			next = next[m:]
		case 8:
			v, m := protowire.ConsumeVarint(next)
			if m < 0 {
				return mr, consumed, gotPos
			}
			mr.StopMove = v != 0
			next = next[m:]
		case 17:
			v, m := protowire.ConsumeVarint(next)
			if m < 0 {
				return mr, consumed, gotPos
			}
			mr.SceneCfgID = int32(v)
			next = next[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, next)
			if m < 0 {
				return mr, consumed, gotPos
			}
			next = next[m:]
		}
		consumed += len(rest) - len(next)
		rest = next
	}
	return mr, consumed, gotPos
}

// parsePosition 把子消息字节解为 Position。Position 仅含 x/y/z(field 1-3,int32/varint),
// 出现其他字段号或非 varint 即判为「不是 Position」,用于锚定移动包起点。
func parsePosition(b []byte) (Position, bool) {
	var p Position
	rest := b
	for len(rest) > 0 {
		num, typ, n := protowire.ConsumeTag(rest)
		if n < 0 || typ != protowire.VarintType || num < 1 || num > 3 {
			return Position{}, false
		}
		rest = rest[n:]
		v, m := protowire.ConsumeVarint(rest)
		if m < 0 {
			return Position{}, false
		}
		switch num {
		case 1:
			p.X = int32(v)
		case 2:
			p.Y = int32(v)
		case 3:
			p.Z = int32(v)
		}
		rest = rest[m:]
	}
	return p, true
}

// ParseEnterScene 从 s2c ZoneEnterSceneRsp 的 protobuf body 取 scene_cfg_id(field 2)、
// scene_res_cfg_id(field 3)与 home_room_level(field 5,家园室内底图按此分层选图)。
// ret_info(field 1)非零表示失败,则返回 ok=false。
func ParseEnterScene(body []byte) (cfgID, resID, room int32, ok bool) {
	fail := false
	scanFields(body, func(num protowire.Number, typ protowire.Type, val []byte, v uint64) {
		switch {
		case num == 1 && typ == protowire.BytesType:
			if retFailed(val) {
				fail = true
			}
		case num == 2 && typ == protowire.VarintType:
			cfgID = int32(v)
		case num == 3 && typ == protowire.VarintType:
			resID = int32(v)
		case num == 5 && typ == protowire.VarintType:
			room = int32(v)
		}
	})
	return cfgID, resID, room, !fail && resID != 0
}

// ParseTeleport 从 s2c ZoneSceneTeleportNotify 的 protobuf body 取 to_scene_cfg_id(field 11)、
// to_scene_res_cfg_id(field 12)与 home_room_level(field 31)。
func ParseTeleport(body []byte) (cfgID, resID, room int32, ok bool) {
	scanFields(body, func(num protowire.Number, typ protowire.Type, val []byte, v uint64) {
		switch {
		case num == 11 && typ == protowire.VarintType:
			cfgID = int32(v)
		case num == 12 && typ == protowire.VarintType:
			resID = int32(v)
		case num == 31 && typ == protowire.VarintType:
			room = int32(v)
		}
	})
	return cfgID, resID, room, resID != 0
}

// retFailed 判断 RetInfo 子消息是否表示失败(field 1 = ret_code,非 0 即失败)。
func retFailed(b []byte) bool {
	failed := false
	scanFields(b, func(num protowire.Number, typ protowire.Type, _ []byte, v uint64) {
		if num == 1 && typ == protowire.VarintType && v != 0 {
			failed = true
		}
	})
	return failed
}

// scanFields 遍历顶层 protobuf 字段;对 varint 传 v,对 bytes 传 val(子消息/字符串原始字节)。
// 解码出错即静默停止(容忍尾部 tsf4g 等非 protobuf 残留)。
func scanFields(b []byte, fn func(num protowire.Number, typ protowire.Type, val []byte, v uint64)) {
	rest := b
	for len(rest) > 0 {
		num, typ, n := protowire.ConsumeTag(rest)
		if n < 0 {
			return
		}
		rest = rest[n:]
		switch typ {
		case protowire.VarintType:
			v, m := protowire.ConsumeVarint(rest)
			if m < 0 {
				return
			}
			fn(num, typ, nil, v)
			rest = rest[m:]
		case protowire.BytesType:
			val, m := protowire.ConsumeBytes(rest)
			if m < 0 {
				return
			}
			fn(num, typ, val, 0)
			rest = rest[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, rest)
			if m < 0 {
				return
			}
			rest = rest[m:]
		}
	}
}
