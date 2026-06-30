"""提取宠物展示需要的 id->中文名 精简表，输出到 internal/gamedata/data/names.json。

数据全部来自随仓库提交的游戏解包产物(FModel 从 Windows 客户端提取),不依赖任何外部仓库:
- 名称表: nrc/bin/ 下游戏自有二进制配置(`.bytes` 数据 + `.non` schema + dev_CN 本地化),
          用 vendored 的 scripts/decode_bin.py 解码(参考 CUE4Parse FRocoBinData.cs):
  - 种类:   MONSTER_CONF + PET_CONF      id -> name
  - 性格:   AUDIO_NATURE_CONF            nature_id -> name
  - 奖牌:   MEDAL_CONF                   id -> {name, desc}
  - 系别/天分/标记/特长: PET_FILTER_CONF 的 filter_enum_value -> filter_desc / PET_TALENT_CONF
- 枚举/opcode: 游戏描述符 nrc/all.pb(ZoneSvrCmd、SkillDamType 等),经 scripts/pbdesc.py 读取。

opcode/枚举与字段号(internal/pb)同出 nrc/all.pb(见 gen_proto.py),与 internal/pb 天然同版本。
运行(需 uv 管理的 protobuf 依赖):  uv run python scripts/gen_gamedata.py
更新到新版本游戏:用 FModel 重新提取覆盖 nrc/bin/ 与 nrc/all.pb 再跑本脚本(原因见 docs/data.md)。
"""
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import decode_bin  # vendored 解码器(scripts/decode_bin.py,纯标准库)
import pbdesc      # 读 all.pb 描述符(依赖 protobuf,uv 管理)

# 名称表:vendored 的游戏二进制配置(nrc/bin);opcode/枚举:游戏描述符 nrc/all.pb。
BIN_DIR = os.environ.get("NRC_BIN_DIR", "nrc/bin")
ALL_PB = os.path.join(os.environ.get("NRC_PB_DIR", "nrc"), "all.pb")
OUT = "internal/gamedata/data"

_FDS = pbdesc.load(ALL_PB)  # 描述符只读一次,enum_dim/opcodes 共用


def rows(table):
    """解码 nrc/bin 下一张表(.bytes + .non + dev_CN 本地化)为 {id字符串: 行}。"""
    base = table[:-5] if table.endswith(".json") else table
    loc = os.path.join(BIN_DIR, "BinLocalize", "dev_CN", base + ".bytes")
    return decode_bin.decode_file(
        os.path.join(BIN_DIR, "BinDataCompressed", base + ".bytes"),
        schema_path=os.path.join(BIN_DIR, "BinConf", base + ".non"),
        loc_path=loc if os.path.exists(loc) else None,
    )["RocoDataRows"]


# PET_FILTER 按 enum 名分组: {enum_name: {value_name: desc}}
filter_groups = {}
for r in rows("PET_FILTER_CONF.json").values():
    en = r.get("filter_enum_name")
    if en:
        filter_groups.setdefault(en, {})[r.get("filter_enum_value")] = r.get("filter_desc")


def enum_dim(enum_name):
    """组合 all.pb 枚举(名->int)与 filter(名->中文)得到 {int: 中文}。"""
    name2int = pbdesc.enum(_FDS, enum_name)
    out = {}
    for vname, desc in filter_groups.get(enum_name, {}).items():
        if vname in name2int:
            out[str(name2int[vname])] = desc
    return out


# 种类名：常规宠物在 MONSTER_CONF，彩蛋/特殊宠物在 PET_CONF，两表 id 不重叠，合并取用。
species = {k: v["name"] for k, v in rows("MONSTER_CONF.json").items() if v.get("name")}
species.update({k: v["name"] for k, v in rows("PET_CONF.json").items() if v.get("name")})


# ---- 宠物图片索引 ----
# 链路: conf_id(MONSTER/PET 行) --base_id--> PETBASE 基础形态 --> 头像/全身图文件名。
#   头像取自 MODEL_CONF(经 PETBASE.model_conf 关联)的 icon/big_icon;全身图取自 PETBASE.JL_res。
#   文件名【不能用 id 拼】(728 个形态共用他人头像,如 3228 用 3012),故存表;
#   Go 侧按固定目录(HeadIcon/BigHeadIcon256/Pet1024/Pet256)拼出 .webp 路径。
def texkey(ref):
    """从 UE 贴图引用 Texture2D'/Game/.../Dir/NAME.NAME' 抠出文件名 NAME。"""
    if not isinstance(ref, str):
        return None
    m = re.search(r"/Game/.*/([^/.']+)\.", ref)
    return m.group(1) if m else None


_petbase = rows("PETBASE_CONF.json")
_model = rows("MODEL_CONF.json")

# images: petbase_id -> {h:小头像 b:大头像 p:全身图 ps:全身缩略}(全身图去掉 JL_ 前缀省字节)。
#   异色变体 sh/sb/sps(头像形如 3010_1,全身图形如 JL_<拼音>_yise)仅在与普通版不同时收录;
#   多数宠物异色复用普通美术(shiny_icon==icon、无 JL_shiny_res),不会产生 sh/sb/sps。
def _strip_jl(s):
    return s[3:] if s and s.startswith("JL_") else s


