"""
洛克王国 .bytes / .non 二进制配置文件解码器
参考 CUE4Parse FRocoBinData.cs 实现

[vendored] 本文件为社区解码器原样收录(供 scripts/gen_gamedata.py 等解码解包目录
Bin 下的游戏配置)。经实测:对当前解包出的 Bin 数据,本解码器与 pak-public-kit 版逐表 100%
一致(MONSTER/PET/MEDAL/特长),且 ELocalizedString 本地化正确(pak-public-kit 早期产出的
PET_CONF 名整体错位一位,本解码器与两个独立 world-data 源一致)。详见 docs/data.md。

用法:
  # 解码单个文件 (自动查找同名 .non schema)
  python decode_bin.py ACTIVITY_CONF.bytes

  # 指定 schema
  python decode_bin.py ACTIVITY_CONF.bytes --schema ACTIVITY_CONF.non

  # 指定类型 (默认自动检测)
  python decode_bin.py ACTIVITY_CONF.bytes --type BinDataCompressed

  # 带本地化
  python decode_bin.py ACTIVITY_CONF.bytes --loc BinLocalize/en_US/ACTIVITY_CONF.bytes

  # 批量解码目录
  python decode_bin.py --batch-dir ./raw/BinDataCompressed --schema-dir ./raw/BinConf --out-dir ./decoded
"""

import struct
import json
import math
import os
import sys
import argparse
from pathlib import Path
from typing import Any

MAGIC = 0x53DF17BE

FOOTER_SIZES = {
    "BinDataCompressed": 68,
    "BinData": 56,
    "BinLocalize": 28,
}


# ── 底层读取 ──────────────────────────────────────────────

class BinReader:
    def __init__(self, data: bytes):
        self.data = data
        self.pos = 0
        self.length = len(data)

    def seek(self, pos: int):
        self.pos = pos

    def read_bytes(self, n: int) -> bytes:
        result = self.data[self.pos:self.pos + n]
        self.pos += n
        return result

    def read_uint32(self) -> int:
        val = struct.unpack_from('<I', self.data, self.pos)[0]
        self.pos += 4
        return val

    def read_int32(self) -> int:
        val = struct.unpack_from('<i', self.data, self.pos)[0]
        self.pos += 4
        return val

    def read_int64(self) -> int:
        val = struct.unpack_from('<q', self.data, self.pos)[0]
        self.pos += 8
        return val

    def read_uint64(self) -> int:
        val = struct.unpack_from('<Q', self.data, self.pos)[0]
        self.pos += 8
        return val

    def read_float(self) -> float:
        val = struct.unpack_from('<f', self.data, self.pos)[0]
        self.pos += 4
        return round(val, 6)

    def read_byte(self) -> int:
        val = self.data[self.pos]
        self.pos += 1
        return val


# ── 表结构 ────────────────────────────────────────────────

class BinTable:
    """FRocoBinTable: Index(u32) + Length(i32) + Offset(i64) = 16 bytes"""
    __slots__ = ('index', 'length', 'offset')

    def __init__(self, reader: BinReader):
        self.index = reader.read_uint32()
        self.length = reader.read_int32()
        self.offset = reader.read_int64()


class BinFooter:
    """文件尾部元信息，不同类型布局不同"""

    def __init__(self, reader: BinReader, bin_type: str):
        self.data_section_offset = 0
        self.data_section_length = 0
        self.entries_count = 0
        self.struct_size = 0
        self.data_table_offset = 0
        self.data_table_entries_count = 0
        self.constants_table_offset = 0
        self.constants_table_entries_count = 0
        self.constants_section_offset = 0
        self.constants_section_length = 0

        if bin_type == "BinDataCompressed":
            self.data_section_offset = reader.read_int64()
            self.data_section_length = reader.read_int64()
            self.entries_count = reader.read_int32()
            self.struct_size = reader.read_int64()
            self.data_table_offset = reader.read_int64()
            self.data_table_entries_count = reader.read_int32()
            self.constants_table_offset = reader.read_int64()
            self.constants_table_entries_count = reader.read_int32()
            self.constants_section_offset = reader.read_int64()
            self.constants_section_length = reader.read_int64()
        elif bin_type == "BinData":
            self.data_section_offset = reader.read_int64()
            self.data_section_length = reader.read_int64()
            self.entries_count = reader.read_int32()
            self.struct_size = reader.read_int64()
            self.constants_table_offset = reader.read_int64()
            self.constants_table_entries_count = reader.read_int32()
            self.constants_section_offset = reader.read_int64()
            self.constants_section_length = reader.read_int64()
        elif bin_type == "BinLocalize":
            self.constants_table_offset = reader.read_int64()
            self.constants_table_entries_count = reader.read_int32()
            self.constants_section_offset = reader.read_int64()
            self.constants_section_length = reader.read_int64()


# ── 核心解码器 ────────────────────────────────────────────

