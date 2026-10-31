package pipeline

import (
	"log"
	"time"

	"github.com/whoisnian/rocom-capture/internal/pet"
	"github.com/whoisnian/rocom-capture/internal/store"
	"github.com/whoisnian/rocom-parse/capture"
	"github.com/whoisnian/rocom-parse/gcp"
)

// petSweep 累积一轮分页宠物列表全量下发。客户端登录/打开仓库时会连续请求 page 1..TotalPage,
// 在末页据完整快照对账:库中存在却不在快照里的 gid,即玩家在别处放生/赠送的宠物,予以清除。
// 仅当 1..TotalPage 连续到达才对账(nextPage 校验),乱序或单独请求某页不触发,避免误删。
type petSweep struct {
	gids     map[uint32]bool // 本轮各页出现过的 gid
	nextPage uint32          // 期望的下一页号(保证连续)
	valid    bool            // 自 page 1 起连续累积至今(否则不对账)
	start    time.Time       // 本轮起始(page 1 到达):对账时据此放过其后刚入库的新宠;并计全量请求耗时
	proc     time.Duration   // 累计实际解析+入库(+末页对账)耗时,排除等待客户端下一页的空档,用于暴露处理瓶颈
}

// handlePet 分发宠物相关消息(盒子/队伍/奖牌快照、宠物增减、分页列表)。
func (p *Pipeline) handlePet(m capture.Message, acc string) {
	sc := p.st.For(acc)

	// 盒子布局:登录数据(0x0102)或盒子操作回包携带完整背包 PetBackpackInfo,
	// 解出 gid->(盒子,格位) 全量快照存入 pet_box,读取宠物时 JOIN 注入位置。
	if m.Direction == gcp.S2C && pet.CarriesTeam(m.Opcode) {
		p.applyLayouts(m, sc, acc)
	}

	// petsTouched:本条消息可能改了宠物的增减/形态/嗓音,事后让野生宠物图层核对一遍
	// 「已收集的满声音进化链」(见 refreshWildOwned)。
	petsTouched := true
	switch {
	// 携带更新后完整 PetData 的回包(换牌:佩戴奖牌已变;进化:base_conf_id 换形态、
	// 等级/属性/技能刷新),就地更新宠物(同一 gid)但不产生获得事件。
	case m.Direction == gcp.S2C && (m.Opcode == pet.OpPetMedalCommonRsp || m.Opcode == pet.OpPetEvoluteRsp):
		if pd := pet.FindNewPet(m.AppBody); pd != nil {
			pp := pet.ToPet(pd, p.db)
			sc.UpsertPet(pp)
			p.srv.Hub().Broadcast("pet", acc, pp)
		}

	// 伙伴标记增删改:回包只有一条 GT_PET_MARK 的变更(不含 PetData),据其 gid 与改后的
	// 标记值就地改库里那一只。
	case m.Direction == gcp.S2C && m.Opcode == pet.OpUpdatePetCollectTagRsp:
		p.applyPartnerMark(m, sc, acc)
		petsTouched = false

	// 获得新宠物:孵蛋、战斗外捕捉、普通战斗内捕捉(经奖励通知)、花种战斗内捕捉(经玩家同步)、
	// 传说精灵战后捕捉(catch_way=5,仅经战斗结束通知下发)都把新宠物嵌在子消息里。同一宠物可能
	// 经多个 opcode 下发(普通捕捉的 BATTLE_FINISH 与 GOODS_REWARD 重复),用 isNew 去重;获得方式由 catch_way 区分。
	case m.Direction == gcp.S2C &&
		(m.Opcode == pet.OpCrackEggRsp || m.Opcode == pet.OpPetCatchRsp ||
			m.Opcode == pet.OpGoodsRewardNotify || m.Opcode == pet.OpPlayerSyncNotify ||
			m.Opcode == pet.OpBattleFinishNotify):
		p.applyNewPet(m, sc, acc)

	// 放生:服务器下行确认被放生的 gid 列表。宠物减少不计入事件,仅从库中移除并刷新前端。
	case m.Direction == gcp.S2C && m.Opcode == pet.OpPetFreeRsp:
		freed := false
		for _, gid := range pet.ParseFreeRsp(m.AppBody) {
			sc.RemovePet(gid)
			freed = true
		}
		// 通知前端刷新列表与盒子/队伍示意图(放生已清掉盒位/队位)
		if freed {
			p.srv.Hub().Broadcast("pet", acc, map[string]any{"locUpdate": true})
		}

	// 赠送:玩家开盒子手动把共同捕捉的宠物赠送给好友。宠物减少不计入事件,
	// 仅据执行回包携带的 gid 从自己库中移除并刷新前端。
	case m.Direction == gcp.S2C && m.Opcode == pet.OpTogetherCatchGiftRsp:
		if gid := pet.ParseTogetherCatchGiftRsp(m.AppBody); gid != 0 {
			sc.RemovePet(gid)
			// 刷新列表与盒子/队伍示意图(赠送已清掉盒位/队位)
			p.srv.Hub().Broadcast("pet", acc, map[string]any{"locUpdate": true})
		}

	case m.Direction == gcp.S2C && m.Opcode == pet.OpGetPetInfoByPageRsp:
		p.applyPetPage(m, sc, acc)

	default:
		petsTouched = false
	}
	if petsTouched {
		p.refreshWildOwned(m.Session, acc, m.Time)
	}
}

