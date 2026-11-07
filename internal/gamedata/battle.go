package gamedata

// BattleCfg 是 BATTLE_CONF 里赛季奇遇战斗的一条:Name=战斗名(即对手宠物名),
// PetConf=敌方宠物 conf_id。0x1316 的 init_info.battle_cfg_id 在进战斗时就确定宠物种类,
// 捕获事件页的「战斗信息」行据此在打破前显示种类。
type BattleCfg struct {
	Name    string `json:"n"`
	PetConf uint32 `json:"p"`
}

// BattleCfg 返回某 battle_cfg_id 的赛季奇遇战斗配置;ok=false 表示不是赛季奇遇战斗。
func (db *DB) BattleCfg(id uint32) (BattleCfg, bool) {
	v, ok := db.battleCfg[id]
	return v, ok
}
