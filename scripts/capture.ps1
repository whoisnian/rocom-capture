<#
.SYNOPSIS
    rocom-capture Windows 辅助抓包脚本(需 Npcap)

.DESCRIPTION
    列出可用网卡、列出命令行参数、或直接启动 rocom-capture 实时抓包。
    Npcap 安装: https://nmap.org/npcap/ (安装时勾选 "Install in WinPcap API-compatible Mode")

.PARAMETER List
    列出可用网卡(名称 + 描述 + IP)
.PARAMETER Iface
    网卡名或描述(不区分大小写子串匹配;不指定则自动选默认路由出口网卡)
.PARAMETER Port
    游戏服务器端口(默认 8195)
.PARAMETER Addr
    Web 服务监听地址(默认 :4939)
.PARAMETER Pcap
    离线 pcap 文件路径(回放模式,跳过实时抓包)
.PARAMETER Help
    显示帮助

.EXAMPLE
    .\scripts\capture.ps1 -List                      # 列出网卡
    .\scripts\capture.ps1 -Iface "以太网"            # 指定网卡
    .\scripts\capture.ps1 -Pcap ".\capture.pcap"      # 离线回放
#>
param(
    [switch]$List,
    [string]$Iface,
    [int]$Port = 8195,
    [string]$Addr = ":4939",
    [string]$Pcap,
    [switch]$Help
)

$ErrorActionPreference = "Stop"

# ---- 帮助 ----
if ($Help) {
    Get-Help $MyInvocation.MyCommand.Path -Detailed
    exit 0
}

# ---- 列出网卡 ----
function Show-NicList {
    Write-Host "`n===== 可用网卡 =====" -ForegroundColor Cyan
    $adapters = Get-NetAdapter | Where-Object { $_.Status -eq "Up" -or $_.Status -eq "Connected" }
    if ($adapters.Count -eq 0) {
        Write-Host "(无活跃网卡)" -ForegroundColor Yellow
        return
    }
    foreach ($nic in $adapters) {
        $ipObjs = Get-NetIPAddress -InterfaceIndex $nic.InterfaceIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue
        $ips = ($ipObjs | ForEach-Object { $_.IPAddress }) -join ", "
        if (-not $ips) { $ips = "(无 IPv4)" }
        Write-Host ("  [{0,-2}] {1,-16}  {2,-40}  IP: {3}" -f
            $nic.IfIndex, $nic.Name, $nic.InterfaceDescription, $ips)
    }
    Write-Host "======================`n"
}

if ($List) {
    Show-NicList
    exit 0
}

# ---- 检查 Npcap ----
function Test-Npcap {
    $npcapDll = Join-Path $env:SystemRoot "System32\Npcap\wpcap.dll"
    $winpcapDll = Join-Path $env:SystemRoot "System32\wpcap.dll"
    if (Test-Path $npcapDll -PathType Leaf) { return $true }
    if (Test-Path $winpcapDll -PathType Leaf) { return $true }
    return $false
}

if (-not (Test-Npcap)) {
    Write-Host "错误: 未找到 Npcap。请先安装 Npcap:" -ForegroundColor Red
    Write-Host "  https://nmap.org/npcap/"
    Write-Host "  安装时务必勾选 'Install in WinPcap API-compatible Mode'"
    exit 1
}

# ---- 查找 rocom-capture 二进制 ----
function Find-Binary {
    $candidates = @(
        ".\rocom-capture.exe",
        ".\dist\rocom-capture-windows-amd64.exe",
        (Join-Path $PSScriptRoot "..\rocom-capture.exe"),
        (Join-Path $PSScriptRoot "..\dist\rocom-capture-windows-amd64.exe")
    )
    foreach ($c in $candidates) {
        if (Test-Path $c -PathType Leaf) { return $c }
    }
    # 尝试 go build
    $bin = ".\rocom-capture.exe"
    Write-Host "未找到已编译二进制,尝试 go build..." -ForegroundColor Yellow
    $result = go build -o $bin .\cmd\rocom-capture 2>&1
    if ($LASTEXITCODE -ne 0) {
        Write-Host "go build 失败: $result" -ForegroundColor Red
        exit 1
    }
    return $bin
}

# ---- 自动选择网卡 ----
function Select-DefaultInterface {
    $route = Get-NetRoute -DestinationPrefix "0.0.0.0/0" -ErrorAction SilentlyContinue
    if ($route) {
        $nic = Get-NetAdapter -InterfaceIndex $route[0].InterfaceIndex -ErrorAction SilentlyContinue
        if ($nic) {
            Write-Host "自动选择网卡: $($nic.Name) ($($nic.InterfaceDescription))" -ForegroundColor Yellow
            return $nic.Name
        }
    }
    Write-Host "无法自动判断网卡,请用 -Iface 指定。" -ForegroundColor Red
    Show-NicList
    exit 1
}

# ---- 主逻辑: 回放模式 ----
if ($Pcap) {
    $bin = Find-Binary
    $args = @("-pcap", $Pcap, "-port", $Port.ToString(), "-addr", $Addr)
    Write-Host "离线回放: $Pcap" -ForegroundColor Green
    & $bin $args
    exit $LASTEXITCODE
}

# ---- 主逻辑: 实时抓包 ----
if (-not $Iface) {
    $Iface = Select-DefaultInterface
}

$bin = Find-Binary
$args = @("-iface", $Iface, "-port", $Port.ToString(), "-addr", $Addr)

Write-Host "==========================================" -ForegroundColor Green
Write-Host " 网卡     : $Iface" -ForegroundColor Green
Write-Host " 端口     : $Port/tcp" -ForegroundColor Green
Write-Host " 地址     : $Addr" -ForegroundColor Green
Write-Host " 提醒     : 请先进游戏触发连接,再启动抓包以捕获密钥" -ForegroundColor Yellow
Write-Host " 按 Ctrl-C 停止抓包" -ForegroundColor Yellow
Write-Host "==========================================" -ForegroundColor Green
Write-Host ""

& $bin $args
exit $LASTEXITCODE
