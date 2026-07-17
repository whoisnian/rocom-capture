// Package pet 负责把宠物相关的应用层消息解析为业务模型，并检测宠物增减事件。
package pet

import (
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/whoisnian/rocom-capture/internal/pb"
)

// 宠物相关 opcode(来自 ZoneSvrCmd enum,见 names.json opcodes,源自游戏描述符 all.pb)。
const (
	OpGetPetInfoByPageRsp    = 0x1346 // ZONE_GET_PET_INFO_BY_PAGE_RSP(4934), 分页宠物列表
	OpPetFreeRsp             = 0x01c5 // ZONE_PET_FREE_RSP(453), 放生(下行含 pet_gid 列表)
	OpTogetherCatchGiftRsp   = 0x1808 // ZONE_TOGETHER_CATCH_PET_FOR_GIFTING_RSP(6152), 赠送共同捕捉的宠物给好友(执行回包含 gid)
	OpCrackEggRsp            = 0x030c // ZONE_CRACK_EGG_RSP(780), 孵蛋(新宠物嵌在 goods_reward)
	OpPetCatchRsp            = 0x1983 // ZONE_SCENE_THROW_CATCH_FINISH_RSP(6531), 战斗外捕捉(赛季球/高级球)
	OpGoodsRewardNotify      = 0x0243 // ZONE_GOODS_REWARD_NOTIFY, 奖励通知(普通战斗内捕捉等新宠物)
	OpPlayerSyncNotify       = 0x0160 // ZONE_PLAYER_SYNC_NOTIFY, 玩家数据同步(花种战斗内捕捉走此通道)
	OpBattleFinishNotify     = 0x132c // ZONE_BATTLE_FINISH_NOTIFY(4908), 战斗结束通知(传说精灵战后捕捉,catch_way=5,唯一下发通道)
	OpLoginRsp               = 0x0102 // ZONE_LOGIN_RSP(258), 登录数据(含完整背包 PetBackpackInfo)
	OpPetBoxChangePetRsp     = 0x1888 // ZONE_PET_BOX_CHANGE_PET_RSP(6280), 盒位移动回包(box_pet_change 增量)
	OpPetBoxSettingUpRsp     = 0x1891 // ZONE_PET_BOX_SETTING_UP_RSP(6289), 整理/编辑排列回包(改名/换位,全量 repeated PetBox)
	OpPetBoxUnlockRsp        = 0x1883 // ZONE_PET_BOX_UNLOCK_RSP(6275), 解锁新盒回包(field2=新 PetBox 增量)
	OpPetBoxSetMarkTypeRsp   = 0x1893 // ZONE_PET_BOX_SET_MARK_TYPE_RSP(6291), 设标记/改名回包(单盒元数据增量)
	OpPetMedalCommonRsp      = 0x141e // ZONE_PET_MEDAL_COMMON_RSP(5150), 换牌等回包(含更新后 PetData)
	OpPetEvoluteRsp          = 0x01ae // ZONE_PET_EVOLUTE_RSP(430), 进化回包(含进化后完整 PetData,base_conf_id 已换形态)
	OpUpdatePetCollectTagRsp = 0x0403 // ZONE_UPDATE_PET_COLLECT_TAG_RSP(1027), 伙伴标记增删改回包(含更新后完整 PetData)
)

// 盒子操作 opcode 区间(ZoneSvrCmd 十进制 6272-6292,如 TIDY_RSP/SETTING_UP_RSP 携带全量盒子)。
const boxOpcodeLo, boxOpcodeHi = 6272, 6292

// 队伍变更 opcode 区间(524 TEAM_CHANGE / 526 CHANGE_MAIN_TEAM 的 REQ/RSP,回包带刷新后队伍快照)。
const teamOpcodeLo, teamOpcodeHi = 524, 527

// CarriesBackpack 判断该 opcode 是否可能携带盒子布局(登录数据或盒子操作回包)。
func CarriesBackpack(opcode uint16) bool {
	return opcode == OpLoginRsp || (opcode >= boxOpcodeLo && opcode <= boxOpcodeHi)
}

// CarriesTeam 判断该 opcode 是否可能携带大世界队伍快照(登录、盒子操作或队伍变更回包)。
func CarriesTeam(opcode uint16) bool {
	return CarriesBackpack(opcode) || (opcode >= teamOpcodeLo && opcode <= teamOpcodeHi)
}

