"""把解包目录下所有 RocoBinData 二进制配置(.bytes)解码为 JSON,紧邻源文件落盘。

自解包更新后跑一次(scripts/unpack.sh 导出后会自动调),产出既供 grep/jq 查数据,
也是 gen_gamedata/gen_icons 的输入(它们直接读这些 JSON,不再自行解 .bytes)。
按 CUE4Parse 的 `FRocoBinData.cs`(GameTypes/RocoKingdomWorld/Assets/Objects)算法
独立实现,是全仓 .bytes 解码的唯一实现。

- 扫描整个解包根(默认 ~/Downloads/rocom/parsed,可传目录或用 ROCOM_PARSED 覆盖),
  按文件头 magic(0x53DF17BE)识别 RocoBinData;非此格式的 .bytes(BigMap/Audio 等)跳过。
- 类型按目录名判定(格式本身无类型标记):路径含 `BinLocalize` → 本地化串表;父目录名
  `BinData` → 定长表(无 data table,CUE4Parse 同样解不出行,产出空 RocoDataRows);
  其余(`BinDataCompressed` 等)→ 压缩表。压缩/定长表的 schema 取同 Bin 根下 `BinConf/<名>.non`,
  ELocalizedString 用 `BinLocalize/dev_CN/<名>.bytes`(存在则)解引用。
- 输出 `<源>.json`(去掉 .bytes 扩展名)紧邻源文件:压缩/定长表为 `{"RocoDataRows": {...}}`,
  本地化为 `{"LocalizationStrings": {...}}`。
- 增量:输出比源(.bytes/.non/本地化)都新则跳过;--force 全部重解。并行(CPU 核数)。

用法:
    uv run python scripts/bin2json.py              # 默认根 ~/Downloads/rocom/parsed
    uv run python scripts/bin2json.py <解包根/子目录>
    uv run python scripts/bin2json.py --force      # 忽略增量,全部重解
"""
import json
import math
import multiprocessing
import os
import struct
import sys

MAGIC = 0x53DF17BE
FOOTER_SIZES = {"BinDataCompressed": 68, "BinData": 56, "BinLocalize": 28}


# ── 底层读取(FArchive 等价:小端定长)────────────────────────────────
class _Reader:
    def __init__(self, data: bytes):
        self.data = data
        self.pos = 0
        self.length = len(data)

    def seek(self, pos):
        self.pos = pos

    def bytes(self, n):
        b = self.data[self.pos:self.pos + n]
        self.pos += n
        return b

    def _rd(self, fmt, size):
        v = struct.unpack_from(fmt, self.data, self.pos)[0]
        self.pos += size
        return v

    def u32(self): return self._rd("<I", 4)
    def i32(self): return self._rd("<i", 4)
    def u64(self): return self._rd("<Q", 8)
    def i64(self): return self._rd("<q", 8)
    def f32(self): return round(self._rd("<f", 4), 6)
    def byte(self):
        v = self.data[self.pos]
        self.pos += 1
        return v


# ── FRocoBinData 移植 ──────────────────────────────────────────────
# FRocoBinTable: Index(u32) + Length(i32) + Offset(i64)
def _read_table(r, count):
    return [(r.u32(), r.i32(), r.i64()) for _ in range(count)]  # (index, length, offset)


