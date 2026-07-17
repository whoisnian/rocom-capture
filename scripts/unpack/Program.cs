// NrcUnpack:在 Linux 上复刻 FModel 手动导出流程(docs/data.md),从游戏 pak 直接批量解包。
//
// 输出布局与 FModel 完全一致(默认 ~/Downloads/NRC/Content/...),下游脚本零改动:
//   bin     ScriptC/Data/Bin/**            原始 .non/.bytes(BinConf/BinData*/BinLocalize)
//   pb      ScriptC/Data/PB/**             原始 all.pb / proto.non
//   icons   Common/Icon/{HeadIcon,BigHeadIcon256,Pet256,BagItem}  Texture2D → PNG
//   atlas   5 组图集:Frames/** → Save Properties JSON,Textures/** → PNG
//   bigmap  BigMap/Raw/Texture/{Maps,LayerMap}/**                 Texture2D → PNG
//   pet1024 Common/Icon/Pet1024(体积大,默认不导,--pet1024 开启)
//
// 依赖 ~/Git/gh/CUE4Parse(内置 GAME_RocoKingdomWorld:自定义 AES 变体/Bin/luac 支持,无需 usmap)。
// AES 主密钥必传(FModel 的 %AppData%/FModel/AppSettings.json → AesKeys 同一把)。

using System.Collections.Concurrent;
using System.Diagnostics;
using CUE4Parse.Compression;
using CUE4Parse.Encryption.Aes;
using CUE4Parse.FileProvider;
using CUE4Parse.FileProvider.Objects;
using CUE4Parse.FileProvider.Vfs;
using CUE4Parse.MappingsProvider.Usmap;
using CUE4Parse.UE4.Assets.Exports.Texture;
using CUE4Parse.UE4.Objects.Core.Misc;
using CUE4Parse.UE4.Versions;
using CUE4Parse_Conversion.Textures;
using Newtonsoft.Json;
using Serilog;
using Serilog.Events;

const string Usage = """
    用法: unpack.sh --paks <目录|.apk> --aes <64位hex|@文件> [选项]

    必选:
      --paks <path>     游戏 Paks 目录(递归扫描 *.pak/*.utoc),或安卓 .apk
      --aes <key>       AES 主密钥:64 位十六进制(可带 0x),或 @/path/to/key.txt
                        (与 Windows FModel AppSettings.json 里 AesKeys 同一把)

    选项:
      --out <dir>       输出根目录,默认 ~/Downloads/NRC(FModel 同款布局)
      -j <n>            并行度,默认 CPU 核数
      --only <cats>     只导出指定类别,逗号分隔: bin,pb,icons,atlas,bigmap,pet1024
      --pet1024         在默认类别之外追加 Pet1024 全身大图
      --raw <prefix>    额外原样导出 Content/ 下指定前缀(可重复,如 --raw NewRoco/Script)
      --usmap <path>    可选 .usmap 映射(当前版本无需)
      --force           覆盖已存在文件(默认跳过,增量导出)
      --list [substr]   只列出将导出的虚拟路径(可选子串过滤)后退出
    """;

string? paksPath = null, aesArg = null, usmapPath = null, listFilter = null;
var outDir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), "Downloads", "NRC");
var parallelism = Environment.ProcessorCount;
var force = false; var listOnly = false; var withPet1024 = false;
HashSet<string>? onlyCats = null;
var rawPrefixes = new List<string>();

for (var i = 0; i < args.Length; i++)
{
    switch (args[i])
    {
        case "--paks": paksPath = Next(ref i); break;
        case "--aes": aesArg = Next(ref i); break;
        case "--aes-file": aesArg = "@" + Next(ref i); break;
        case "--out": outDir = Path.GetFullPath(Next(ref i)); break;
        case "-j": parallelism = int.Parse(Next(ref i)); break;
        case "--only": onlyCats = Next(ref i).Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries).ToHashSet(StringComparer.OrdinalIgnoreCase); break;
        case "--pet1024": withPet1024 = true; break;
        case "--raw": rawPrefixes.Add(Next(ref i).TrimStart('/')); break;
        case "--usmap": usmapPath = Next(ref i); break;
        case "--force": force = true; break;
        case "--list":
            listOnly = true;
            if (i + 1 < args.Length && !args[i + 1].StartsWith("--")) listFilter = args[++i];
            break;
        case "-h" or "--help": Console.WriteLine(Usage); return 0;
        default: return Fail($"未知参数: {args[i]}\n{Usage}");
    }
}

