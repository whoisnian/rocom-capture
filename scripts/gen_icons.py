"""统一 UI 图标生成器:裁切/转码解包图标为 webp,落到 internal/gamedata/data/img/<组>/。

两种资源机制:
  - 图集精灵(Paper2D PaperSprite):本身不含像素,从图集(Texture2D)按 UV 裁一块。UV 取自 FModel
    的 Save Properties(.json),图集取自 Save Texture(PNG)。用于 filter / blood / static。
  - 整张贴图(Texture2D):Save Texture 出的 PNG 直接转码(同宠物头像),无需裁切。用于 medal。

**命名保持原始解包文件名**:webp 文件名即游戏资产名(如 `ui_icon_species_04_png.webp` /
`img_huo_png.webp` / `img_MedalIcon_Huge.webp`),按 basename **去重**(多个枚举值/id 复用同一资产
时只存一份)。语义映射(enum/id → 文件名)由 gen_gamedata.py 从 vendored 配置写进 names.json 的
`filter_icons`/`blood_icons`/`medal_icons`;本脚本只产图,不涉及 enum/id。

各组数据源:
  - filter:   PET_FILTER_CONF.filter_icon(系别/六维/搭档标记的精灵)  → img/filter/
  - blood:    PET_BLOOD_CONF.icon(24 血脉主图标精灵)                 → img/blood/
  - static:   下方 STATIC 清单(人工挑选的杂项精灵)                   → img/static/
  - worldmap: 下方 WORLDMAP 清单(人工挑选的大地图 POI 精灵)          → img/worldmap/
  - medal:    MEDAL_CONF.icon(BagItem 奖牌小图,整张贴图)            → img/medal/

webp 转码确定性(同 libwebp 下同源字节一致),默认跳过已存在;--force 强制重编(见 gen_images.py)。
前置(FModel,导到下载根 SRC):
  - Save Properties(.json)+ Save Texture(图集 PNG):Common/Icon/Species/Frames、
    PetUI/Raw/Atlas/PetUI/Frames、Common/CommonStatic/Frames、Common/Icon/XueMai/Frames、
    System/BigMap/Raw/Atlas/WorldMapNpc/Frames 及各 Textures/。
  - Save Texture(PNG):Common/Icon/BagItem(奖牌小图)。
运行(需 uv 管理的 pillow):
    uv run python scripts/gen_icons.py [下载根目录] [--force]
"""
import os
import re
import sys

from PIL import Image

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import decode_bin  # vendored 解码器(nrc/bin 的 .bytes)

FORCE = "--force" in sys.argv[1:]
_pos = [a for a in sys.argv[1:] if not a.startswith("-")]
SRC = _pos[0] if _pos else os.environ.get("IMG_SRC", os.path.expanduser("~/Downloads/NRC"))
BIN_DIR = os.environ.get("NRC_BIN_DIR", "nrc/bin")
OUT_ROOT = os.environ.get("IMG_OUT", "internal/gamedata/data/img")
QUALITY = 90  # 与 gen_images 一致;UI 图标够用且体积小

# static 组:人工挑选的杂项精灵 {sprite 名(即原始 basename): 中文说明}。均在 Common/CommonStatic 图集。
STATIC = {
    "img_collect_png":     "伙伴标记外框",
    "img_emeng_png":       "污染图标",
    "img_yisetubian_png":  "异色图标",
    "img_bolitubian_png":  "炫彩图标",
    "img_yisexuancai_png": "异色炫彩图标",
}

# worldmap 组:人工挑选的大地图 POI 精灵,均在 System/BigMap/Raw/Atlas/WorldMapNpc 图集。
# 该图集的 Frames 下混着两类资产:数字名(00102 等)是独立 Texture2D(NPC 头像),
# 语义名的才是 PaperSprite;这里只挑后者,故与 static 同走 crop_sprite。
# 眠枭的两张「之星」资产名把拼音写反了(mianxiao / miaoxian),同一图两色,非笔误勿改。
WORLDMAP = {
    "Alchemy_png":                          "炼金釜",
    "Interestplace_Campinglan_png":         "魔力之源",
    "Interestplace_Underground_Unlock_png": "守护地",
    "img_MapIcon_Ore_png":                  "矿石标记",
    "img_MapIcon_PetPlant_png":             "植物标记",
    "img_dijimianxiao_weifangman_png":      "小型眠枭庇护所",
    "img_gaojimianxiao_weifangman_png":     "大型眠枭庇护所",
    "img_mianxiaozhixing_huang_png":        "黄色眠枭之星",
    "img_miaoxianzhixing_lan_png":          "蓝色眠枭之星",
    "img_miaoxianzhixing_zi_png":           "紫色眠枭之星",
    "owl_worldmap_fruit_A1_png":            "蓝色精灵果实",
    "owl_worldmap_fruit_A2_png":            "黄色精灵果实",
    "owl_worldmap_fruit_A3_png":            "紫色精灵果实",
}


# ── 基础设施 ──────────────────────────────────────────────

def load_rows(table: str) -> dict:
    loc = os.path.join(BIN_DIR, "BinLocalize", "dev_CN", table + ".bytes")
    return decode_bin.decode_file(
        os.path.join(BIN_DIR, "BinDataCompressed", table + ".bytes"),
        schema_path=os.path.join(BIN_DIR, "BinConf", table + ".non"),
        loc_path=loc if os.path.exists(loc) else None,
    )["RocoDataRows"]


