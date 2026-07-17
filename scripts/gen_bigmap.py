"""把解包出的大地图瓦片 PNG 拼成整图 webp,落到 internal/gamedata/data/img/bigmap/(编译期 embed)。

游戏把每张大地图切成 4x4 共 16 张 1024² 瓦片,**行主序**编号(piece = row*4 + col + 1,
见客户端 BigMapUtils.GetMapPieceIdByPos)。网页端不需要复刻客户端的分块按需加载,
拼成一张整图 + CSS 缩放平移即可,故这里直接拼合转码。

输出文件名 = scene_res_cfg_id(家园室内 30001 的底图按房屋等级分层,文件名 30001_<level>,
选层用 ZoneEnterSceneRsp.home_room_level)。世界地图保留原始 4096²(细节要经得起放大),
家园场景本身很小(边长 120~260 米),2048² 足够,省 embed 体积。

只转 names.json 的 maps 索引里出现的场景(索引出自 WORLD_MAP_BLOCK_CONF,见 gen_gamedata.py),
坐标→底图的投影参数也在那里,与底图分辨率无关(归一化)。

webp 转码是确定性的,故与 gen_images.py 一致**默认跳过已存在的 webp**(常规重跑零改动),
换 quality/尺寸时用 --force 强制重编。

前置:scripts/unpack.sh 全量解包(纹理已导出为 PNG)。运行(需 uv 管理的 pillow):
    uv run python scripts/gen_bigmap.py [PNG源目录] [--force]
"""
import json
import os
import sys

from PIL import Image

FORCE = "--force" in sys.argv[1:]
_pos = [a for a in sys.argv[1:] if not a.startswith("-")]
PARSED = os.environ.get("ROCOM_PARSED", os.path.expanduser("~/Downloads/rocom/parsed"))
SRC = _pos[0] if _pos else os.path.join(
    PARSED, "NRC", "Content", "NewRoco", "Modules", "System", "BigMap", "Raw", "Texture", "Maps")
NAMES = "internal/gamedata/data/names.json"
OUT = "internal/gamedata/data/img/bigmap"
# 分层地图(洞穴/地下层)切片源与输出:每层一张 1024² 单图(非 4x4 瓦片),直接转码不拼接。
LAYER_SRC = os.path.join(os.path.dirname(SRC), "LayerMap")
LAYER_OUT = os.path.join(OUT, "layer")
QUALITY = 82       # 插画风底图,q82 肉眼无损且 4096² 只要约 1MB(PNG 源 23MB)
SIDE = 4           # 4x4 瓦片
WORLD_PX = 4096    # 大世界底图边长(= 4x1024,原始分辨率)
HOME_PX = 2048     # 家园场景底图边长(场景小,降采样)


def stitch(tile_dir):
    """把 tile_dir 下 01..16.png 按行主序拼成整图;瓦片不全则返回 None。"""
    tiles = [os.path.join(tile_dir, f"{i:02d}.png") for i in range(1, SIDE * SIDE + 1)]
    if not all(os.path.exists(t) for t in tiles):
        return None
    with Image.open(tiles[0]) as first:
        tw, th = first.size
    canvas = Image.new("RGB", (tw * SIDE, th * SIDE))
    for i, t in enumerate(tiles):
        with Image.open(t) as im:
            canvas.paste(im.convert("RGB"), ((i % SIDE) * tw, (i // SIDE) * th))
    return canvas


def emit(name, tile_dir, px, stat):
    """拼 tile_dir 并按边长 px 输出 bigmap/<name>.webp。"""
    dst = os.path.join(OUT, name + ".webp")
    if os.path.exists(dst) and not FORCE:
        stat["kept"] += 1
        return
    canvas = stitch(tile_dir)
    if canvas is None:
        stat["miss"] += 1
        print(f"  ! 瓦片缺失,跳过: {tile_dir}")
        return
    if canvas.width != px:
        canvas = canvas.resize((px, px), Image.LANCZOS)
    os.makedirs(OUT, exist_ok=True)
    canvas.save(dst, "WEBP", quality=QUALITY, method=6)
    stat["done"] += 1
    print(f"  {name}.webp  {px}²  {os.path.getsize(dst) / 1024:.0f} KB")


def emit_layer(img_name, stat):
    """把 LayerMap/<img_name>.png 单张转码为 bigmap/layer/<img_name>.webp(保持原名,不拼接)。"""
    dst = os.path.join(LAYER_OUT, img_name + ".webp")
    if os.path.exists(dst) and not FORCE:
        stat["kept"] += 1
        return
    src = os.path.join(LAYER_SRC, img_name + ".png")
    if not os.path.exists(src):
        stat["miss"] += 1
        print(f"  ! 层切片缺失,跳过: {src}")
        return
    os.makedirs(LAYER_OUT, exist_ok=True)
    with Image.open(src) as im:
        # 层切片是透明背景上的洞穴/楼层轮廓,必须保留 alpha(存 RGBA webp);
        # 转 RGB 会把透明区填成黑色,叠在底图上显示异常。底图(Maps)不透明,故 emit 用 RGB。
        im.convert("RGBA").save(dst, "WEBP", quality=QUALITY, method=6)
    stat["done"] += 1
    print(f"  layer/{img_name}.webp  {os.path.getsize(dst) / 1024:.0f} KB")


def main():
    with open(NAMES, encoding="utf-8") as f:
        names = json.load(f)
    maps, layers = names["maps"], names.get("layers", {})

    if not os.path.isdir(SRC):
        sys.exit(f"源目录不存在: {SRC}\n请先跑 scripts/unpack.sh 解包,或传源目录/设 ROCOM_PARSED。")

    stat = {"done": 0, "kept": 0, "miss": 0}
    for res, e in sorted(maps.items()):
        px = WORLD_PX if e.get("world") else HOME_PX
        rooms = e.get("rooms")
        if rooms:  # 家园室内:每个房屋等级一张底图
            for lvl in range(1, rooms + 1):
                emit(f"{res}_{lvl}", os.path.join(SRC, res, f"RoomLevel{lvl}"), px, stat)
        else:
            emit(res, os.path.join(SRC, res), px, stat)

    # 分层地图切片(洞穴/地下层),单图转码到 bigmap/layer/。源目录缺失则跳过(层为可选增强)。
    if os.path.isdir(LAYER_SRC):
        for _id, e in sorted(layers.items(), key=lambda kv: int(kv[0])):
            emit_layer(e["img"], stat)
    elif layers:
        print(f"  (跳过分层切片:源目录不存在 {LAYER_SRC})")

    print(f"-> {OUT}  新转 {stat['done']},已存在跳过 {stat['kept']},源缺失 {stat['miss']}"
          f"（--force 可强制重编）")
    if stat["done"] + stat["kept"] == 0:
        sys.exit("未产出任何 webp:确认源目录是 PNG 导出(不是 .uasset)。")


if __name__ == "__main__":
    main()
