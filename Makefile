# 发布构建:
#   Linux:  用 zig 作交叉 CC,一键产出 amd64 + arm64 静态二进制到 dist/。
#           capture 依赖 gopacket/afpacket(cgo),无法用 CGO_ENABLED=0 直接交叉编译;
#           zig 自带各架构 musl libc 与 Linux 头,故只需装 zig,无需 arm64 库/sysroot。
#             安装(官方仓库无 zig,单文件免 root):
#               curl -L https://ziglang.org/download/0.16.0/zig-linux-x86_64-0.16.0.tar.xz | tar -xJ
#               export PATH=$$PWD/zig-x86_64-linux-0.16.0:$$PATH
#   Windows: gopacket/pcap 在 Windows 上为纯 Go(syscall 加载 wpcap.dll),CGO_ENABLED=0 即可。
#
# 前端产物已提交到 internal/server/web,无需在此 npm build。

PKG     := ./cmd/rocom-capture
BIN     := rocom-capture
DIST    := dist
# -extldflags=-Wl,-s 让 zig 外部链接器真正 strip(仅 -s -w 对 zig 不完全生效)
LDFLAGS := -s -w -extldflags=-Wl,-s

BUILD   := -trimpath
LDF     := -ldflags "$(LDFLAGS)"

.PHONY: all release release-win windows windows-dev clean

all: release

# ── Linux 静态交叉构建(需 zig) ──────────────────────────────────────
release: $(DIST)/$(BIN)-linux-amd64 $(DIST)/$(BIN)-linux-arm64
	@echo "==> 完成:" && ls -lh $(DIST)

$(DIST)/$(BIN)-linux-amd64:
	@mkdir -p $(DIST)
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
		CC="zig cc -target x86_64-linux-musl" \
		go build $(BUILD) $(LDF) -o $@ $(PKG)

$(DIST)/$(BIN)-linux-arm64:
	@mkdir -p $(DIST)
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 \
		CC="zig cc -target aarch64-linux-musl" \
		go build $(BUILD) $(LDF) -o $@ $(PKG)

# ── Windows 构建(PC 端实时抓包) ─────────────────────────────────────
# 本地开发:直接 go build(调试,无 strip)
windows-dev:
	go build -o $(BIN).exe $(PKG)
	@echo "==> $(BIN).exe ($(GOOS)/$(GOARCH))"

# 发布构建(可在 Linux 上交叉编译,无需 zig/CGO)
release-win: $(DIST)/$(BIN)-windows-amd64.exe
	@echo "==> 完成:" && ls -lh $(DIST)

$(DIST)/$(BIN)-windows-amd64.exe:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
		go build $(BUILD) $(LDF) -o $@ $(PKG)

# ── 清理 ────────────────────────────────────────────────────────────
clean:
	rm -rf $(DIST) $(BIN).exe