if (paksPath is null) return Fail($"缺少 --paks\n{Usage}");
if (aesArg is null) return Fail($"缺少 --aes\n{Usage}");

var aesKey = aesArg.StartsWith('@') ? File.ReadAllText(aesArg[1..]).Trim() : aesArg;
aesKey = string.Concat(aesKey.Where(c => !char.IsWhiteSpace(c)));
var aesHex = aesKey.StartsWith("0x", StringComparison.OrdinalIgnoreCase) ? aesKey[2..] : aesKey;
if (aesHex.Length != 64 || !aesHex.All(Uri.IsHexDigit))
    return Fail($"AES 密钥须为 64 位十六进制,拿到 {aesHex.Length} 位");

Log.Logger = new LoggerConfiguration().MinimumLevel.Is(LogEventLevel.Warning).WriteTo.Console().CreateLogger();
// Detex/oodle 原生库是 Windows dll;托管解码器(AssetRipper)全平台可用,PC 端 BC/DXT 与安卓 ASTC 都覆盖
TextureDecoder.UseAssetRipperTextureDecoder = true;

// oodle/zlib-ng 原生解压库:缺则自动下载到 ~/.cache/nrc-unpack(pak 未用到时失败也不致命)
var cacheDir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), ".cache", "nrc-unpack");
Directory.CreateDirectory(cacheDir);
try { OodleHelper.Initialize(Path.Combine(cacheDir, OodleHelper.OodleFileName)); }
catch (Exception e) { Console.Error.WriteLine($"[warn] Oodle 初始化失败(若 pak 未用 oodle 压缩可忽略): {e.Message}"); }
try { ZlibHelper.Initialize(Path.Combine(cacheDir, ZlibHelper.DllName)); }
catch (Exception e) { Console.Error.WriteLine($"[warn] zlib-ng 初始化失败(若 pak 未用 zlib 压缩可忽略): {e.Message}"); }

var version = new VersionContainer(EGame.GAME_RocoKingdomWorld);
AbstractVfsFileProvider provider;
if (File.Exists(paksPath) && paksPath.EndsWith(".apk", StringComparison.OrdinalIgnoreCase))
    provider = new ApkFileProvider(paksPath, versions: version, pathComparer: StringComparer.OrdinalIgnoreCase);
else if (Directory.Exists(paksPath))
    provider = new DefaultFileProvider(paksPath, SearchOption.AllDirectories, version, StringComparer.OrdinalIgnoreCase);
else
    return Fail($"--paks 路径不存在: {paksPath}");

if (usmapPath is not null)
    provider.MappingsContainer = new FileUsmapTypeMappingsProvider(usmapPath);

var sw = Stopwatch.StartNew();
provider.Initialize();
provider.SubmitKey(new FGuid(), new FAesKey(aesHex));
if (provider.Files.Count == 0)
    return Fail("挂载后没有任何文件:检查 --paks 目录是否含 pak、AES 密钥是否正确");
Console.WriteLine($"挂载 {provider.MountedVfs.Count} 个包,{provider.Files.Count} 个文件({sw.ElapsedMilliseconds}ms)");

