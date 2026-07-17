# 参考资料

与本项目相关的工具与开源项目(数据来源本身见 [data.md](data.md):
原始 pak → `~/Downloads/rocom/Paks/`,解包产物 → `~/Downloads/rocom/parsed/`)。

## 解包

| 项目 | 说明 |
| --- | --- |
| [CUE4Parse](https://github.com/FabianFG/CUE4Parse) | `scripts/unpack` 的解析引擎,内置 `GAME_RocoKingdomWorld` 游戏支持(自定义 AES 变体/Bin/luac,无需 usmap)。构建需本地克隆,位置用环境变量 `CUE4PARSE_DIR` 指定(默认 `~/Git/gh/CUE4Parse`) |
| [FModel](https://github.com/4sval/FModel) | Windows GUI 解包器,手动导出备用路径,可与 `scripts/unpack.sh` 互为校验(该游戏条目 UeVersion=68812827 即 `GAME_RocoKingdomWorld`) |
| [MIXUULS/Roco-Kingdom-World-Data](https://github.com/MIXUULS/Roco-Kingdom-World-Data) | `scripts/decode_bin.py`(解 Bin 目录 `.bytes` 配置)的原始出处,已 vendored |

## 协议与同类项目

| 项目 | 说明 |
| --- | --- |
| [lsj9383/blog](https://github.com/lsj9383/blog) | tsf4g 通信协议说明 |
| [h3110w0r1d-y/rocom-helper](https://github.com/h3110w0r1d-y/rocom-helper) | 闭源洛克王国世界助手,本项目受其启发 |
| [yuzeis/Roco-Kingdom-Protocol-Parser](https://github.com/yuzeis/Roco-Kingdom-Protocol-Parser) | 开源洛克王国协议解析器,简称 RKPP |

## 已弃用(仅留作历史对照)

| 项目 | 说明 |
| --- | --- |
| [phainia/pak-public-kit](https://github.com/phainia/pak-public-kit) | 曾为名称表源;其 PET_CONF 本地化整体错位(见 data.md 第 5 节),已被自有解包替代 |
| [kikozz/Roco-Kingdom-World-Data-2026-05-21](https://github.com/kikozz/Roco-Kingdom-World-Data-2026-05-21) | 曾为 `.proto` 源;现字段号直接取自解包描述符 all.pb,其 Bin JSON 可作名称表三方对照 |