// applyLayouts 从登录/盒子/队伍回包提取盒子布局、队伍快照与宠物奖牌,落库并通知前端。
func (p *Pipeline) applyLayouts(m capture.Message, sc *store.Scoped, acc string) {
	updated := false
	var focusGid uint32 // 客户端刚调整位置的宠物,推给前端自动切页选中
	var focusBox int32  // 该宠物移动后所在盒子,供前端切换盒子示意图
	// 全量背包快照:整体替换盒位(占用)+ 盒子元数据(名称/数量/位置,含空盒)。
	// 登录/整理走 PetBackpackInfo;整理排列(改名/换位)的 SETTING_UP 回包是裸的
	// repeated PetBox(非 PetBackpackInfo),前者解不出时按后者再试。
	if pet.CarriesBackpack(m.Opcode) {
		entries, metas := pet.ParseBackpack(m.AppBody)
		if len(metas) == 0 && m.Opcode == pet.OpPetBoxSettingUpRsp {
			entries, metas = pet.ParseBoxSettingUp(m.AppBody)
		}
		if len(metas) > 0 {
			updated = sc.ReplacePetBoxMetas(metas) == nil || updated
		}
		if len(entries) > 0 {
			updated = sc.ReplacePetBoxes(entries) == nil || updated
		}
		// 单盒元数据增量:解锁(新增空盒→盒数+1)/设标记·改名(更新单盒名称/标记)
		var meta *pet.BoxMeta
		switch m.Opcode {
		case pet.OpPetBoxUnlockRsp:
			meta = pet.ParseBoxUnlock(m.AppBody)
		case pet.OpPetBoxSetMarkTypeRsp:
			meta = pet.ParseBoxSetMark(m.AppBody)
		}
		if meta != nil {
			updated = sc.UpsertPetBoxMeta(*meta) == nil || updated
		}
	}
	// 大世界队伍快照(登录/队伍变更/盒子操作回包常一并刷新):整体替换队位
	if teams := pet.ParseTeams(m.AppBody); len(teams) > 0 {
		updated = sc.ReplacePetTeams(teams) == nil || updated
	}
	// 盒位移动增量(box_pet_change,仅 CHANGE_PET 回包携带,其余 opcode 易误报)
	if m.Opcode == pet.OpPetBoxChangePetRsp {
		if moves := pet.ParseBoxMoves(m.AppBody); len(moves) > 0 {
			if sc.ApplyBoxMoves(moves) == nil {
				updated = true
				// 末项为被拖动(开始选中)的宠物:交换时回包按「先被挤走者、
				// 后拖动者落到目标位」排列,移到空位时也仅末项是被移动的宠物。
				last := moves[len(moves)-1]
				focusGid, focusBox = last.Gid, last.BoxID
			}
		}
	}
	// 宠物拥有的奖牌(仅登录数据携带 pet_medal_info),过滤掉非真实奖牌 id
	if m.Opcode == pet.OpLoginRsp {
		owns := pet.ParsePetMedals(m.AppBody)
		valid := owns[:0]
		for _, o := range owns {
			if _, ok := p.db.Medal(o.MedalID); ok {
				valid = append(valid, o)
			}
		}
		if len(valid) > 0 {
			updated = sc.ReplacePetMedals(valid) == nil || updated
		}
	}
	if updated {
		payload := map[string]any{"locUpdate": true}
		if focusGid != 0 {
			payload["focusGid"] = focusGid
			payload["focusBox"] = focusBox
		}
		p.srv.Hub().Broadcast("pet", acc, payload)
	}
}

