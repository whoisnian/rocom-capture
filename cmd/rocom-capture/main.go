package main

import (
	"crypto/tls"
	"flag"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/whoisnian/rocom-capture/internal/gamedata"
	"github.com/whoisnian/rocom-capture/internal/livecap"
	"github.com/whoisnian/rocom-capture/internal/pipeline"
	"github.com/whoisnian/rocom-capture/internal/server"
	"github.com/whoisnian/rocom-capture/internal/store"
	"github.com/whoisnian/rocom-parse/capture"
)

func main() {
	pcapPath := flag.String("pcap", "", "离线 pcap 文件路径(回放模式;可再跟多个,轮转出来的多份会当成一条流连读)")
	iface := flag.String("iface", "", "实时抓包网卡名")
	ignoreIPs := flag.String("ignore-ip", "", "额外忽略的 IP(逗号分隔;两端命中即丢包)。实时抓包已自动忽略网卡自身 IP,此项用于离线回放或多网关等场景")
	port := flag.Int("port", 8195, "游戏服务器端口")
	addr := flag.String("addr", ":4939", "Web 服务监听地址")
	dbPath := flag.String("db", "rocom.db", "SQLite 数据库路径")
	useTLS := flag.Bool("tls", false, "启用 HTTPS(自签证书;手机经局域网访问以满足屏幕常亮等需 secure context 的 API)")
	certPath := flag.String("cert", "rocom-cert.pem", "TLS 证书路径(-tls 时不存在则自动生成自签证书)")
	keyPath := flag.String("key", "rocom-key.pem", "TLS 私钥路径(-tls 时不存在则自动生成)")
	petsURL := flag.String("pets-url", "", "本机桌宠 rocom-pets 的本地监听(如 http://127.0.0.1:47831);由看页面的浏览器去连,通了则炫彩色卡唤起桌宠预览。不设则不探测")
	petsToken := flag.String("pets-token", "", "rocom-pets 本地接口的鉴权令牌(桌宠「常用配置 › 本地接口」里复制);设了 -pets-url 就必须给")
	flag.Parse()

	db, err := gamedata.Load()
	if err != nil {
		log.Fatalf("加载名称库失败: %v", err)
	}
	st, err := store.New(*dbPath, db)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	pets := strings.TrimRight(*petsURL, "/")
	if u, err := url.Parse(pets); pets != "" && (err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "") {
		log.Fatalf("-pets-url 要是 http(s)://host:port 形式: %q", *petsURL)
	}
	token := strings.TrimSpace(*petsToken)
	if pets != "" && token == "" {
		log.Fatalf("-pets-url 需要同时给 -pets-token:rocom-pets 的本地接口一律要鉴权令牌")
	}
	if pets == "" {
		token = ""
	}
	srv := server.New(st, server.NewHub(), db, server.Options{PetsURL: pets, PetsToken: token})
	eng := capture.NewEngine(*port)
	eng.Keys = st // 会话密钥持久化:抓包服务重启后继续解密仍存活的连接
	for s := range strings.SplitSeq(*ignoreIPs, ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		ip, err := netip.ParseAddr(s)
		if err != nil {
			log.Fatalf("-ignore-ip 无效地址 %q: %v", s, err)
		}
		eng.AddSkipIP(ip)
	}

	pl := pipeline.New(st, db, srv)
	go pl.Run(eng)
	go serveWeb(*addr, srv.Handler(), *useTLS, *certPath, *keyPath)

	switch {
	case *pcapPath != "":
		// 通配展开的其余文件走位置参数:-pcap 'pcap/rocom-2026*.pcap00' 把第一份给 -pcap、
		// 其余留在 flag.Args()。轮转出来的多份必须当成一条流连读 —— 会话密钥只在第一份的
		// 0x1002 ACK 里明文下发一次,单独喂后面任何一份都一条消息都解不出来。
		paths := append([]string{*pcapPath}, flag.Args()...)
		log.Printf("离线回放: %s", strings.Join(paths, " "))
		if err := eng.RunOfflineFiles(paths...); err != nil {
			log.Fatalf("回放失败: %v", err)
		}
		// 涂地是攒批落盘的(见 server/paint.go),而回放几秒就跑完一整份 pcap、一次都攒不到,
		// 故这里补一次落盘,否则回放出来的覆盖图重启就没了。
		srv.FlushPaint()
		log.Printf("回放完成，%d 个账号共宠物 %d 只。Web 服务保持运行(Ctrl-C 退出)", pl.AccountCount(), pl.PetTotal())
		if d := eng.NoKeyDropped(); d > 0 {
			log.Printf("提示: %d 个数据包因尚无会话密钥被丢弃(抓包晚于密钥协商时属正常)", d)
		}
		if d := eng.BadKeyDropped(); d > 0 {
			log.Printf("提示: %d 个数据包因密钥错误(明文校验失败)被丢弃(缓存密钥失效时会出现)", d)
		}
		select {}
	case *iface != "":
		log.Printf("实时抓包: 网卡=%s 端口=%d", *iface, *port)
		if err := livecap.Run(eng, *iface); err != nil {
			log.Fatalf("抓包失败(需 root): %v", err)
		}
	default:
		log.Println("用法: -pcap <文件> 或 -iface <网卡>")
	}
}

// serveWeb 启动 Web 服务(-tls 时用自签证书起 HTTPS,证书不存在则生成,见 tls.go)。
func serveWeb(addr string, h http.Handler, useTLS bool, certPath, keyPath string) {
	if useTLS {
		cert, err := loadOrCreateCert(certPath, keyPath)
		if err != nil {
			log.Fatalf("准备 TLS 证书失败: %v", err)
		}
		hs := &http.Server{
			Addr:      addr,
			Handler:   h,
			TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
		}
		log.Printf("Web 界面: https://localhost%s (自签证书,浏览器首次访问需手动信任)", addr)
		if err := hs.ListenAndServeTLS("", ""); err != nil {
			log.Fatalf("HTTPS 服务失败: %v", err)
		}
		return
	}
	log.Printf("Web 界面: http://localhost%s", addr)
	if err := http.ListenAndServe(addr, h); err != nil {
		log.Fatalf("HTTP 服务失败: %v", err)
	}
}