// ── 导出清单:与 FModel 手动流程(docs/data.md)一一对应 ──────────────
var iconRoot = "NewRoco/Modules/System/Common/Icon/";
var atlasRoots = new[]
{
    "NewRoco/Modules/System/Common/Icon/Species/",
    "NewRoco/Modules/System/Common/Icon/XueMai/",
    "NewRoco/Modules/System/Common/CommonStatic/",
    "NewRoco/Modules/System/PetUI/Raw/Atlas/PetUI/",
    "NewRoco/Modules/System/BigMap/Raw/Atlas/WorldMapNpc/",
};
var rules = new List<(string Cat, string Prefix, Kind Kind)>
{
    ("bin", "ScriptC/Data/Bin/", Kind.Raw),
    ("pb", "ScriptC/Data/PB/", Kind.Raw),
    ("icons", iconRoot + "HeadIcon/", Kind.Png),
    ("icons", iconRoot + "BigHeadIcon256/", Kind.Png),
    ("icons", iconRoot + "Pet256/", Kind.Png),
    ("icons", iconRoot + "BagItem/", Kind.Png),
    ("pet1024", iconRoot + "Pet1024/", Kind.Png),
    ("bigmap", "NewRoco/Modules/System/BigMap/Raw/Texture/Maps/", Kind.Png),
    ("bigmap", "NewRoco/Modules/System/BigMap/Raw/Texture/LayerMap/", Kind.Png),
};
rules.AddRange(atlasRoots.Select(r => ("atlas", r + "Frames/", Kind.Json)));
rules.AddRange(atlasRoots.Select(r => ("atlas", r + "Textures/", Kind.Png)));
rules.AddRange(rawPrefixes.Select(p => ("raw", p, Kind.Raw)));

var active = onlyCats ?? ["bin", "pb", "icons", "atlas", "bigmap"];
if (withPet1024) active.Add("pet1024");
if (rawPrefixes.Count > 0) active.Add("raw");
rules.RemoveAll(r => !active.Contains(r.Cat));

// ── 扫描虚拟文件系统,生成任务 ─────────────────────────────────────
// Files.Values 枚举所有已挂载 pak 的索引:补丁包(_P)与基础包的同名文件都会出现,
// 须按路径去重,并经 Files[path] 索引器(按 readOrder 降序)取补丁优先的胜者。
var jobs = new List<(GameFile File, string Cat, Kind Kind, string OutPath)>();
var seen = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
var skipped = 0;
foreach (var candidate in provider.Files.Values)
{
    var idx = candidate.Path.IndexOf("/Content/", StringComparison.OrdinalIgnoreCase);
    if (idx < 0) continue;
    var rel = candidate.Path[(idx + "/Content/".Length)..];
    foreach (var rule in rules)
    {
        if (!rel.StartsWith(rule.Prefix, StringComparison.OrdinalIgnoreCase)) continue;
        // PB 下混着 lua 字节码(ProtoMgr.luac 等):非描述符用不上,且其 luac 解密路径报错,跳过
        if (rule.Cat == "pb" && rel.EndsWith(".luac", StringComparison.OrdinalIgnoreCase)) break;
        var ext = rule.Kind switch { Kind.Png => ".png", Kind.Json => ".json", _ => null };
        if (ext is not null && !candidate.Path.EndsWith(".uasset", StringComparison.OrdinalIgnoreCase)) break; // uexp/ubulk 随包体自动读取
        if (!seen.Add(candidate.Path)) break;
        var file = provider.Files[candidate.Path];
        var outPath = Path.Combine(outDir, "Content", ext is null ? rel : Path.ChangeExtension(rel, ext));
        if (!force && File.Exists(outPath)) skipped++;
        else jobs.Add((file, rule.Cat, rule.Kind, outPath));
        break;
    }
}

if (listOnly)
{
    foreach (var j in jobs.Where(j => listFilter is null || j.File.Path.Contains(listFilter, StringComparison.OrdinalIgnoreCase)).OrderBy(j => j.File.Path))
        Console.WriteLine($"{j.Cat,-7} {j.Kind,-4} {j.File.Path}");
    Console.WriteLine($"共 {jobs.Count} 项(另有 {skipped} 项已存在将跳过)");
    return 0;
}

Console.WriteLine($"待导出 {jobs.Count} 项,已存在跳过 {skipped} 项,并行度 {parallelism}");
var done = 0;
var noTexture = 0;
var okByCat = new ConcurrentDictionary<string, int>();
var errors = new ConcurrentBag<string>();
sw.Restart();