class RocoBinDecoder:
    def __init__(self, data: bytes, schema: dict | None, bin_type: str,
                 loc_decoder: 'RocoBinDecoder | None' = None):
        self.reader = BinReader(data)
        self.schema = schema
        self.bin_type = bin_type
        self.data_table: list[BinTable] = []
        self.constants_table: list[BinTable] = []
        self.rows: dict[str, Any] = {}
        self.loc_strings: dict[int, str] = {}

        # 校验 magic
        magic = self.reader.read_uint32()
        if magic != MAGIC:
            raise ValueError(f"Invalid magic: 0x{magic:08X}, expected 0x{MAGIC:08X}")

        # 读 footer
        footer_size = FOOTER_SIZES[bin_type]
        self.reader.seek(self.reader.length - footer_size)
        footer = BinFooter(self.reader, bin_type)

        # 读 data table
        if footer.data_table_offset > 0:
            self.reader.seek(footer.data_table_offset)
            self.data_table = [BinTable(self.reader)
                               for _ in range(footer.data_table_entries_count)]

        # 读 constants table
        if footer.constants_table_offset > 0:
            self.reader.seek(footer.constants_table_offset)
            self.constants_table = [BinTable(self.reader)
                                    for _ in range(footer.constants_table_entries_count)]

        # BinLocalize: 只解本地化字符串
        if bin_type == "BinLocalize":
            self.reader.seek(footer.constants_section_offset)
            for i in range(footer.constants_table_entries_count):
                entry = self.constants_table[i]
                if entry.length == 0:
                    continue
                self.reader.seek(entry.offset)
                raw = self.reader.read_bytes(entry.length)
                self.loc_strings[i + 1] = raw.decode('utf-8')
            return

        if not self.data_table:
            return

        # 解数据行
        self.reader.seek(footer.data_section_offset)
        unique_key = schema.get("UniqueKey", "id") if schema else "id"
        for i in range(footer.entries_count):
            entry = self.data_table[i]
            if entry.length == 0:
                continue
            if entry.offset != self.reader.pos:
                self.reader.seek(entry.offset)
            row = self._parse_struct(schema, loc_decoder)
            key = str(row.get(unique_key, f"Unknown_{i}"))
            self.rows[key] = row

    def _read_string(self) -> str:
        idx = self.reader.read_uint32()
        if idx == 0:
            return ""
        entry = self.constants_table[idx - 1]
        saved = self.reader.pos
        self.reader.seek(entry.offset)
        s = self.reader.read_bytes(entry.length).decode('utf-8')
        self.reader.pos = saved
        return s

    def _read_array(self, prop: dict, loc: 'RocoBinDecoder | None',
                    is_dynamic: bool = False) -> list:
        const_idx = self.reader.read_int32()
        entry = self.constants_table[const_idx - 1]
        saved = self.reader.pos
        self.reader.seek(entry.offset)
        elem_size = prop["Size"] if is_dynamic else 4
        count = entry.length // elem_size
        result = [self._read_property(prop, loc) for _ in range(count)]
        self.reader.pos = saved
        return result

    def _read_property(self, prop: dict, loc: 'RocoBinDecoder | None') -> Any:
        t = prop["Type"]
        if t == "EUint32":
            return self.reader.read_uint32()
        elif t == "EInt32":
            return self.reader.read_int32()
        elif t == "EInt64":
            return self.reader.read_int64()
        elif t == "EUint64":
            return self.reader.read_uint64()
        elif t == "EFloat":
            return self.reader.read_float()
        elif t == "EBool":
            return self.reader.read_byte() != 0
        elif t == "EString":
            return self._read_string()
        elif t == "EStruct":
            return self._parse_nested_struct(prop["Struct"], loc)
        elif t == "ELocalizedString":
            idx = self.reader.read_int32()
            if loc and idx in loc.loc_strings:
                return loc.loc_strings[idx]
            return ""
        else:
            raise ValueError(f"Unknown property type: {t}")

    def _parse_struct(self, schema: dict, loc: 'RocoBinDecoder | None') -> dict:
        props = schema["Properties"]
        flag_bytes_count = math.ceil(len(props) / 8)
        flags = self.reader.read_bytes(flag_bytes_count)
        row = {}
        for j, prop in enumerate(props):
            is_present = (flags[j // 8] & (1 << (7 - (j % 8)))) != 0
            if not is_present:
                continue
            is_dynamic = prop.get("DynamicArray", False)
            array_dim = prop.get("ArrayDim")
            if is_dynamic or array_dim is not None:
                row[prop["Name"]] = self._read_array(prop, loc, is_dynamic)
            else:
                row[prop["Name"]] = self._read_property(prop, loc)
        return row

    def _parse_nested_struct(self, schema: dict,
                             loc: 'RocoBinDecoder | None') -> dict:
        const_idx = self.reader.read_int32()
        entry = self.constants_table[const_idx - 1]
        saved = self.reader.pos
        self.reader.seek(entry.offset)
        row = self._parse_struct(schema, loc)
        self.reader.pos = saved
        return row

    def to_dict(self) -> dict:
        if self.bin_type == "BinLocalize":
            return {"LocalizationStrings": self.loc_strings}
        return {"RocoDataRows": self.rows}


# ── 辅助函数 ──────────────────────────────────────────────

def guess_bin_type(filepath: str) -> str:
    """从路径推断类型"""
    p = filepath.replace('\\', '/')
    if "BinDataCompressed" in p:
        return "BinDataCompressed"
    elif "BinLocalize" in p:
        return "BinLocalize"
    elif "BinData" in p:
        return "BinData"
    # 默认尝试 BinDataCompressed
    return "BinDataCompressed"


def find_schema(bytes_path: str, schema_dir: str | None = None) -> str | None:
    """自动查找同名 .non 或 .json schema 文件"""
    name = Path(bytes_path).stem
    search_dirs = []
    if schema_dir:
        search_dirs.append(Path(schema_dir))
    parent = Path(bytes_path).parent
    search_dirs.extend([
        parent / "BinConf",
        parent.parent / "BinConf",
        parent,
    ])
    for d in search_dirs:
        for ext in (".non", ".json"):
            candidate = d / (name + ext)
            if candidate.exists():
                return str(candidate)
    return None


def decode_file(bytes_path: str, schema_path: str | None = None,
                bin_type: str | None = None, loc_path: str | None = None,
                schema_dir: str | None = None) -> dict:
    """解码单个 .bytes 文件"""
    if bin_type is None:
        bin_type = guess_bin_type(bytes_path)

    with open(bytes_path, 'rb') as f:
        data = f.read()

    # 本地化类型不需要 schema
    if bin_type == "BinLocalize":
        decoder = RocoBinDecoder(data, None, bin_type)
        return decoder.to_dict()

    # 查找 schema
    if schema_path is None:
        schema_path = find_schema(bytes_path, schema_dir)
    if schema_path is None:
        raise FileNotFoundError(
            f"找不到 {Path(bytes_path).stem} 的 schema 文件 (.non/.json)，"
            f"请用 --schema 指定")

    with open(schema_path, 'r', encoding='utf-8') as f:
        schema = json.load(f)

    # 可选: 加载本地化
    loc_decoder = None
    if loc_path:
        with open(loc_path, 'rb') as f:
            loc_data = f.read()
        loc_decoder = RocoBinDecoder(loc_data, None, "BinLocalize")

    decoder = RocoBinDecoder(data, schema, bin_type, loc_decoder)
    return decoder.to_dict()


# ── CLI ───────────────────────────────────────────────────

def batch_decode(src_dir: str, schema_dir: str, out_dir: str,
                 bin_type: str | None = None, loc_dir: str | None = None):
    """批量解码目录下所有 .bytes 文件"""
    src = Path(src_dir)
    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)

    files = list(src.glob("*.bytes"))
    if not files:
        print(f"目录 {src_dir} 下没有 .bytes 文件")
        return

    ok, fail = 0, 0
    for f in sorted(files):
        try:
            loc_path = None
            if loc_dir:
                loc_candidate = Path(loc_dir) / f.name
                if loc_candidate.exists():
                    loc_path = str(loc_candidate)

            result = decode_file(str(f), schema_dir=schema_dir,
                                 bin_type=bin_type, loc_path=loc_path)
            out_path = out / (f.stem + ".json")
            with open(out_path, 'w', encoding='utf-8') as wf:
                json.dump(result, wf, ensure_ascii=False, indent=2)
            print(f"  OK  {f.name} -> {out_path.name}")
            ok += 1
        except Exception as e:
            print(f"  ERR {f.name}: {e}")
            fail += 1

    print(f"\n完成: {ok} 成功, {fail} 失败")


def main():
    parser = argparse.ArgumentParser(
        description="洛克王国 .bytes 二进制配置解码器")
    parser.add_argument("file", nargs="?", help=".bytes 文件路径")
    parser.add_argument("--schema", "-s", help=".non / .json schema 路径")
    parser.add_argument("--type", "-t", choices=list(FOOTER_SIZES.keys()),
                        help="数据类型 (默认自动检测)")
    parser.add_argument("--loc", "-l", help="本地化 .bytes 文件路径")
    parser.add_argument("--output", "-o", help="输出 JSON 路径 (默认 stdout)")
    parser.add_argument("--schema-dir", help="schema 搜索目录")
    parser.add_argument("--batch", action="store_true",
                        help="批量模式: file 为 .bytes 目录, 需配合 --schema-dir 和 --out-dir")
    parser.add_argument("--out-dir", help="批量模式输出目录")
    parser.add_argument("--loc-dir", help="批量模式本地化文件目录")

    args = parser.parse_args()

    if not args.file:
        parser.print_help()
        return

    if args.batch:
        if not args.schema_dir or not args.out_dir:
            parser.error("批量模式需要 --schema-dir 和 --out-dir")
        batch_decode(args.file, args.schema_dir, args.out_dir,
                     args.type, args.loc_dir)
        return

    result = decode_file(args.file, args.schema, args.type,
                         args.loc, args.schema_dir)

    output = json.dumps(result, ensure_ascii=False, indent=2)
    if args.output:
        with open(args.output, 'w', encoding='utf-8') as f:
            f.write(output)
        print(f"已保存到 {args.output}")
    else:
        print(output)


if __name__ == "__main__":
    main()