// warehouseMark 是 WarehouseMarkType(盒子分类标记)枚举值 -> 中文。
var warehouseMark = map[int32]string{1: "首领", 2: "污染", 4: "奇异", 8: "炫彩", 16: "闪光"}

// MarkName 返回盒子分类标记中文(0/未知返回空)。
func MarkName(v int32) string { return warehouseMark[v] }

// BoxEntry 是一只宠物的盒子位置(供 store 落库)。
type BoxEntry struct {
	Gid     uint32
	BoxID   int32
	Slot    int32
	BoxName string
	Mark    int32
}

// BoxMeta 是一个盒子的元数据(名称/标记/是否锁定),与是否有宠物无关——空盒子也在内。
// 盒号 box_id 即展示位置(1 起),改名/换位/解锁都会更新这份全量元数据。
type BoxMeta struct {
	BoxID int32
	Name  string
	Mark  int32
	Lock  bool
}

// boxesToLayout 把一组 PetBox 展开为「占用项(有宠物的格)」+「全量盒子元数据(含空盒)」。
// 任一盒子的 vacancy/box_id 越界即判为误解析,返回 (nil,nil,false)。
func boxesToLayout(boxes []*pb.PetBox) (entries []BoxEntry, metas []BoxMeta, ok bool) {
	for _, bx := range boxes {
		if bx.GetVacancyNum() < 0 || bx.GetVacancyNum() > 200 || bx.GetBoxId() < 0 || bx.GetBoxId() > 1000 {
			return nil, nil, false
		}
		name := string(bx.GetBoxName())
		mark := int32(bx.GetMarkType())
		metas = append(metas, BoxMeta{BoxID: bx.GetBoxId(), Name: name, Mark: mark, Lock: bx.GetLock()})
		for slot, g := range bx.GetPetGid() {
			if g != 0 {
				entries = append(entries, BoxEntry{Gid: g, BoxID: bx.GetBoxId(), Slot: int32(slot), BoxName: name, Mark: mark})
			}
		}
	}
	return entries, metas, true
}

// collectBackpacks 递归收集 body 里所有 boxes 非空的 PetBackpackInfo 候选。
func collectBackpacks(body []byte, out *[]*pb.PetBackpackInfo) {
	b := body
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if typ == protowire.BytesType {
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				break
			}
			var bp pb.PetBackpackInfo
			if proto.Unmarshal(v, &bp) == nil && len(bp.GetBoxes()) > 0 {
				*out = append(*out, &bp)
			}
			collectBackpacks(v, out)
			b = b[m:]
		} else {
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				break
			}
			b = b[m:]
		}
	}
}

// ParseBackpack 在 body 里找最完整的 PetBackpackInfo(登录/整理回包),展开为盒子占用项 +
// 全量盒子元数据。位置 = 宠物 gid 在 PetBox.pet_gid[] 中的下标(空格为 0,跳过)。取非零 gid 数
// 最多的候选以排除误解析;少于 5 只视为非真实背包,返回 (nil,nil)。
func ParseBackpack(body []byte) ([]BoxEntry, []BoxMeta) {
	var cands []*pb.PetBackpackInfo
	collectBackpacks(body, &cands)

	var best *pb.PetBackpackInfo
	bestN := 0
	for _, bp := range cands {
		n := 0
		for _, bx := range bp.GetBoxes() {
			if bx.GetVacancyNum() < 0 || bx.GetVacancyNum() > 200 || bx.GetBoxId() < 0 || bx.GetBoxId() > 1000 {
				n = -1 // 数值不合理,整体判为误解析
				break
			}
			for _, g := range bx.GetPetGid() {
				if g != 0 {
					n++
				}
			}
		}
		if n > bestN {
			bestN, best = n, bp
		}
	}
	if best == nil || bestN < 5 {
		return nil, nil
	}
	entries, metas, _ := boxesToLayout(best.GetBoxes())
	return entries, metas
}

// ParseBoxSettingUp 解析 ZonePetBoxSettingUpRsp(整理/编辑排列,含改名/换位)的 body。
// 该回包不是 PetBackpackInfo,而是 { RetInfo ret_info=1; repeated PetBox boxes=2; }——盒子直接
// 挂在顶层 field2。逐个把 field2 反序列化为 PetBox,展开为占用项 + 全量元数据;盒子数 <5 或数值
// 越界视为误解析返回 (nil,nil)。box_id 即新的展示位置,故整体替换即让盒内宠物随盒换位。
func ParseBoxSettingUp(body []byte) ([]BoxEntry, []BoxMeta) {
	var boxes []*pb.PetBox
	b := body
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if num == 2 && typ == protowire.BytesType {
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				break
			}
			var bx pb.PetBox
			if proto.Unmarshal(v, &bx) == nil && bx.BoxId != nil {
				boxes = append(boxes, &bx)
			}
			b = b[m:]
		} else {
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				break
			}
			b = b[m:]
		}
	}
	if len(boxes) < 5 {
		return nil, nil
	}
	entries, metas, ok := boxesToLayout(boxes)
	if !ok {
		return nil, nil
	}
	return entries, metas
}

