package pet

import (
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/whoisnian/rocom-capture/internal/pb"
	"github.com/whoisnian/rocom-capture/internal/wire"
)

// petDataAddTimeFloor 是 PetData.add_time 的合理下界(2020-09,早于游戏上线)。
// 每只宠物都带创建时间(秒级 unix 时间),而恰好凑出 gid/conf_id 的普通子消息给出这么大的
// field 32 varint 的概率足够低,故以它证明这段字节确实是宠物。
const petDataAddTimeFloor = 1_600_000_000

// FindNewPet 在响应 body 中递归查找新宠物 PetData。
// 孵蛋/捕捉获得的宠物作为奖励嵌套在 ret_info.goods_reward.rewards[].pet 里,逐层路径随消息
// 而异,故递归尝试把每个 LEN 子字段反序列化为 PetData,以 gid/conf_id/add_time 均有效作为命中判据。
//
// 不能拿 name 作判据:新获得的宠物 name 为空(客户端按配置显示种类名),各通道一样,分页全量
// 0x1346 里也是空的;只有早先创建的老宠物还保留 name。
func FindNewPet(body []byte) *pb.PetData {
	var found *pb.PetData
	wire.Walk(body, func(v []byte) bool {
		if found != nil {
			return false
		}
		var pd pb.PetData
		if proto.Unmarshal(v, &pd) == nil &&
			pd.GetGid() > 0 && pd.GetConfId() > 1000 && pd.GetAddTime() >= petDataAddTimeFloor {
			found = &pd
			return false
		}
		return true
	})
	return found
}

// ParseFreeRsp 解析 ZonePetFreeRsp(放生)的 body,返回被放生的 gid 列表。
// 消息结构: { RetInfo ret_info=1; repeated uint32 pet_gid=2; }(packed 与非 packed 都兼容)。
func ParseFreeRsp(body []byte) []uint32 {
	var gids []uint32
	wire.ScanFields(body, func(num protowire.Number, typ protowire.Type, val []byte, v uint64) {
		if num != 2 {
			return
		}
		switch typ {
		case protowire.VarintType:
			gids = append(gids, uint32(v))
		case protowire.BytesType:
			for _, x := range wire.PackedVarints(val) {
				gids = append(gids, uint32(x))
			}
		}
	})
	return gids
}

// ParseTogetherCatchGiftRsp 解析 ZoneTogetherCatchPetForGiftingRsp(赠送共同捕捉的宠物)的 body,
// 返回被赠送出的 pet_gid(0 表示非赠送执行回包)。捕捉与赠送是相互独立的事件:先前捕捉照常入库,
// 之后玩家开盒子手动选择赠送才走本回包,应据此从自己的库中移除。
// 该 opcode 有两种回包且都在顶层带 pet_gid(field3):一种是内嵌完整 PetData 的宠物详情(赠送前预览/
// 同步,不代表已送出),一种是紧凑的执行确认 ack({ RetInfo ret_info=1; uint32 _=2; uint32 pet_gid=3; })。
// 只认后者:内嵌 PetData 的直接返回 0,避免预览误记 + 两种回包重复记;ack 里 result==0 且 gid>0 才算成功。
func ParseTogetherCatchGiftRsp(body []byte) uint32 {
	if FindNewPet(body) != nil { // 宠物详情回包(预览/同步),非执行确认
		return 0
	}
	if retResult(body) != 0 {
		return 0
	}
	gid, _ := wire.Varint(body, 3)
	return uint32(gid)
}

// PageResult 是一页宠物列表的解析结果。
type PageResult struct {
	TotalPage uint32
	ReqPage   uint32
	PageNum   uint32 // 实为每页容量(实测恒 50),不是页序,不能用作累积接续判据
	Pets      []*pb.PetData
}

// ParsePetListRsp 解析 ZoneGetPetInfoByPageRsp(opcode 0x1346)的 protobuf body。
// 只取需要的字段:total_page=2, req_page=3, pet_info=4(PetDataInfoList), page_num=5。
func ParsePetListRsp(body []byte) *PageResult {
	res := &PageResult{}
	wire.ScanFields(body, func(num protowire.Number, typ protowire.Type, val []byte, v uint64) {
		switch {
		case num == 2 && typ == protowire.VarintType:
			res.TotalPage = uint32(v)
		case num == 3 && typ == protowire.VarintType:
			res.ReqPage = uint32(v)
		case num == 5 && typ == protowire.VarintType:
			res.PageNum = uint32(v)
		case num == 4 && typ == protowire.BytesType:
			var list pb.PetDataInfoList
			if proto.Unmarshal(val, &list) == nil {
				res.Pets = append(res.Pets, list.PetData...)
			}
		}
	})
	return res
}

// GoodsTypePetMark 是 dataconfig.GoodsType 的 GT_PET_MARK,即宠物伙伴标记的变更条目。
const GoodsTypePetMark = 39

// ParseCollectTagRsp 解析 ZoneUpdatePetCollectTagRsp(0x0403,伙伴标记增删改)的 body,
// 返回被改动的宠物 gid 与改动后的标记值(PetPartnerMarkType,0=无标记)。
//
// 该回包**不含 PetData**(只有 54 字节):改动只体现为
// `ret_info.goods_change_info.changes[]` 里一条 `{type=GT_PET_MARK, op=OT_SET,
// num=改后标记值, gid=pet_gid}`——加标记、换样式、移除(num=0)都走这一条,
// 故据 gid 就地改库里那一只的 partner_mark 即可(见 pipeline.applyPartnerMark)。
// 只认 op=OT_SET(标记是"设成某值"而非增减),ret_code 非 0 或无此条目则返回 false。
func ParseCollectTagRsp(body []byte) (gid uint32, mark int32, ok bool) {
	if retResult(body) != 0 {
		return 0, 0, false
	}
	chg := wire.SubMsg(wire.SubMsg(body, 1), 4) // ret_info.goods_change_info
	for _, c := range wire.Subs(chg, 1) {       // changes(GoodsChangeItem)
		if t, _ := wire.Varint(c, 1); t != GoodsTypePetMark {
			continue
		}
		if op, _ := wire.Varint(c, 2); op != OpTypeSet {
			continue
		}
		g, has := wire.Varint(c, 14) // gid(=pet_gid)
		if !has || g == 0 {
			continue
		}
		num, _ := wire.Varint(c, 3) // num:OT_SET 时即改后的标记值,移除标记为 0
		return uint32(g), int32(num), true
	}
	return 0, 0, false
}