Parallel.ForEach(jobs, new ParallelOptions { MaxDegreeOfParallelism = parallelism }, job =>
{
    try
    {
        var exported = true;
        Directory.CreateDirectory(Path.GetDirectoryName(job.OutPath)!);
        switch (job.Kind)
        {
            case Kind.Raw:
                File.WriteAllBytes(job.OutPath, job.File.Read());
                break;
            case Kind.Json:
                var exports = provider.LoadPackage(job.File).GetExports().ToList();
                File.WriteAllText(job.OutPath, JsonConvert.SerializeObject(exports, Formatting.Indented));
                break;
            case Kind.Png:
                var textures = provider.LoadPackage(job.File).GetExports().OfType<UTexture>().ToList();
                if (textures.Count == 0) { Interlocked.Increment(ref noTexture); exported = false; break; } // 非纹理包(如 BagItem 相框),FModel 对其 Save Texture 同样不可用
                foreach (var (tex, n) in textures.Select((t, n) => (t, n)))
                {
                    var decoded = tex.Decode() ?? throw new InvalidOperationException($"纹理解码失败({tex.Format})");
                    FixBc7ChannelOrder(tex, decoded);
                    // 单纹理包(常态)用包名;罕见多纹理包时后续项以导出名附加,避免互相覆盖
                    var path = n == 0 ? job.OutPath : Path.ChangeExtension(job.OutPath, $"{tex.Name}.png");
                    File.WriteAllBytes(path, decoded.Encode(ETextureFormat.Png, false, out _));
                }
                break;
        }
        if (exported) okByCat.AddOrUpdate(job.Cat, 1, (_, v) => v + 1);
    }
    catch (Exception e)
    {
        errors.Add($"{job.File.Path}: {e.Message}");
    }
    var d = Interlocked.Increment(ref done);
    if (d % 500 == 0) Console.WriteLine($"  ... {d}/{jobs.Count}");
});

Console.WriteLine($"完成({sw.Elapsed.TotalSeconds:F1}s): " +
                  string.Join(", ", okByCat.OrderBy(kv => kv.Key).Select(kv => $"{kv.Key} {kv.Value}")) +
                  $";跳过 {skipped},非纹理包 {noTexture},失败 {errors.Count}");
foreach (var err in errors.Take(20)) Console.Error.WriteLine($"[err] {err}");
if (errors.Count > 20) Console.Error.WriteLine($"[err] ... 共 {errors.Count} 个失败");
return errors.IsEmpty ? 0 : 2;

string Next(ref int i)
{
    if (i + 1 >= args.Length) { Console.Error.WriteLine($"{args[i]} 缺少参数值\n{Usage}"); Environment.Exit(1); }
    return args[++i];
}

static int Fail(string msg)
{
    Console.Error.WriteLine(msg);
    return 1;
}

// 上游 bug 修正:TextureDecoder 的 PF_BC7 在 AssetRipper 托管分支里用 ColorRGBA 解码,
// 却把 colorType 标成 PF_B8G8R8A8(那是 Detex 原生分支的字节序),R/B 全图对调。
// 双重条件防御:上游若改 colorType 修复,此处自动失效;若改成 ColorBGRA 修复则需删掉本函数。
static void FixBc7ChannelOrder(UTexture tex, CTexture decoded)
{
    if (!TextureDecoder.UseAssetRipperTextureDecoder) return;
    if (tex.Format != CUE4Parse.UE4.Assets.Exports.Texture.EPixelFormat.PF_BC7) return;
    if (decoded.PixelFormat != CUE4Parse.UE4.Assets.Exports.Texture.EPixelFormat.PF_B8G8R8A8) return;
    var d = decoded.Data;
    for (var i = 0; i + 3 < d.Length; i += 4)
        (d[i], d[i + 2]) = (d[i + 2], d[i]);
}

internal enum Kind { Raw, Png, Json }
