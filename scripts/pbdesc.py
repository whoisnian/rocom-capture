"""读取游戏自带的 protobuf 描述符 all.pb(FileDescriptorSet),供生成脚本共用。

依赖 protobuf(uv 管理)。all.pb 是解包目录里的游戏描述符,含字段号/类型/枚举。
"""
from google.protobuf import descriptor_pb2

WELL_KNOWN = "google/protobuf/descriptor.proto"


def load(path):
    """读 all.pb 为 FileDescriptorSet。"""
    fds = descriptor_pb2.FileDescriptorSet()
    with open(path, "rb") as f:
        fds.ParseFromString(f.read())
    return fds


def closure(fds, root):
    """求 root(如 com_pet.proto)的依赖闭包,返回文件名列表(拓扑序)。

    well-known 的 descriptor.proto 不在 all.pb 里,自然被排除(由 with_descriptor 单独补)。
    """
    deps = {f.name: list(f.dependency) for f in fds.file}
    seen = []

    def visit(name):
        if name in seen or name not in deps:
            return
        seen.append(name)
        for d in deps[name]:
            visit(d)

    visit(root)
    return seen


def enum(fds, name):
    """取名为 name 的枚举的 {值名: 整数}(找顶层 enum,再找消息内嵌套一层)。"""
    def pick(enum_types):
        for e in enum_types:
            if e.name == name:
                return {v.name: v.number for v in e.value}
        return None

    for f in fds.file:
        got = pick(f.enum_type)
        if got is None:
            for m in f.message_type:
                got = pick(m.enum_type)
                if got is not None:
                    break
        if got is not None:
            return got
    return {}


def with_descriptor(fds):
    """追加 well-known descriptor.proto 的 FileDescriptorProto,返回合并后字节。

    protoc --descriptor_set_in 解析 rpc_options.proto(依赖 descriptor.proto)时需要它;
    all.pb 不含,这里从 protobuf 运行时自带的描述符补上,免去 protoc --descriptor_set_out + cat。
    """
    out = descriptor_pb2.FileDescriptorSet()
    out.CopyFrom(fds)
    fdp = descriptor_pb2.FileDescriptorProto()
    descriptor_pb2.DESCRIPTOR.CopyToProto(fdp)
    out.file.append(fdp)
    return out.SerializeToString()
