"""从游戏描述符 nrc/all.pb 生成宠物相关 Go 结构体(internal/pb)。

nrc/all.pb 是 FModel 提取的 google.protobuf.FileDescriptorSet(游戏运行时 pb.loadufsfile
加载的同一份),含字段号/类型,直接喂给 protoc 即可生成 Go,无需 .proto 文本、无需 fix_proto。
com_pet.proto 的依赖闭包由描述符**动态求取**(随 all.pb 版本而变,避免硬编码失配)。
protoc 解析 rpc_options.proto 需要 well-known 的 descriptor.proto,本脚本在内存里补进描述符集。

运行(需 uv 管理的 protobuf 依赖):  uv run python scripts/gen_proto.py
更新游戏版本:用 FModel 重新提取 all.pb 覆盖 nrc/all.pb 再跑本脚本(或设 NRC_PB_DIR 指向别处)。
"""
import os
import subprocess
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import pbdesc

PKG = "github.com/whoisnian/rocom-capture/internal/pb"
OUT = "internal/pb"
ROOT = "com_pet.proto"
ALL_PB = os.path.join(os.environ.get("NRC_PB_DIR", "nrc"), "all.pb")
DESCRIPTORPB_GO = "google.golang.org/protobuf/types/descriptorpb"


def main():
    fds = pbdesc.load(ALL_PB)
    files = pbdesc.closure(fds, ROOT)
    if not files:
        sys.exit(f"无法从 {ALL_PB} 求取 {ROOT} 闭包")
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