class RocoBin:
    def __init__(self, data: bytes, schema: dict | None, bin_type: str, loc: "RocoBin | None" = None):
        r = _Reader(data)
        self.rows: dict[str, object] = {}
        self.loc_strings: dict[int, str] = {}
        self._const = []
        self._r = r

        if r.u32() != MAGIC:
            raise ValueError(f"magic 不符: 0x{r.data[0]:02X}...")

        r.seek(r.length - FOOTER_SIZES[bin_type])
        f = self._footer(r, bin_type)

        if f["data_table_offset"] > 0:
            r.seek(f["data_table_offset"])
            data_table = _read_table(r, f["data_table_entries_count"])
        else:
            data_table = []
        if f["constants_table_offset"] > 0:
            r.seek(f["constants_table_offset"])
            self._const = _read_table(r, f["constants_table_entries_count"])

        if bin_type == "BinLocalize":
            for i in range(f["constants_table_entries_count"]):
                _, length, offset = self._const[i]
                if length == 0:
                    continue
                r.seek(offset)
                self.loc_strings[i + 1] = r.bytes(length).decode("utf-8")
            return

        if not data_table:
            return

        r.seek(f["data_section_offset"])
        key = (schema or {}).get("UniqueKey") or "id"
        for i in range(f["entries_count"]):
            _, length, offset = data_table[i]
            if length == 0:
                continue
            if offset != r.pos:
                r.seek(offset)
            row = self._struct(schema, loc)
            self.rows[str(row.get(key, f"Unknown_{i}"))] = row

    @staticmethod
    def _footer(r, t):
        f = dict.fromkeys(
            ("data_section_offset", "data_section_length", "entries_count", "struct_size",
             "data_table_offset", "data_table_entries_count", "constants_table_offset",
             "constants_table_entries_count", "constants_section_offset", "constants_section_length"), 0)
        if t == "BinDataCompressed":
            f["data_section_offset"] = r.i64(); f["data_section_length"] = r.i64()
            f["entries_count"] = r.i32(); f["struct_size"] = r.i64()
            f["data_table_offset"] = r.i64(); f["data_table_entries_count"] = r.i32()
            f["constants_table_offset"] = r.i64(); f["constants_table_entries_count"] = r.i32()
            f["constants_section_offset"] = r.i64(); f["constants_section_length"] = r.i64()
        elif t == "BinData":
            f["data_section_offset"] = r.i64(); f["data_section_length"] = r.i64()
            f["entries_count"] = r.i32(); f["struct_size"] = r.i64()
            f["constants_table_offset"] = r.i64(); f["constants_table_entries_count"] = r.i32()
            f["constants_section_offset"] = r.i64(); f["constants_section_length"] = r.i64()
        elif t == "BinLocalize":
            f["constants_table_offset"] = r.i64(); f["constants_table_entries_count"] = r.i32()
            f["constants_section_offset"] = r.i64(); f["constants_section_length"] = r.i64()
        return f

    def _string(self):
        r = self._r
        idx = r.u32()
        if idx == 0:
            return ""
        _, length, offset = self._const[idx - 1]
        saved = r.pos
        r.seek(offset)
        s = r.bytes(length).decode("utf-8")
        r.pos = saved
        return s

    def _array(self, prop, loc, is_dynamic):
        r = self._r
        _, length, offset = self._const[r.i32() - 1]
        saved = r.pos
        r.seek(offset)
        elem_size = prop["Size"] if is_dynamic else 4
        out = [self._property(prop, loc) for _ in range(length // elem_size)]
        r.pos = saved
        return out

    def _property(self, prop, loc):
        r = self._r
        t = prop["Type"]
        if t == "EUint32": return r.u32()
        if t == "EInt32": return r.i32()
        if t == "EInt64": return r.i64()
        if t == "EUint64": return r.u64()
        if t == "EFloat": return r.f32()
        if t == "EBool": return r.byte() != 0
        if t == "EString": return self._string()
        if t == "EStruct": return self._nested(prop["Struct"], loc)
        if t == "ELocalizedString":
            idx = r.i32()
            return loc.loc_strings.get(idx, "") if loc else ""
        raise ValueError(f"未知类型: {t}")

    def _struct(self, schema, loc):
        r = self._r
        props = schema["Properties"]
        flags = r.bytes(math.ceil(len(props) / 8))
        row = {}
        for j, prop in enumerate(props):
            if not (flags[j // 8] & (1 << (7 - (j % 8)))):  # 标志位 MSB 优先,0=缺省跳过
                continue
            dyn = prop.get("DynamicArray", False)
            if dyn or prop.get("ArrayDim") is not None:
                row[prop["Name"]] = self._array(prop, loc, dyn)
            else:
                row[prop["Name"]] = self._property(prop, loc)
        return row

    def _nested(self, schema, loc):
        r = self._r
        _, _, offset = self._const[r.i32() - 1]
        saved = r.pos
        r.seek(offset)
        row = self._struct(schema, loc)
        r.pos = saved
        return row


def _classify(path: str) -> str:
    p = path.replace("\\", "/")
    if "/BinLocalize/" in p:
        return "BinLocalize"
    if os.path.basename(os.path.dirname(p)) == "BinData":
        return "BinData"
    return "BinDataCompressed"


def _is_rocobin(path: str) -> bool:
    try:
        with open(path, "rb") as f:
            return struct.unpack("<I", f.read(4))[0] == MAGIC
    except (OSError, struct.error):
        return False


def _decode(path: str, bin_type: str):
    """解码单个 .bytes;压缩/定长表按需带 dev_CN 本地化。返回 to_dict 结果。"""
    with open(path, "rb") as f:
        data = f.read()
    if bin_type == "BinLocalize":
        return {"LocalizationStrings": RocoBin(data, None, bin_type).loc_strings}

    # schema: 同 Bin 根下 BinConf/<名>.non(父目录的父目录 = Bin 根)
    name = os.path.splitext(os.path.basename(path))[0]
    bin_root = os.path.dirname(os.path.dirname(path))
    schema_path = os.path.join(bin_root, "BinConf", name + ".non")
    if not os.path.exists(schema_path):
        raise FileNotFoundError(f"缺 schema: {schema_path}")
    with open(schema_path, encoding="utf-8") as f:
        schema = json.load(f)

    loc = None
    loc_path = os.path.join(bin_root, "BinLocalize", "dev_CN", name + ".bytes")
    if os.path.exists(loc_path):
        with open(loc_path, "rb") as f:
            loc = RocoBin(f.read(), None, "BinLocalize")
    return {"RocoDataRows": RocoBin(data, schema, bin_type, loc).rows}


def _deps(path: str, bin_type: str):
    """输出的依赖文件(用于增量 mtime 比较)。"""
    deps = [path]
    if bin_type != "BinLocalize":
        name = os.path.splitext(os.path.basename(path))[0]
        bin_root = os.path.dirname(os.path.dirname(path))
        for p in (os.path.join(bin_root, "BinConf", name + ".non"),
                  os.path.join(bin_root, "BinLocalize", "dev_CN", name + ".bytes")):
            if os.path.exists(p):
                deps.append(p)
    return deps


def _work(args):
    path, force = args
    bin_type = _classify(path)
    dst = os.path.splitext(path)[0] + ".json"
    if not force and os.path.exists(dst):
        if os.path.getmtime(dst) >= max(os.path.getmtime(d) for d in _deps(path, bin_type)):
            return "skip"
    try:
        result = _decode(path, bin_type)
        tmp = dst + ".tmp"
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump(result, f, ensure_ascii=False, indent=1)
            f.write("\n")
        os.replace(tmp, dst)
        return "ok"
    except Exception as e:  # 个别表结构特殊解不开,不拖累其余
        return f"fail\t{path}\t{type(e).__name__}: {e}"


def main():
    force = "--force" in sys.argv
    pos = [a for a in sys.argv[1:] if not a.startswith("-")]
    root = pos[0] if pos else os.environ.get(
        "ROCOM_PARSED", os.path.expanduser("~/Downloads/rocom/parsed"))
    if not os.path.isdir(root):
        sys.exit(f"源目录不存在: {root}(可传解包根或设 ROCOM_PARSED)")

    targets = []
    for dirpath, _, files in os.walk(root):
        for n in files:
            if n.endswith(".bytes"):
                p = os.path.join(dirpath, n)
                if _is_rocobin(p):
                    targets.append((p, force))
    if not targets:
        print(f"{root} 下未找到 RocoBinData(.bytes),无事可做")
        return

    with multiprocessing.Pool() as pool:
        results = pool.map(_work, targets)
    ok = sum(r == "ok" for r in results)
    skip = sum(r == "skip" for r in results)
    fails = [r for r in results if r.startswith("fail")]
    for r in fails:
        _, path, err = r.split("\t", 2)
        print(f"  解码失败: {path}: {err}", file=sys.stderr)
    print(f"-> {root}  解码 {ok},跳过 {skip},失败 {len(fails)}(共 {len(targets)} 个 RocoBinData;--force 全部重解)")


if __name__ == "__main__":
    main()
