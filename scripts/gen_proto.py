"""从游戏描述符 all.pb 生成宠物相关 Go 结构体(internal/pb)。

all.pb 是解包出的 google.protobuf.FileDescriptorSet(游戏运行时 pb.loadufsfile
加载的同一份),含字段号/类型,直接喂给 protoc 即可生成 Go,无需 .proto 文本、无需 fix_proto。
com_pet.proto 的依赖闭包由描述符**动态求取**(随 all.pb 版本而变,避免硬编码失配)。
protoc 解析 rpc_options.proto 需要 well-known 的 descriptor.proto,本脚本在内存里补进描述符集。

运行(需 uv 管理的 protobuf 依赖):  uv run python scripts/gen_proto.py
更新游戏版本:重跑 scripts/unpack.sh 刷新解包目录再跑本脚本(源目录用 ROCOM_PARSED 覆盖)。
"""
import os
import subprocess
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import pbdesc

PKG = "github.com/whoisnian/rocom-capture/internal/pb"
OUT = "internal/pb"
# 解析根:com_pet(PetData/背包) + com_pet_team(大世界队伍 PetTeamInfo)。闭包动态合并求取。
ROOTS = ["com_pet.proto", "com_pet_team.proto"]
PARSED = os.environ.get("ROCOM_PARSED", os.path.expanduser("~/Downloads/rocom/parsed"))
ALL_PB = os.path.join(PARSED, "NRC", "Content", "ScriptC", "Data", "PB", "all.pb")
DESCRIPTORPB_GO = "google.golang.org/protobuf/types/descriptorpb"


def main():
    fds = pbdesc.load(ALL_PB)
    files, seen = [], set()
    for root in ROOTS:
        c = pbdesc.closure(fds, root)
        if not c:
            sys.exit(f"无法从 {ALL_PB} 求取 {root} 闭包")
        for f in c:
            if f not in seen:
                seen.add(f)
                files.append(f)
    print(f"闭包({len(files)} 文件): {' '.join(files)}")

    # protoc-gen-go 在 GOPATH/bin
    gobin = subprocess.run(["go", "env", "GOPATH"], capture_output=True, text=True,
                           check=True).stdout.strip() + "/bin"
    env = dict(os.environ, PATH=gobin + os.pathsep + os.environ.get("PATH", ""))

    # 闭包内游戏文件无 go_package,全部映射到单一 Go 包;descriptor.proto 映射到 descriptorpb。
    mappings = [f"--go_opt=M{f}={PKG}" for f in files]
    mappings.append(f"--go_opt=M{pbdesc.WELL_KNOWN}={DESCRIPTORPB_GO}")

    os.makedirs(OUT, exist_ok=True)
    for f in os.listdir(OUT):
        if f.endswith(".pb.go"):
            os.remove(os.path.join(OUT, f))

    with tempfile.NamedTemporaryFile(suffix=".pb") as tmp:
        tmp.write(pbdesc.with_descriptor(fds))
        tmp.flush()
        subprocess.run(
            ["protoc", f"--descriptor_set_in={tmp.name}",
             f"--go_out={OUT}", "--go_opt=paths=source_relative",
             *mappings, *files],
            check=True, env=env)

    generated = sorted(f for f in os.listdir(OUT) if f.endswith(".pb.go"))
    print("生成完成:")
    for f in generated:
        print(f"  {OUT}/{f}")


if __name__ == "__main__":
    main()
