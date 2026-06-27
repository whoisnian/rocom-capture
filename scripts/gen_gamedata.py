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

data = {
    "species": species,
    "nature": {k: v.get("name", "") for k, v in rows("AUDIO_NATURE_CONF.json").items() if v.get("name")},
    "skill_dam_type": enum_dim("SkillDamType"),
    "talent_rate": enum_dim("PetTalentRate"),
    "partner_mark": enum_dim("PetPartnerMarkType"),
    # 特长：speciality_id 直接对应 PET_TALENT_CONF 的 id
    "speciality": {k: v["name"] for k, v in rows("PET_TALENT_CONF.json").items() if v.get("name")},
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