// ParseBoxUnlock 解析 ZonePetBoxUnlockRsp(解锁新盒)的 body,返回新盒的元数据(增量,单盒)。
// 结构: { RetInfo ret_info=1(含玩家数据); PetBox new_box=2 }——新盒挂在顶层 field2。
// box_id 越界或未找到返回 nil。
func ParseBoxUnlock(body []byte) *BoxMeta {
	b := body
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if num == 2 && typ == protowire.BytesType {
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				break
			}
			var bx pb.PetBox
			if proto.Unmarshal(v, &bx) == nil && bx.BoxId != nil &&
				bx.GetBoxId() > 0 && bx.GetBoxId() <= 1000 {
				return &BoxMeta{BoxID: bx.GetBoxId(), Name: string(bx.GetBoxName()), Mark: int32(bx.GetMarkType()), Lock: bx.GetLock()}
			}
			b = b[m:]
		} else {
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				break
			}
			b = b[m:]
		}
	}
	return nil
}

// ParseBoxSetMark 解析 ZonePetBoxSetMarkTypeRsp(设标记/改名)的 body,返回该盒更新后的元数据。
// 结构(非 PetBox): { RetInfo ret_info=1; uint32 box_id=2; WarehouseMarkType mark=3; bytes name=4;
// bool lock=5 }。仅 ret_info.result==0 且 box_id 合法才返回,否则 nil。
func ParseBoxSetMark(body []byte) *BoxMeta {
	var result int64 = -1
	var boxID, mark int32
	var name string
	var lock bool
	b := body
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		var m int
		switch {
		case num == 1 && typ == protowire.BytesType: // ret_info: 取 field1 作 result
			var v []byte
			v, m = protowire.ConsumeBytes(b)
			if m >= 0 {
				if rn, rt, rk := protowire.ConsumeTag(v); rk >= 0 && rn == 1 && rt == protowire.VarintType {
					if rv, rm := protowire.ConsumeVarint(v[rk:]); rm >= 0 {
						result = int64(rv)
					}
				}
			}
		case num == 2 && typ == protowire.VarintType:
			var v uint64
			v, m = protowire.ConsumeVarint(b)
			boxID = int32(v)
		case num == 3 && typ == protowire.VarintType:
			var v uint64
			v, m = protowire.ConsumeVarint(b)
			mark = int32(v)
		case num == 4 && typ == protowire.BytesType:
			var v []byte
			v, m = protowire.ConsumeBytes(b)
			name = string(v)
		case num == 5 && typ == protowire.VarintType:
			var v uint64
			v, m = protowire.ConsumeVarint(b)
			lock = v != 0
		default:
			m = protowire.ConsumeFieldValue(num, typ, b)
		}
		if m < 0 {
			break
		}
		b = b[m:]
	}
	if result != 0 || boxID <= 0 || boxID > 1000 {
		return nil
	}
	return &BoxMeta{BoxID: boxID, Name: name, Mark: mark, Lock: lock}
}

// hasCJK 判断字节串是否含中日韩统一表意文字(宠物名为中文)。
func hasCJK(b []byte) bool {
	for _, r := range string(b) {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

// FindNewPet 在响应 body 中递归查找新宠物 PetData。
// 孵蛋/捕捉获得的宠物作为奖励嵌套在 ret_info.goods_reward.rewards[].pet 里，
// 逐层路径随消息而异，这里递归尝试把每个 LEN 子字段反序列化为 PetData，
// 以 gid/conf_id/name 均有效作为命中判据。
func FindNewPet(body []byte) *pb.PetData {
	b := body
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if typ == protowire.BytesType {
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				break
			}
			var pd pb.PetData
			if proto.Unmarshal(v, &pd) == nil &&
				pd.GetGid() > 0 && pd.GetConfId() > 1000 && hasCJK(pd.GetName()) {
				return &pd
			}
			if r := FindNewPet(v); r != nil {
				return r
			}
			b = b[m:]
		} else {
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				break
			}
			b = b[m:]
		}
	}
	return nil
}