// applyPartnerMark 处理伙伴标记的增删改(0x0403):回包不含 PetData,只在
// goods_change_info 里给出宠物 gid 与改后的标记值,故取库里那一只改掉标记再广播。
func (p *Pipeline) applyPartnerMark(m capture.Message, sc *store.Scoped, acc string) {
	gid, mark, ok := pet.ParseCollectTagRsp(m.AppBody)
	if !ok {
		return
	}
	pp, err := sc.SetPetPartnerMark(gid, p.db.PartnerMark(mark), p.db.PartnerMarkIcon(mark))
	if err != nil || pp == nil { // 库中还没有这只(未同步过)时无从更新,等下次全量同步
		return
	}
	p.srv.Hub().Broadcast("pet", acc, pp)
}

// applyNewPet 从捕捉/孵蛋类回包提取新宠物入库,并产生获得事件。
func (p *Pipeline) applyNewPet(m capture.Message, sc *store.Scoped, acc string) {
	pd := pet.FindNewPet(m.AppBody)
	if pd == nil {
		return
	}
	// PLAYER_SYNC_NOTIFY/BATTLE_FINISH_NOTIFY 是通用通知通道(理论上可能携带对手/旧快照),
	// 额外用 add_time 时近性(相对本包时间)守卫,仅认刚捕获的宠物。
	if (m.Opcode == pet.OpPlayerSyncNotify || m.Opcode == pet.OpBattleFinishNotify) &&
		int64(pd.GetAddTime()) < m.Time.Unix()-grace {
		return
	}
	pp := pet.ToPet(pd, p.db)
	isNew, _ := sc.UpsertPet(pp)
	// 获得新宠物的回包(战斗外捕捉/孵蛋等)常同时携带该宠物的盒位放置(box_pet_change);
	// 据此落库盒位,否则新宠物在盒子示意图上缺位(仅列表末尾可见,位置标「待同步」)。
	// 严格按本次新宠 gid 过滤:回包体内只有该宠物一条落位,借此排除 PetData 子结构被误解析。
	var placed []pet.BoxEntry
	for _, mv := range pet.ParseBoxMoves(m.AppBody) {
		if mv.Gid == pp.Gid {
			placed = append(placed, mv)
		}
	}
	if len(placed) > 0 {
		sc.ApplyBoxMoves(placed)
	}
	p.srv.Hub().Broadcast("pet", acc, pp)
	if isNew {
		ev := &store.Event{Time: m.Time.Unix(), SubKind: catchWayName(pd, acc), Gid: pp.Gid, Pet: pp}
		if sc.AddEvent(ev) == nil {
			logEvent(acc, ev)
			p.srv.Hub().Broadcast("event", acc, ev)
		}
	}
}

