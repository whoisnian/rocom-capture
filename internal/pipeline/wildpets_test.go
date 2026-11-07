package pipeline

import (
	"testing"
	"time"

	"github.com/whoisnian/rocom-capture/internal/scene"
)

// 棋子按 NPC_CONF id + npc_bonus_info 认:只有 29401 且带龙龙骰(chess_drop_dice)的
// 才是大龙棋士;小龙棋士(不带骰)刷得更多,不标。
func TestWildMatchChess(t *testing.T) {
	p := &Pipeline{} // chess 分支在查 gamedata 之前返回,不需要 db
	big := scene.NpcActor{CfgID: chessWorldNpc, ChessEmotionID: 105, ChessDropDice: true}
	if !p.wildMatch(big) {
		t.Error("大龙棋士应命中野生宠物图层")
	}
	for _, c := range []struct {
		name string
		a    scene.NpcActor
	}{
		{"小龙棋士", scene.NpcActor{CfgID: chessWorldNpc, ChessEmotionID: 105}},
		{"缺 chess_emotion_id", scene.NpcActor{CfgID: chessWorldNpc, ChessDropDice: true}},
		{"棋子-访客(29402)", scene.NpcActor{CfgID: chessWorldNpc + 1, ChessEmotionID: 105, ChessDropDice: true}},
	} {
		if p.wildMatch(c.a) {
			t.Errorf("%s 不该命中", c.name)
		}
	}
}

// 大龙棋士存活 4 小时(2920016 的 survive_time),到点由 pushWilds 兜底清掉;
// 出生时刻缺失时以首次见到为准。
func TestChessExpired(t *testing.T) {
	born := time.Unix(1794054516, 0)
	w := &wildPet{bornAt: born, seenAt: born.Add(time.Hour)}
	if chessExpired(w, born.Add(chessSurviveTTL-time.Second)) {
		t.Error("差 1 秒到 4 小时不该过期")
	}
	if !chessExpired(w, born.Add(chessSurviveTTL)) {
		t.Error("满 4 小时应过期")
	}

	noBorn := &wildPet{seenAt: born}
	if chessExpired(noBorn, born.Add(chessSurviveTTL-time.Second)) {
		t.Error("缺出生时刻:不足 4 小时不该过期")
	}
	if !chessExpired(noBorn, born.Add(chessSurviveTTL)) {
		t.Error("缺出生时刻:首见满 4 小时应过期")
	}
}
