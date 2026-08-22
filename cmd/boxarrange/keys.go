package main

import (
	"database/sql"
	"time"

	"github.com/whoisnian/rocom-capture/internal/store"
)

// roKeys 从 rocom-capture 的库里**只读地**借会话密钥,实现 capture.KeyStore。
//
// 会话密钥只在 0x1002 ACK 里明文下发一次,自己从中途开抓是拿不到的 —— 一条已经建立的连接
// 在我们眼里就是一坨解不开的密文。而网关上 rocom-capture 一直在跑、也一直在把密钥落盘,
// 借它一份就能随时接上正在进行的连接:这样长时间嗅探时中途重启、或者游戏已经开着才想起来
// 开自检,都不用为了拿密钥去重连一次(重连会让包序号归零,那正是要避免的)。
//
// 只写不回:落盘是 rocom-capture 的事,两个进程往同一张表写没必要,也容易互相踩。
type roKeys struct{ db *sql.DB }

func (r roKeys) LoadKey(connID string) ([]byte, bool) {
	var key []byte
	err := r.db.QueryRow(`SELECT key FROM sessions WHERE conn_id=? AND updated_at>=?`,
		connID, time.Now().Add(-store.SessionTTL).Unix()).Scan(&key)
	if err != nil || len(key) == 0 {
		return nil, false
	}
	return key, true
}

func (roKeys) SaveKey(string, []byte) {}

// openKeyStore 打开只读密钥源。打不开不是错误:自己从游戏建立连接之前开始抓同样能拿到密钥,
// 只是没法中途接上而已,所以只提示不退出。
func openKeyStore(path string) (roKeys, bool) {
	db, err := openReadOnly(path)
	if err != nil {
		return roKeys{}, false
	}
	if db.QueryRow(`SELECT 1 FROM sessions LIMIT 1`).Scan(new(int)) != nil {
		// 表不在、或一条会话都没有:借不到东西,别占着连接
		if err := db.Ping(); err != nil {
			db.Close()
			return roKeys{}, false
		}
	}
	return roKeys{db: db}, true
}