def game_to_src(ref: str) -> str:
    """/Game/A/B/x.x 或裸 basename -> <SRC>/Content/A/B/x(不含扩展名;裸名则 <SRC>/Content/x)。"""
    m = re.search(r"(?:/Game/|/Content/|^Content/)(.+)", ref)
    rel = m.group(1) if m else ref
    rel = re.sub(r"\.[^./]*$", "", rel)  # 去掉最后的 .Name 或 .0 序号
    return os.path.join(SRC, "Content", rel)


def basename(ref: str) -> str:
    """资产引用 -> 原始文件名(basename,不含扩展名/序号)。"""
    return os.path.basename(game_to_src(ref))


_by_base: dict[str, dict[str, str]] = {}


def find(ref: str, ext: str) -> str:
    """定位解包文件:先按引用完整路径,再按 basename 回退(同名精灵散在多处/只导出等价一份时)。"""
    p = game_to_src(ref) + ext
    if os.path.exists(p):
        return p
    if ext not in _by_base:
        idx = {}
        for root, _, files in os.walk(SRC):
            for f in files:
                if f.endswith(ext):
                    idx.setdefault(f, os.path.join(root, f))
        _by_base[ext] = idx
    return _by_base[ext].get(os.path.basename(p), "")


def crop_sprite(ref: str, dst: str) -> str | None:
    """PaperSprite:读 Save Properties JSON 的 UV,从图集 PNG 裁切并写 webp。返回失败原因或 None。"""
    import json
    jf = find(ref, ".json")
    if not jf:
        return "缺 JSON"
    with open(jf, encoding="utf-8") as f:
        sp = next((o for o in json.load(f) if o.get("Type") == "PaperSprite"), None)
    if sp is None:
        return "非 PaperSprite"
    P = sp["Properties"]
    uv = P.get("BakedSourceUV") or {"X": 0, "Y": 0}  # 零值被 FModel 省略,默认 (0,0)
    dim = P["BakedSourceDimension"]
    png = find(P["BakedSourceTexture"]["ObjectPath"], ".png")
    if not png:
        return "缺图集"
    x, y, w, h = int(uv["X"]), int(uv["Y"]), int(dim["X"]), int(dim["Y"])
    Image.open(png).convert("RGBA").crop((x, y, x + w, y + h)).save(
        dst, "WEBP", quality=QUALITY, method=4)
    return None


def copy_texture(ref: str, dst: str) -> str | None:
    """Texture2D:Save Texture 出的整张 PNG 直接转码。返回失败原因或 None。"""
    png = find(ref, ".png")
    if not png:
        return "缺 PNG"
    Image.open(png).convert("RGBA").save(dst, "WEBP", quality=QUALITY, method=4)
    return None


# ── 各组:枚举图标资产引用 ─────────────────────────────────

# filter 组只收 names.json filter_icons 实际输出的三组(与 gen_gamedata 同一白名单):
# 2026-07 版 PET_FILTER_CONF 新增 PetBloodType 组(游戏内血脉筛选),其图标与 PET_BLOOD_CONF
# 的血脉主图标同为 XueMai 图集精灵,照单全收会往 img/filter 重复转码 21 张 img/blood 已有的图。
FILTER_ENUMS = {"SkillDamType", "AttributeType", "PetPartnerMarkType"}


def icon_refs(table: str, field: str, enums: set | None = None):
    for r in load_rows(table).values():
        if enums and r.get("filter_enum_name") not in enums:
            continue
        ic = r.get(field)
        if isinstance(ic, str) and ic:
            m = re.search(r"/Game/[^']+", ic)
            if m:
                yield m.group(0)


def gen_group(group: str, refs, writer) -> int:
    """按 basename 去重,逐个 writer(ref, dst) 产出 <group>/<原名>.webp。"""
    out = os.path.join(OUT_ROOT, group)
    os.makedirs(out, exist_ok=True)
    uniq = {}
    for ref in refs:
        uniq.setdefault(basename(ref), ref)  # 同名只处理一次
    done = kept = miss = 0
    for name, ref in sorted(uniq.items()):
        dst = os.path.join(out, name + ".webp")
        if os.path.exists(dst) and not FORCE:
            kept += 1
            continue
        why = writer(ref, dst)
        if why:
            print(f"  {group} {name}: {why}")
            miss += 1
        else:
            done += 1
    print(f"  {group:7} 新转 {done:3}  已存在跳过 {kept:3}  源缺失 {miss:3}  (唯一 {len(uniq)})")
    return done + kept


def main():
    if not os.path.isdir(SRC):
        sys.exit(f"源目录不存在: {SRC}(可传下载根目录或用 IMG_SRC 指定)")
    total = 0
    total += gen_group("filter", icon_refs("PET_FILTER_CONF", "filter_icon", FILTER_ENUMS), crop_sprite)
    total += gen_group("blood", icon_refs("PET_BLOOD_CONF", "icon"), crop_sprite)
    total += gen_group("static", list(STATIC), crop_sprite)
    total += gen_group("worldmap", list(WORLDMAP), crop_sprite)
    total += gen_group("medal", icon_refs("MEDAL_CONF", "icon"), copy_texture)
    print(f"-> {OUT_ROOT}(--force 可强制重编)")
    if total == 0:
        sys.exit(f"未产出任何 webp:确认已在 FModel 导出到 {SRC} 对应目录。")


if __name__ == "__main__":
    main()