// PTTBigWorld 是 PlayerTeamType.PTT_BIG_WORLD(大世界队伍 team_type)。
const PTTBigWorld = 1

// MedalOwn 是一只宠物拥有的一枚奖牌(来自登录数据的 PetMedalInfo)。
type MedalOwn struct {
	Gid     uint32
	MedalID uint32
}

// ParsePetMedals 从登录数据(PlayerSvrDataInfo.pet_medal_info)递归解析每只宠物拥有的奖牌。
// PetMedalInfo:#1 medal_conf_id / #2 medal_type / #3 owner 组[],组内 #2 记录里宠物 gid = #8??#6??#2。
// 注:线上 wire 格式与 all.pb 的 PetMedalOwnerInfo 定义不一致(版本偏移),故纯按 wire 经验解码。
func ParsePetMedals(body []byte) []MedalOwn {
	var out []MedalOwn
	collectPetMedals(body, &out)
	return out
}

func collectPetMedals(body []byte, out *[]MedalOwn) {
	// 形似 PetMedalInfo(#1 在奖牌区间 + 有 medal_type + 有 owner 组)则提取,不再深入。
	if mc, ok := wireVarint(body, 1); ok && mc >= 1000 && mc < 2000 {
		if _, hasType := wireVarint(body, 2); hasType {
			if groups := wireSubs(body, 3); len(groups) > 0 {
				for _, g := range groups {
					for _, rec := range wireSubs(g, 2) {
						if gid := recPetGid(rec); gid != 0 {
							*out = append(*out, MedalOwn{Gid: gid, MedalID: uint32(mc)})
						}
					}
				}
				return
			}
		}
	}
	b := body
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return
		}
		b = b[n:]
		if typ == protowire.BytesType {
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return
			}
			collectPetMedals(v, out)
			b = b[m:]
		} else {
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return
			}
			b = b[m:]
		}
	}
}

// recPetGid 从奖牌记录里取宠物 gid(优先 obtain_pet_gid #8,退 #6,再退 #2)。
func recPetGid(rec []byte) uint32 {
	for _, f := range []uint32{8, 6, 2} {
		if v, ok := wireVarint(rec, f); ok {
			return uint32(v)
		}
	}
	return 0
}

// wireVarint 取消息里指定字段号的 varint 值(无/类型不符则 false)。
func wireVarint(b []byte, want uint32) (uint64, bool) {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return 0, false
		}
		b = b[n:]
		if uint32(num) == want && typ == protowire.VarintType {
			v, m := protowire.ConsumeVarint(b)
			if m < 0 {
				return 0, false
			}
			return v, true
		}
		m := protowire.ConsumeFieldValue(num, typ, b)
		if m < 0 {
			return 0, false
		}
		b = b[m:]
	}
	return 0, false
}

// wireSubs 取消息里指定字段号的所有 length-delimited 子消息。
func wireSubs(b []byte, want uint32) [][]byte {
	var out [][]byte
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if uint32(num) == want && typ == protowire.BytesType {
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				break
			}
			out = append(out, v)
			b = b[m:]
		} else {
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				break
			}
			b = b[m:]
		}
	}
	return out
}

// wireBytes 取消息里指定字段号第一个 length-delimited 值(无/类型不符则 false)。
func wireBytes(b []byte, want uint32) ([]byte, bool) {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil, false
		}
		b = b[n:]
		if uint32(num) == want && typ == protowire.BytesType {
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return nil, false
			}
			return v, true
		}
		m := protowire.ConsumeFieldValue(num, typ, b)
		if m < 0 {
			return nil, false
		}
		b = b[m:]
	}
	return nil, false
}

// ParseLoginAccount 从 ZoneLoginRsp(opcode 0x0102)取玩家 user_id 与昵称。
// body 结构(实测,见 docs/architecture.md「多账号隔离」):{ #1: RetInfo, #2: LoginData{ #1: base{...} } },
// base 内 #1=user_id(varint)、#2=openid(str)、#3=nickname(bytes)。user_id 全局唯一、
// 跨设备/跨服稳定,作账号身份键;昵称仅供展示(可能为占位名如「你的名字」)。
func ParseLoginAccount(body []byte) (userID uint64, name string, ok bool) {
	data, ok2 := wireBytes(body, 2) // LoginData
	if !ok2 {
		return 0, "", false
	}
	base, ok1 := wireBytes(data, 1) // LoginData.#1(玩家基础信息)
	if !ok1 {
		return 0, "", false
	}
	id, okID := wireVarint(base, 1)
	if !okID || id == 0 {
		return 0, "", false
	}
	if nb, ok3 := wireBytes(base, 3); ok3 {
		name = string(nb)
	}
	return id, name, true
}

