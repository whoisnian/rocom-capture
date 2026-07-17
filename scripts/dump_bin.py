"""把解包目录下的全部游戏二进制配置批量解码为 JSON(调试/查数据用,不产 repo 生成物)。

scripts/unpack.sh 重新解包后跑一次,之后查数据直接 grep/jq JSON,免得每张表都临时调 decode_bin 重新解析:

    uv run python scripts/dump_bin.py            # 默认源 ~/Downloads/rocom/parsed/NRC/Content/ScriptC/Data/Bin
    uv run python scripts/dump_bin.py <Bin目录>  # 或用 ROCOM_PARSED 覆盖解包根
    uv run python scripts/dump_bin.py --force    # 忽略增量,全部重解

- 输入: <Bin>/BinDataCompressed/*.bytes + BinConf/*.non(schema)+ BinLocalize/dev_CN(本地化,有则用)
- 输出: <Bin>/Json/<表名>.json(decode_bin 的完整解码结果,含 RocoDataRows)
- 增量: 输出比 .bytes/.non/本地化都新则跳过;解码失败的表报错并继续,不中断整体。
"""

import json
import multiprocessing
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import decode_bin  # vendored 解码器(scripts/decode_bin.py,纯标准库)

FORCE = "--force" in sys.argv
_pos = [a for a in sys.argv[1:] if not a.startswith("-")]
PARSED = os.environ.get("ROCOM_PARSED", os.path.expanduser("~/Downloads/rocom/parsed"))
SRC = _pos[0] if _pos else os.path.join(PARSED, "NRC", "Content", "ScriptC", "Data", "Bin")
OUT = os.path.join(SRC, "Json")


def dump_one(base: str) -> str | None:
    """解码一张表并写 JSON;跳过返回 None,成功返回 "ok",失败返回错误串。"""
    src = os.path.join(SRC, "BinDataCompressed", base + ".bytes")
    schema = os.path.join(SRC, "BinConf", base + ".non")
    loc = os.path.join(SRC, "BinLocalize", "dev_CN", base + ".bytes")
    if not os.path.exists(loc):
        loc = None
    dst = os.path.join(OUT, base + ".json")
    deps = [src, schema] + ([loc] if loc else [])
    if not FORCE and os.path.exists(dst) and os.path.getmtime(dst) >= max(map(os.path.getmtime, deps)):
        return None
    try:
        data = decode_bin.decode_file(src, schema_path=schema, loc_path=loc)
        tmp = dst + ".tmp"
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump(data, f, ensure_ascii=False, indent=1)
            f.write("\n")
        os.replace(tmp, dst)
        return "ok"
    except Exception as e:  # 个别表结构特殊解不开,不拖累其余
        return f"{type(e).__name__}: {e}"


def main():
    conf_dir = os.path.join(SRC, "BinConf")
    if not os.path.isdir(conf_dir):
        sys.exit(f"源目录不存在: {conf_dir}(可传 Bin 目录或用 ROCOM_PARSED 覆盖解包根)")
    bases = sorted(n[:-4] for n in os.listdir(conf_dir) if n.endswith(".non")
                   and os.path.exists(os.path.join(SRC, "BinDataCompressed", n[:-4] + ".bytes")))
    os.makedirs(OUT, exist_ok=True)
    with multiprocessing.Pool() as pool:
        results = pool.map(dump_one, bases)
    done = sum(1 for r in results if r == "ok")
    kept = sum(1 for r in results if r is None)
    fails = [(b, r) for b, r in zip(bases, results) if r not in (None, "ok")]
    for b, r in fails:
        print(f"  {b}: {r}", file=sys.stderr)
    print(f"-> {OUT}  新解 {done},未变跳过 {kept},失败 {len(fails)}(--force 可全部重解)")


if __name__ == "__main__":
    main()