// applyPetPage 处理一页分页宠物列表:逐只入库、必要时产生获得事件,并做末页对账(见 petSweep)。
func (p *Pipeline) applyPetPage(m capture.Message, sc *store.Scoped, acc string) {
	pageT0 := time.Now() // 本页处理起点(解析+入库),累计入 sw.proc 以衡量实际处理耗时
	res := pet.ParsePetListRsp(m.AppBody)
	// 本页页号取 req_page(响应回显所请求页,登录时依次为 1..TotalPage);
	// page_num 实为每页容量(实测恒为 50),不是页序,不能用作累积接续判据。
	page := res.ReqPage
	as := p.acct(acc)
	sw := as.sweep
	if sw == nil || !sw.valid || page != sw.nextPage { // 无法接续上一页则从本页重开(仅 page 1 起算有效)
		sw = &petSweep{gids: map[uint32]bool{}, valid: page <= 1, start: pageT0}
		as.sweep = sw
	}
	pets := make([]*pet.Pet, 0, len(res.Pets))
	for _, pd := range res.Pets {
		pp := pet.ToPet(pd, p.db)
		sw.gids[pp.Gid] = true // 无论 upsert 成败都视为"仍拥有",避免对账误删
		pets = append(pets, pp)
	}
	// 整页一个事务批量入库(见 store.UpsertPets):把每只一次自动提交压到每页一次。
	// 失败则整页跳过(此前已登记 sw.gids,不会误删),下一页/下轮同步会重试。
	newGids, err := sc.UpsertPets(pets)
	if err != nil {
		log.Printf("用户 %s 第 %d 页宠物入库失败: %v", acc, page, err)
	} else {
		for i, pd := range res.Pets {
			pp := pets[i]
			p.srv.Hub().Broadcast("pet", acc, pp)
			// newGids 是「本页此前不存在」的集合;消费掉首次出现,同页重复 gid 不再计新增(与逐只 upsert 等价)。
			if newGids[pp.Gid] {
				delete(newGids, pp.Gid)
				if int64(pd.GetAddTime()) >= p.startTS {
					ev := &store.Event{
						Time:    int64(pd.GetAddTime()),
						SubKind: catchWayName(pd, acc),
						Gid:     pp.Gid,
						Pet:     pp,
					}
					if sc.AddEvent(ev) == nil {
						logEvent(acc, ev)
						p.srv.Hub().Broadcast("event", acc, ev)
					}
				}
			}
		}
	}
	sw.nextPage = page + 1
	sw.proc += time.Since(pageT0) // 累计本页实际处理耗时(不含等待客户端下一页的空档)
	// 末页:先据完整快照清除库中已不存在(玩家在别处放生/赠送)的宠物(否则残留为"位置待同步"),
	// 再汇总本轮:请求耗时=首页到末页的墙钟跨度(含客户端分页节奏),解析耗时=纯处理累计。
	// 二者背离即暴露问题:处理耗时接近/超过请求跨度,说明处理速度赶不上抓包到达而积压。
	if sw.valid && res.TotalPage > 0 && page >= res.TotalPage {
		pruneT0 := time.Now()
		if stale, err := sc.PruneMissingPets(sw.gids, sw.start.Unix()); err == nil && len(stale) > 0 {
			log.Printf("用户 %s 对账清除 %d 只已不在仓库的宠物", acc, len(stale))
			p.srv.Hub().Broadcast("pet", acc, map[string]any{"locUpdate": true})
		}
		sw.proc += time.Since(pruneT0)
		log.Printf("用户 %s 宠物同步完成: %d 只 %d 页, 请求耗时 %v, 解析耗时 %v",
			acc, len(sw.gids), res.TotalPage, time.Since(sw.start), sw.proc)
		as.sweep = nil // 本轮结束,防止后续单独请求某页复用旧累积
	}
}

// logEvent 打印一条获得宠物事件日志。
func logEvent(acc string, ev *store.Event) {
	sp := "?"
	if ev.Pet != nil && ev.Pet.Species != "" {
		sp = ev.Pet.Species
	}
	log.Printf("用户 %s 获得宠物 %s(gid=%d) [%s]", acc, sp, ev.Gid, ev.SubKind)
}