// ParseBoxMoves 从盒子操作回包(GoodsChangeItem.box_pet_change)抽取盒位增量变更。
// 每个 PetBoxPetChange = {pet_gid, is_in_team, id=box_id, pos(1 起)};只取非在队、gid 非 0、
// box/pos 在合理范围的盒位放置,转为 BoxEntry(Slot=pos-1)。空位变更(gid=0)被移走的宠物
// 必有对应的非 0 落位项,故跳过。
func ParseBoxMoves(body []byte) []BoxEntry {
	var cands []*pb.PetBoxPetChange
	collectBoxChanges(body, &cands)
	var out []BoxEntry
	for _, c := range cands {
		box, pos := c.GetId(), c.GetPos()
		if c.GetIsInTeam() || c.GetPetGid() == 0 || box < 1 || box > 50 || pos < 1 || pos > 30 {
			continue
		}
		out = append(out, BoxEntry{Gid: c.GetPetGid(), BoxID: box, Slot: pos - 1})
	}
	return out
}

// collectBoxChanges 递归收集 body 里所有可解析为 PetBoxPetChange 的子消息。
func collectBoxChanges(body []byte, out *[]*pb.PetBoxPetChange) {
	b := body
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if typ == protowire.BytesType {
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				break
			}
			var c pb.PetBoxPetChange
			if proto.Unmarshal(v, &c) == nil && c.Id != nil && c.Pos != nil {
				*out = append(*out, &c)
			}
			collectBoxChanges(v, out)
			b = b[m:]
		} else {
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				break
			}
			b = b[m:]
		}
	}
}

// TeamEntry 是一只宠物在大世界队伍中的位置(team_idx 第几队,pos 队内位置 0 起,每队 6 位)。
type TeamEntry struct {
	Gid     uint32
	TeamIdx int32
	Pos     int32
}

// collectTeamInfos 递归收集 body 里所有可解析的 PetTeamInfo 候选。
func collectTeamInfos(body []byte, out *[]*pb.PetTeamInfo) {
	b := body
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if typ == protowire.BytesType {
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				break
			}
			var ti pb.PetTeamInfo
			if proto.Unmarshal(v, &ti) == nil && len(ti.GetTeams()) > 0 {
				*out = append(*out, &ti)
			}
			collectTeamInfos(v, out)
			b = b[m:]
		} else {
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				break
			}
			b = b[m:]
		}
	}
}

// ParseTeams 在 body 里找大世界队伍(team_type==PTT_BIG_WORLD)的 PetTeamInfo,
// 展开为 gid->(team_idx, pos) 列表。取含宠物数最多的大世界候选以排除误解析。
func ParseTeams(body []byte) []TeamEntry {
	var cands []*pb.PetTeamInfo
	collectTeamInfos(body, &cands)

	var best *pb.PetTeamInfo
	bestN := 0
	for _, ti := range cands {
		if ti.GetTeamType() != PTTBigWorld {
			continue
		}
		n := 0
		for _, t := range ti.GetTeams() {
			for _, pi := range t.GetPetInfos() {
				if pi.GetPetGid() != 0 {
					n++
				}
			}
		}
		if n > bestN {
			bestN, best = n, ti
		}
	}
	if best == nil {
		return nil
	}

	// 队号取 teams[] 数组下标(实测 PetTeam.team_idx 恒 0、无队名,故以数组顺序为准)。
	var out []TeamEntry
	for ti, t := range best.GetTeams() {
		for pos, pi := range t.GetPetInfos() {
			if g := pi.GetPetGid(); g != 0 {
				out = append(out, TeamEntry{Gid: g, TeamIdx: int32(ti), Pos: int32(pos)})
			}
		}
	}
	return out
}

