package server

import (
	"net/http"

	"github.com/whoisnian/rocom-capture/internal/pet"
	"github.com/whoisnian/rocom-capture/internal/store"
)

// handleEggs 返回当前账号背包里的精灵蛋(库里存的就是背包现状,破壳/送人的行已删)。
//
// 参数:
//
//	search=          按蛋名/物种名模糊
//	sort=quality|obtained  order=asc|desc(复刻游戏内背包的两种排序,见 docs/eggs.md)
//
// 页面不分标签页:一次取回全部,孵蛋器那几颗按 hatching 标志自行分栏
// (与游戏一致——在孵的蛋不出现在背包格子里)。**排序只作用于背包那部分**:客户端也是先把
// 在孵的蛋摘掉(IsRemoveEggItem)再 table.sort,喂给排序的列表不同,同键蛋的落位就不同。
func (s *Server) handleEggs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	eggs, err := s.store.For(s.acct(r)).ListEggs(store.EggFilter{Search: q.Get("search")})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for i, e := range eggs {
		// 按当前名称库重算(旧行可能是本工具还没有异色标记/排序键时写的),
		// 顺带补上要等双亲快照才算得出的推测嗓音与奖牌。
		eggs[i] = pet.RefreshEggView(e, s.db)
	}
	// 排序键来自上面的重算,故排在其后。在孵的蛋留在最前且不参与背包排序(它们属于孵蛋器),
	// 但自己按槽位顺序排一遍——槽位是 egg_gid[] 的数组次序,与背包次序无关,见 pet.SortHatchingEggs。
	hatching, bag := []*pet.EggView{}, []*pet.EggView{}
	for _, e := range eggs {
		if e.Hatching {
			hatching = append(hatching, e)
		} else {
			bag = append(bag, e)
		}
	}
	pet.SortHatchingEggs(hatching)
	pet.SortEggs(bag, q.Get("sort"), q.Get("order") == "asc")
	eggs = append(hatching, bag...)
	writeJSON(w, map[string]any{"eggs": eggs})
}
