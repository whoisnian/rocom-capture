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
