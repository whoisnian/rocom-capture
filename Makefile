# 发布构建:用 zig 作交叉 CC,一键产出 amd64 + arm64 静态二进制到 dist/。
#
# 背景:capture 依赖 gopacket/afpacket(cgo),无法用 CGO_ENABLED=0 直接交叉编译;
# zig 自带各架构 musl libc 与 Linux 头,故只需装 zig,无需 arm64 库/sysroot。
#   安装(官方仓库无 zig,单文件免 root):
#     curl -L https://ziglang.org/download/0.16.0/zig-linux-x86_64-0.16.0.tar.xz | tar -xJ
#     export PATH=$$PWD/zig-x86_64-linux-0.16.0:$$PATH
# 前端产物已提交到 internal/server/web,无需在此 npm build。

PKG     := ./cmd/rocom-capture
BIN     := rocom-capture
DIST    := dist
# -extldflags=-Wl,-s 让 zig 外部链接器真正 strip(仅 -s -w 对 zig 不完全生效)
LDFLAGS := -s -w -extldflags=-Wl,-s

.PHONY: all release clean

all: release

release: $(DIST)/$(BIN)-linux-amd64 $(DIST)/$(BIN)-linux-arm64
	@echo "==> 完成:" && ls -lh $(DIST)

$(DIST)/$(BIN)-linux-amd64:
	@mkdir -p $(DIST)
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
		CC="zig cc -target x86_64-linux-musl" \
		go build -trimpath -ldflags "$(LDFLAGS)" -o $@ $(PKG)

$(DIST)/$(BIN)-linux-arm64:
	@mkdir -p $(DIST)
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 \
		CC="zig cc -target aarch64-linux-musl" \
		go build -trimpath -ldflags "$(LDFLAGS)" -o $@ $(PKG)

clean:
	rm -rf $(DIST)