// ParseFreeRsp 解析 ZonePetFreeRsp(放生)的 body，返回被放生的 gid 列表。
// 消息结构: { RetInfo ret_info=1; repeated uint32 pet_gid=2; }
func ParseFreeRsp(body []byte) []uint32 {
	var gids []uint32
	b := body
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		var m int
		if num == 2 && typ == protowire.VarintType { // 非 packed
			var v uint64
			v, m = protowire.ConsumeVarint(b)
			if m >= 0 {
				gids = append(gids, uint32(v))
			}
		} else if num == 2 && typ == protowire.BytesType { // packed repeated
			var v []byte
			v, m = protowire.ConsumeBytes(b)
			for len(v) > 0 {
				x, k := protowire.ConsumeVarint(v)
				if k < 0 {
					break
				}
				gids = append(gids, uint32(x))
				v = v[k:]
			}
		} else {
			m = protowire.ConsumeFieldValue(num, typ, b)
		}
		if m < 0 {
			break
		}
		b = b[m:]
	}
	return gids
}

// ParseTogetherCatchGiftRsp 解析 ZoneTogetherCatchPetForGiftingRsp(赠送共同捕捉的宠物)的 body，
// 返回被赠送出的 pet_gid(0 表示非赠送执行回包)。捕捉与赠送是相互独立的事件:先前捕捉照常入库,
// 之后玩家开盒子手动选择赠送才走本回包,应据此从自己的库中移除并记一条「赠送」失去事件。
// 该 opcode 有两种回包且都在顶层带 pet_gid(field3):一种是内嵌完整 PetData 的宠物详情(赠送前预览/
// 同步,不代表已送出),一种是紧凑的执行确认 ack({ RetInfo ret_info=1; uint32 _=2; uint32 pet_gid=3; })。
// 只认后者:内嵌 PetData 的直接返回 0,避免预览误记 + 两种回包重复记;ack 里 result==0 且 gid>0 才算成功。
func ParseTogetherCatchGiftRsp(body []byte) uint32 {
	if FindNewPet(body) != nil { // 宠物详情回包(预览/同步),非执行确认
		return 0
	}
	var result int64 = -1 // ret_info.result(field1.field1);-1=未见
	var gid uint32
	b := body
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		var m int
		switch {
		case num == 1 && typ == protowire.BytesType: // ret_info: 取其 field1 作为 result
			var v []byte
			v, m = protowire.ConsumeBytes(b)
			if m >= 0 {
				if rnum, rtyp, rn := protowire.ConsumeTag(v); rn >= 0 && rnum == 1 && rtyp == protowire.VarintType {
					if rv, rm := protowire.ConsumeVarint(v[rn:]); rm >= 0 {
						result = int64(rv)
					}
				}
			}
		case num == 3 && typ == protowire.VarintType: // pet_gid
			var v uint64
			v, m = protowire.ConsumeVarint(b)
			if m >= 0 {
				gid = uint32(v)
			}
		default:
			m = protowire.ConsumeFieldValue(num, typ, b)
		}
		if m < 0 {
			break
		}
		b = b[m:]
	}
	if result == 0 {
		return gid
	}
	return 0
}

// PageResult 是一页宠物列表的解析结果。
type PageResult struct {
	TotalPage uint32
	ReqPage   uint32
	PageNum   uint32
	Pets      []*pb.PetData
}

// ParsePetListRsp 解析 ZoneGetPetInfoByPageRsp(opcode 0x1346)的 protobuf body。
// 只取需要的字段：total_page=2, req_page=3, pet_info=4(PetDataInfoList), page_num=5。
// 对 wire 解析容错：遇到无法识别的尾部即停止，返回已解析出的内容。
func ParsePetListRsp(body []byte) *PageResult {
	res := &PageResult{}
	b := body
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]

		var m int
		switch {
		case num == 2 && typ == protowire.VarintType:
			var v uint64
			v, m = protowire.ConsumeVarint(b)
			res.TotalPage = uint32(v)
		case num == 3 && typ == protowire.VarintType:
			var v uint64
			v, m = protowire.ConsumeVarint(b)
			res.ReqPage = uint32(v)
		case num == 5 && typ == protowire.VarintType:
			var v uint64
			v, m = protowire.ConsumeVarint(b)
			res.PageNum = uint32(v)
		case num == 4 && typ == protowire.BytesType:
			var v []byte
			v, m = protowire.ConsumeBytes(b)
			if m >= 0 {
				var list pb.PetDataInfoList
				if proto.Unmarshal(v, &list) == nil {
					res.Pets = append(res.Pets, list.PetData...)
				}
			}
		default:
			m = protowire.ConsumeFieldValue(num, typ, b)
		}
		if m < 0 {
			break
		}
		b = b[m:]
	}
	return res
}
