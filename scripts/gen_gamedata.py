"""从 pak-public-kit 导出数据提取宠物展示需要的 id->中文名 精简表，输出到 internal/gamedata/data/names.json。

数据来源(pak-public-kit 的 output 目录，见 AGENTS.md reference)：
- 种类:   data/BinData/MONSTER_CONF.json + PET_CONF.json  id -> name
- 性格:   data/BinData/AUDIO_NATURE_CONF.json              nature_id -> name
- 奖牌:   data/BinData/MEDAL_CONF.json                     id -> {name, desc}
- 系别/天分/标记/特长: data/BinData/PET_FILTER_CONF.json 的 filter_enum_value -> filter_desc，
          再用反编译 Lua 的 Data/PB/ProtoEnum.lua 把 enum 值名解析为整数。
- opcode: 游戏描述符 proto/all.pb 里的 ZoneSvrCmd 枚举(完整全集，含 6531 等)。

opcode 与字段号(internal/pb)同出 proto/all.pb(见 gen_proto.sh),与 internal/pb 天然同版本;
all.pb 为追加式,几乎不变,故无需随版本跟新(原因见 docs/data.md)。
"""
import json
import os
import re
import subprocess
import sys

ROOT = sys.argv[1] if len(sys.argv) > 1 else os.path.expanduser(
    "~/Git/gh/pak-public-kit/output")
BIN = os.path.join(ROOT, "data", "BinData")
LUA = os.path.join(ROOT, "scripts", "lua", "Data", "PB")
PROTOENUM_LUA = os.path.join(LUA, "ProtoEnum.lua")
# opcode 取自游戏描述符 all.pb 的 ZoneSvrCmd 枚举(与 internal/pb 同源)。
ALL_PB = os.path.join(os.environ.get("NRC_PB_DIR", "proto"), "all.pb")
OUT = "internal/gamedata/data"


def rows(name):
    return json.load(open(os.path.join(BIN, name), encoding="utf-8"))["RocoDataRows"]


def parse_lua_enum(lua_file, qualified_name):
    """解析反编译 Lua 中形如 `Prefix.EnumName = { KEY = n, ... }` 的枚举，返回 {KEY: int}。

    枚举体内只有 `名 = 数字` 项、无嵌套花括号，故非贪婪匹配到首个 `}` 即枚举结束。
    """
    out = {}
    text = open(lua_file, encoding="utf-8", errors="ignore").read()
    m = re.search(re.escape(qualified_name) + r"\s*=\s*\{(.*?)\}", text, re.S)
    if not m:
        return out
    for vm in re.finditer(r"(\w+)\s*=\s*(-?\d+)", m.group(1)):
        out.setdefault(vm.group(1), int(vm.group(2)))
    return out


def parse_pb_enum(all_pb, enum_name):
    """从描述符 all.pb 里解析名为 enum_name 的顶层枚举，返回 {VALUE_NAME: int}。

    用 protoc 把 all.pb 解成 FileDescriptorSet 文本(无需 python-protobuf),其层级缩进:
    文件名 2 空格、enum_type.name 4 空格、value.name/number 6 空格。定位目标枚举后,
    收集其 value 直到遇到同级(4 空格)的下一个 name 或下一个文件(2 空格 *.proto)。
    """
    text = subprocess.run(
        ["protoc", "--decode=google.protobuf.FileDescriptorSet",
         "google/protobuf/descriptor.proto", "-I/usr/include"],
        stdin=open(all_pb, "rb"), capture_output=True, text=True, check=True).stdout
    lines = text.splitlines()
    start = next((i for i, l in enumerate(lines)
                  if re.match(r'^    name: "' + re.escape(enum_name) + r'"$', l)), None)
    if start is None:
        return {}
    out, cur = {}, None
    for l in lines[start + 1:]:
        if re.match(r'^    name: "', l) or re.match(r'^  name: "[^"]+\.proto"$', l):
            break  # 进入同级下一个 enum_type 或下一个文件,本枚举结束
        m = re.match(r'^      name: "([^"]+)"$', l)
        n = re.match(r'^      number: (-?\d+)$', l)
        if m:
            cur = m.group(1)
        elif n and cur is not None:
            out.setdefault(cur, int(n.group(1)))
            cur = None
    return out


# PET_FILTER 按 enum 名分组: {enum_name: {value_name: desc}}
filter_groups = {}
for r in rows("PET_FILTER_CONF.json").values():
    en = r.get("filter_enum_name")
    if en:
        filter_groups.setdefault(en, {})[r.get("filter_enum_value")] = r.get("filter_desc")


def enum_dim(enum_name):
    """组合 ProtoEnum.lua(名->int) 与 filter(名->中文) 得到 {int: 中文}。"""
    name2int = parse_lua_enum(PROTOENUM_LUA, "ProtoEnum." + enum_name)
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
    "opcodes": {str(v): k for k, v in parse_pb_enum(ALL_PB, "ZoneSvrCmd").items()},
}

os.makedirs(OUT, exist_ok=True)
with open(os.path.join(OUT, "names.json"), "w", encoding="utf-8") as f:
    json.dump(data, f, ensure_ascii=False, separators=(",", ":"))

for k, v in data.items():
    print(f"  {k}: {len(v)} 项")
print("-> " + os.path.join(OUT, "names.json"))