images = {}
for pid, p in _petbase.items():
    m = _model.get(str(p.get("model_conf"))) or {}
    entry = {}
    head = texkey(m.get("icon") or m.get("small_icon") or m.get("ui_icon"))
    big = texkey(m.get("big_icon"))
    portrait = texkey(p.get("JL_res"))
    portrait_s = _strip_jl(texkey(p.get("JL_small_res")))
    if head:
        entry["h"] = head
    if big:
        entry["b"] = big
    if portrait:
        entry["p"] = _strip_jl(portrait)
    if portrait_s:
        entry["ps"] = portrait_s
    # 异色变体(仅当与普通版不同):小头像/大头像/全身缩略。
    sh = texkey(m.get("shiny_icon"))
    sb = texkey(m.get("big_shiny_icon"))
    sps = _strip_jl(texkey(p.get("JL_small_shiny_res")))
    if sh and sh != head:
        entry["sh"] = sh
    if sb and sb != big:
        entry["sb"] = sb
    if sps and sps != portrait_s:
        entry["sps"] = sps
    if entry:
        images[pid] = entry

# image_base: conf_id -> base_id(petbase),仅当与自身不同;base==自身者 Go 侧回退直查 images。
image_base = {}
for src in ("MONSTER_CONF.json", "PET_CONF.json"):
    for cid, r in rows(src).items():
        b = r.get("base_id")
        if b is not None and str(b) != cid:
            image_base.setdefault(cid, b)

# petbase: petbase_id -> {n:名称 b:图鉴编号 f:形态名 s:进化阶段 e:进化链分组}。
#   宠物当前形态由 PetData.base_conf_id 直接给出(指向当前 petbase),据此取当前名称/头像/图鉴;
#   conf_id 只指向该线一阶 base,evolved 宠物若用 conf_id 会显示成基础形态。
#   同一 pet_evolution_id 分组、按 stage 排序即该形态的完整进化链(雪绒鸟→冬羽雀→岚鸟)。
petbase = {}
for pid, p in _petbase.items():
    name = p.get("name")
    if not name:
        continue
    e = {"n": name}
    if p.get("pictorial_book_id"):
        e["b"] = p["pictorial_book_id"]
    if p.get("form"):
        e["f"] = p["form"]
    if p.get("stage"):
        e["s"] = p["stage"]
    evo = p.get("pet_evolution_id")
    if isinstance(evo, list) and evo:
        e["e"] = evo[0]
    petbase[pid] = e

# 性格增减维度(权威表，按性格名匹配；维度编号 1生命 2物攻 3魔攻 4物防 5魔防 6速度)。
# NATURE_CONF 推导对个别性格(如平和)的 id 错位，故以名为准。
NATURE_TABLE = {
    "胆小": (6, 2), "急躁": (6, 4), "开朗": (6, 3), "莽撞": (6, 5), "热情": (6, 1),  # 速度增益
    "沉默": (1, 2), "忧郁": (1, 4), "平和": (1, 3), "粗心": (1, 5), "踏实": (1, 6),  # 生命增益
    "大胆": (2, 4), "固执": (2, 3), "调皮": (2, 5), "勇敢": (2, 6), "逞强": (2, 1),  # 物攻增益
    "稳重": (4, 2), "天真": (4, 3), "懒散": (4, 5), "悠闲": (4, 6), "坦率": (4, 1),  # 物防增益
    "聪明": (3, 2), "专注": (3, 4), "偏执": (3, 5), "冷静": (3, 6), "理性": (3, 1),  # 魔攻增益
    "警惕": (5, 2), "温顺": (5, 4), "害羞": (5, 3), "慎重": (5, 6), "焦虑": (5, 1),  # 魔防增益
}
nature_effect = {}
for k, v in rows("AUDIO_NATURE_CONF.json").items():
    if v.get("name") in NATURE_TABLE:
        pos, neg = NATURE_TABLE[v["name"]]
        nature_effect[k] = {"pos": pos, "neg": neg}

data = {
    "species": species,
    "nature": {k: v.get("name", "") for k, v in rows("AUDIO_NATURE_CONF.json").items() if v.get("name")},
    "nature_effect": nature_effect,
    "skill_dam_type": enum_dim("SkillDamType"),
    "talent_rate": enum_dim("PetTalentRate"),
    "partner_mark": enum_dim("PetPartnerMarkType"),
    # 特长：仅取 PET_TALENT_CONF 里 filter_enum_value=PTFN_TALENT_* 的固定特长，
    # 避免误用非特长条目;id=502 的 name 为"勇敢"，游戏内显示为"无畏"。
    "speciality": {
        k: ("无畏" if int(k) == 502 else v["name"])
        for k, v in rows("PET_TALENT_CONF.json").items()
        if str(v.get("filter_enum_value", "")).startswith("PTFN_TALENT") and v.get("name")
    },
    "medal": {k: {"name": v.get("name", ""), "desc": v.get("desc", "")}
              for k, v in rows("MEDAL_CONF.json").items() if v.get("name")},
    # 图片索引:petbase 形态 -> 文件名;conf_id -> petbase(经 base_id,与自身相同者省略)。
    "images": images,
    "image_base": image_base,
    # petbase 形态元数据(名称/图鉴号/形态名/阶段/进化链分组),按 base_conf_id 取当前形态。
    "petbase": petbase,
    # opcode 整数 -> ZoneSvrCmd 名称(供 debug 页面展示事件名)。
    # 取自 all.pb 的 ZoneSvrCmd 全集(含 6531=ZONE_SCENE_THROW_CATCH_FINISH_RSP 等),
    # 与 internal/pb 同源同版本,无需手工补充。
    "opcodes": {str(v): k for k, v in pbdesc.enum(_FDS, "ZoneSvrCmd").items()},
}

os.makedirs(OUT, exist_ok=True)
with open(os.path.join(OUT, "names.json"), "w", encoding="utf-8") as f:
    json.dump(data, f, ensure_ascii=False, separators=(",", ":"))

for k, v in data.items():
    print(f"  {k}: {len(v)} 项")
print("-> " + os.path.join(OUT, "names.json"))
