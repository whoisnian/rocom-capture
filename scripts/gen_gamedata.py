"""从 world-data 提取宠物展示需要的 id->中文名 精简表，输出到 internal/gamedata/data/names.json。

数据来源(Bin/BinDataCompressed 与 PB/proto_out)：
- 种类:   PET_CONF.json            conf_id -> name
- 性格:   AUDIO_NATURE_CONF.json   nature_id -> name
- 奖牌:   MEDAL_CONF.json          id -> {name, desc}
- 系别/天分/标记/特长: PET_FILTER_CONF.json 的 filter_enum_value -> filter_desc，
          再用 xls_enum.proto 把 enum 值名解析为整数。
"""
import json
import os
import re
import sys

ROOT = sys.argv[1] if len(sys.argv) > 1 else os.path.expanduser(
    "~/Git/gh/Roco-Kingdom-World-Data-2026-05-21/pakchunk4-WindowsNoEditor")
BIN = os.path.join(ROOT, "Bin", "BinDataCompressed")
PROTO = os.path.join(ROOT, "PB", "proto_out")
OUT = "internal/gamedata/data"


def rows(name):
    return json.load(open(os.path.join(BIN, name), encoding="utf-8"))["RocoDataRows"]


def parse_enum(proto_file, enum_name):
    """返回 {VALUE_NAME: int}。"""
    out = {}
    text = open(os.path.join(PROTO, proto_file), encoding="utf-8", errors="ignore").read()
    m = re.search(r"enum\s+" + re.escape(enum_name) + r"\s*\{(.*?)\}", text, re.S)
    if not m:
        return out
    for vm in re.finditer(r"(\w+)\s*=\s*(-?\d+)\s*;", m.group(1)):
        out.setdefault(vm.group(1), int(vm.group(2)))
    return out


# PET_FILTER 按 enum 名分组: {enum_name: {value_name: desc}}
filter_groups = {}
for r in rows("PET_FILTER_CONF.json").values():
    en = r.get("filter_enum_name")
    if en:
        filter_groups.setdefault(en, {})[r.get("filter_enum_value")] = r.get("filter_desc")


def enum_dim(enum_name):
    """组合 proto enum(名->int) 与 filter(名->中文) 得到 {int: 中文}。"""
    name2int = parse_enum("xls_enum.proto", enum_name)
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
    # 特长：仅取 PET_TALENT_CONF 里 filter_enum_value=PTFN_TALENT_* 的固定特长(11 种)，
    # 避免误用非特长条目;id=502 的 name 为"勇敢"，游戏内显示为"无畏"。
    "speciality": {
        k: ("无畏" if int(k) == 502 else v["name"])
        for k, v in rows("PET_TALENT_CONF.json").items()
        if str(v.get("filter_enum_value", "")).startswith("PTFN_TALENT") and v.get("name")
    },
    "medal": {k: {"name": v.get("name", ""), "desc": v.get("desc", "")}
              for k, v in rows("MEDAL_CONF.json").items() if v.get("name")},
    # opcode 整数 -> ZoneSvrCmd 名称(供 debug 页面展示事件名)
    "opcodes": {str(v): k for k, v in parse_enum("c2s_cmd.proto", "ZoneSvrCmd").items()},
}

os.makedirs(OUT, exist_ok=True)
with open(os.path.join(OUT, "names.json"), "w", encoding="utf-8") as f:
    json.dump(data, f, ensure_ascii=False, separators=(",", ":"))

for k, v in data.items():
    print(f"  {k}: {len(v)} 项")
print("-> " + os.path.join(OUT, "names.json"))
