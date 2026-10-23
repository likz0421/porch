# =============================================================================
#  Porch 门廊 · Windows 一键在线安装脚本
#
#  用法（PowerShell 5.1 或 7+）:
#    irm https://raw.githubusercontent.com/likz0421/porch/main/install.ps1 | iex
#    .\install.ps1 -Version 1.1.0
#    .\install.ps1 -File .\porch_1.1.0_windows_x86_64.zip   离线安装（不联网）
#    .\install.ps1 -Check                只做环境检测
#    .\install.ps1 -Uninstall            卸载（加 -Purge 连配置一起删）
#
#  设计原则: 与 Linux/macOS 的 install.sh 行为一致——镜像重试、校验和、
#            装完实跑一次 porch.exe -v 才算成功、升级保留配置。
# =============================================================================
#Requires -Version 5.1
[CmdletBinding()]
param(
    [string]$Version = "",
    [string]$Dir = "",
    [int]$Port = 0,
    [string]$Mirror = "",
    [string]$File = "",
    [switch]$NoPath,
    [switch]$Uninstall,
    [switch]$Purge,
    [switch]$Force,
    [switch]$Yes,
    [switch]$Check
)

$ErrorActionPreference = 'Stop'
# 进度条会在非交互/重定向输出时刷出一堆 ANSI 转义垃圾，而且拖慢 Invoke-WebRequest
$ProgressPreference = 'SilentlyContinue'
$Repo = if ($env:PORCH_REPO) { $env:PORCH_REPO } else { 'likz0421/porch' }
$BinName = 'porch'
$DefaultPort = 16601

# ---------------------------- 输出 ----------------------------
function Write-Step($msg) { Write-Host ""; Write-Host "==> $msg" -ForegroundColor Cyan }
function Write-Info($msg) { Write-Host "  $msg" }
function Write-Ok($msg)   { Write-Host "  [OK] $msg" -ForegroundColor Green }
function Write-Warn2($msg) { Write-Host "  [!] $msg" -ForegroundColor Yellow }
function Fail($msg, $hint) {
    Write-Host ""
    Write-Host "  [X] $msg" -ForegroundColor Red
    if ($hint) { Write-Host "      建议: $hint" -ForegroundColor Yellow }
    exit 1
}

# ---------------------------- 环境 ----------------------------
$IsAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator)

$ArchRaw = $env:PROCESSOR_ARCHITECTURE
switch ($ArchRaw) {
    'AMD64' { $Arch = 'x86_64' }
    'x86'   { $Arch = 'i386' }
    'ARM64' {
        $Arch = 'x86_64'
        Write-Warn2 '检测到 Windows on ARM，将安装 x86_64 版本（系统自带 x64 模拟）'
    }
    default { Fail "不支持的 CPU 架构: $ArchRaw" '请在 issue 里附上 echo $env:PROCESSOR_ARCHITECTURE 的输出' }
}

if (-not $Dir) { $Dir = Join-Path $env:LOCALAPPDATA 'Porch' }
$BinPath = Join-Path $Dir "$BinName.exe"
$ConfDir = Join-Path $env:APPDATA 'porch\porchconf'

try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 -bor [Net.SecurityProtocolType]::Tls11
} catch { }

# ---------------------------- 工具 ----------------------------
function Get-LatestTag {
    $api = "https://api.github.com/repos/$Repo/releases/latest"

    # 1) 官方 API（最准）
    try {
        $r = Invoke-RestMethod -Uri $api -UseBasicParsing -TimeoutSec 15 -Headers @{ 'User-Agent' = 'porch-installer' }
        if ($r.tag_name) { return $r.tag_name }
    } catch { }

    # 2) 走公共加速再问一次 API
    foreach ($p in @('https://ghfast.top/', 'https://gh-proxy.com/', 'https://ghproxy.net/')) {
        try {
            $r = Invoke-RestMethod -Uri "$p$api" -UseBasicParsing -TimeoutSec 20 -Headers @{ 'User-Agent' = 'porch-installer' }
            if ($r.tag_name) { return $r.tag_name }
        } catch { }
    }

    # 3) 兜底：不发 API 配额，直接看 releases/latest 的 302 指向哪个 tag
    #    （老版本 PS 的 Response.Headers 在异常里可能是 null，索引会抛
    #     "无法对 Null 数组进行索引"，所以这里层层判空）
    try {
        $req = [System.Net.HttpWebRequest]::Create("https://github.com/$Repo/releases/latest")
        $req.AllowAutoRedirect = $false
        $req.Timeout = 15000
        $req.UserAgent = 'porch-installer'
        $resp = $req.GetResponse()
        $loc = $resp.Headers['Location']
        $resp.Close()
        if ($loc -and $loc -match '/tag/(.+)$') { return $Matches[1] }
    } catch {
        $r2 = $_.Exception.Response
        if ($r2 -and $r2.Headers) {
            $loc = $r2.Headers['Location']
            if ($loc -and $loc -match '/tag/(.+)$') { return $Matches[1] }
        }
    }
    return $null
}

function Get-MirrorList {
    if ($Mirror) { return $Mirror -split ',' }
    return @('', 'https://ghfast.top/', 'https://gh-proxy.com/', 'https://ghproxy.net/')
}

function Save-File($Url, $Dest) {
    $tmp = "$Dest.part"
    if (Test-Path $tmp) { Remove-Item $tmp -Force }
    Invoke-WebRequest -Uri $Url -OutFile $tmp -UseBasicParsing -TimeoutSec 300 -Headers @{ 'User-Agent' = 'porch-installer' }
    Move-Item -Force $tmp $Dest
}

function Get-Asset($Name, $Dest) {
    $base = "https://github.com/$Repo/releases/download"
    foreach ($prefix in (Get-MirrorList)) {
        $url = "$prefix$base/$Tag/$Name"
        $srvHost = ([Uri]$url).Host
        for ($i = 1; $i -le 3; $i++) {
            try {
                Write-Info "下载 $Name  <- $srvHost （第 $i 次）"
                Save-File $url $Dest
                Write-Ok "下载完成（$([math]::Round((Get-Item $Dest).Length / 1MB, 2)) MB）"
                return $true
            } catch {
                Write-Warn2 "$srvHost 第 $i 次失败: $($_.Exception.Message.Split("`n")[0])"
                Start-Sleep -Seconds $i
            }
        }
        Write-Warn2 "镜像 $srvHost 不可用，换下一个"
    }
    return $false
}

# ---------------------------- 卸载 ----------------------------
if ($Uninstall) {
    Write-Step '卸载 Porch'
    Get-Process $BinName -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    if (Test-Path $BinPath) { Remove-Item $BinPath -Force; Write-Ok "已删除 $BinPath" }

    # 从用户 PATH 里摘掉安装目录
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($userPath -and $userPath -like "*$Dir*") {
        $newPath = ($userPath -split ';' | Where-Object { $_ -and $_.TrimEnd('\') -ne $Dir.TrimEnd('\') }) -join ';'
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        Write-Ok '已从用户 PATH 移除'
    }
    if (Test-Path $Dir) {
        $left = Get-ChildItem $Dir -ErrorAction SilentlyContinue
        if (-not $left -or $Purge) { Remove-Item $Dir -Recurse -Force -ErrorAction SilentlyContinue; Write-Ok "已删除 $Dir" }
        else { Write-Info "$Dir 里还有其它文件（配置/日志），保留" }
    }
    if ($Purge -and (Test-Path (Split-Path $ConfDir -Parent))) {
        Remove-Item (Split-Path $ConfDir -Parent) -Recurse -Force -ErrorAction SilentlyContinue
        Write-Ok '已删除配置目录'
    } elseif (Test-Path (Split-Path $ConfDir -Parent)) {
        Write-Info "配置保留在 $(Split-Path $ConfDir -Parent)（加 -Purge 一并删除）"
    }
    Write-Host ""
    Write-Host '卸载完成。' -ForegroundColor Green
    exit 0
}

# ---------------------------- 检测输出 ----------------------------
Write-Step '环境检测'
Write-Info "系统: Windows $([Environment]::OSVersion.Version)  架构: $ArchRaw -> $Arch"
Write-Info "用户: $env:USERNAME$(if ($IsAdmin) { '（管理员）' } else { '（普通用户）' })"
Write-Info "程序目录: $Dir"
Write-Info "配置目录: $ConfDir"
Write-Info "PowerShell: $($PSVersionTable.PSVersion)"

$CurVer = $null
if (Test-Path $BinPath) {
    try {
        $v = & $BinPath -v 2>$null
        if ($v -match 'porch (\S+)') { $CurVer = $Matches[1] }
        Write-Info "已安装版本: $CurVer"
    } catch { }
}

if ($Check) {
    Write-Step '可用版本'
    $t = Get-LatestTag
    if ($t) { Write-Ok "最新版本: $t" } else { Write-Warn2 '无法获取最新版本（网络受限？可用 -Version 指定）' }
    Write-Host ""
    Write-Host '环境检测完成，未做任何修改。' -ForegroundColor DarkGray
    exit 0
}

# ---------------------------- 版本 ----------------------------
$LocalPkg = $false
$PkgName = ""
if ($File) {
    if (-not (Test-Path $File)) { Fail "找不到本地安装包: $File" '确认路径拼写；相对路径以当前目录为准' }
    $File = (Resolve-Path $File).Path
    $LocalPkg = $true
    $PkgName = Split-Path $File -Leaf
    if ($PkgName -match '^porch_([0-9][0-9A-Za-z.]*)_windows_([A-Za-z0-9_]+)\.zip$') {
        $Ver = $Matches[1]
        $fArch = $Matches[2]
        if ($fArch -ne $Arch) {
            Write-Warn2 "安装包架构是 $fArch，本机识别为 $Arch"
            Write-Info '按安装包的架构继续（-File 以文件名为准）'
            $Arch = $fArch
        }
    } elseif ($Version) {
        $Ver = $Version.TrimStart('v')
    } else {
        Fail "无法从文件名识别版本: $PkgName" '文件名应形如 porch_1.1.0_windows_x86_64.zip；或用 -Version 指定'
    }
    $Tag = "v$Ver"
    $Asset = $PkgName
    Write-Ok "离线安装: $PkgName（版本 $Ver，架构 $Arch）"
} elseif ($Version -and $Version -ne 'latest') {
    $Tag = if ($Version.StartsWith('v')) { $Version } else { "v$Version" }
} else {
    Write-Step '获取最新版本'
    $Tag = Get-LatestTag
    if (-not $Tag) {
        # 已经装好了就别因为查不到版本号而报错退出——用户本来只是想确认/升级
        if ($CurVer) {
            Write-Warn2 "查不到最新版本（网络受限）；当前已安装 $CurVer，未做任何修改"
            Write-Info '要装指定版本:  .\install.ps1 -Version 1.1.0'
            exit 0
        }
        Fail '拿不到最新版本号' '网络可能被限制：用 -Version 1.1.0 指定，或用 -Mirror 换镜像'
    }
    Write-Ok "最新版本: $Tag"
}
if (-not $LocalPkg) {
    $Ver = $Tag.TrimStart('v')
    $Asset = "${BinName}_${Ver}_windows_${Arch}.zip"
}

if ($CurVer -eq $Ver -and -not $Force) {
    Write-Ok "已是 $Ver，无需重复安装（重装加 -Force）"
    exit 0
}

Write-Info "目标版本: v$Ver  目标包: $Asset"

# ---------------------------- 下载 ----------------------------
$Work = Join-Path $env:TEMP ("porch-install-" + [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Path $Work -Force | Out-Null

try {
    if ($LocalPkg) {
        Write-Step '读取本地安装包'
        Copy-Item $File (Join-Path $Work $Asset) -Force
        Write-Ok "已读取 $PkgName（$([math]::Round((Get-Item $File).Length / 1MB, 2)) MB）"
    } else {
        Write-Step '下载'
        if (-not (Get-Asset $Asset (Join-Path $Work $Asset))) {
            Fail "所有镜像都没能下载到 $Asset" '1) 检查网络/代理 2) -Mirror "https://ghfast.top/" 3) -Version 指定已发布版本'
        }
    }

    Write-Step '校验'
    $sums = Join-Path $Work 'SHA256SUMS'
    if ($LocalPkg) {
        $localSums = Join-Path (Split-Path $File -Parent) 'SHA256SUMS'
        if (Test-Path $localSums) { Copy-Item $localSums $sums -Force } else { Write-Warn2 '本地包旁边没有 SHA256SUMS，跳过完整性校验' }
    }
    if (Test-Path $sums) {
        $want = $null
        foreach ($line in Get-Content $sums) {
            $p = $line -split '\s+'
            if ($p.Count -ge 2 -and $p[-1].TrimStart('*') -eq $Asset) { $want = $p[0]; break }
        }
        if ($want) {
            $got = (Get-FileHash (Join-Path $Work $Asset) -Algorithm SHA256).Hash.ToLower()
            if ($got -ne $want.ToLower()) { Fail "校验和不匹配: $Asset" '文件可能被篡改或下载不完整，请重试' }
            Write-Ok "校验和一致 $($want.Substring(0, 8))..."
        } else { Write-Warn2 'SHA256SUMS 里没有该资产，跳过校验' }
    }

    Write-Step '安装'
    Get-Process $BinName -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 400
    New-Item -ItemType Directory -Path $Dir -Force | Out-Null
    Expand-Archive -Path (Join-Path $Work $Asset) -DestinationPath $Work -Force
    $src = Get-ChildItem $Work -Recurse -Filter "$BinName.exe" | Select-Object -First 1
    if (-not $src) { Fail '压缩包里没找到 porch.exe' '请提 issue 附上资产名' }
    Copy-Item $src.FullName $BinPath -Force
    Write-Ok "$(if ($CurVer) { "已升级: $CurVer -> $Ver" } else { "已安装: $Ver" })  ->  $BinPath"

    # 实跑一次确认能起来（顺带验证架构）
    $out = & $BinPath -v
    if ($LASTEXITCODE -ne 0 -or $out -notmatch 'porch') { Fail "程序无法运行: $out" '请把输出提到 issue' }
    Write-Ok "自检通过: $out"

    New-Item -ItemType Directory -Path $ConfDir -Force | Out-Null

    if (-not $NoPath) {
        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        if (-not $userPath -or $userPath -notlike "*$Dir*") {
            $newPath = if ($userPath) { "$userPath;$Dir" } else { $Dir }
            [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
            Write-Ok "已加入用户 PATH（新开的终端生效）"
        }
        $env:Path = "$env:Path;$Dir"
    }
} finally {
    Remove-Item $Work -Recurse -Force -ErrorAction SilentlyContinue
}

# ---------------------------- 收尾 ----------------------------
Write-Step '完成'
Write-Host ""
Write-Host '  Porch 已就绪' -ForegroundColor Green
Write-Host ''
$ShowPort = if ($Port -gt 0) { $Port } else { $DefaultPort }
Write-Host "  管理界面   http://127.0.0.1:$ShowPort"
Write-Host "  默认账号   666 / 666   （登录后第一件事：系统设置 -> 改掉密码）" -ForegroundColor Yellow
Write-Host "  程序       $BinPath"
Write-Host "  配置目录   $ConfDir"
Write-Host ''
Write-Host '  启动方式'
Write-Host "    $BinPath -cd `"$ConfDir`"          前台运行（关窗口即停止）"
Write-Host "    $BinPath -cd `"$ConfDir`" -open    启动并自动打开浏览器"
Write-Host ''
Write-Host '  开机自启（任选其一）'
Write-Host "    1) 把快捷方式放进 shell:startup"
Write-Host "    2) 用 NSSM / WinSW 注册成 Windows 服务"
Write-Host ''
Write-Host '  升级  重新执行本脚本即可（配置自动保留）'
Write-Host '  卸载  .\install.ps1 -Uninstall         （加 -Purge 连配置一起删）'
Write-Host ''
Write-Host '  下一步（不用看文档也能走完）'
Write-Host '    1. 打开上面的地址，登录后到「动态域名」加一条任务'
Write-Host '    2. 「SSL 证书」里申请一张证书（需要 80 端口能从公网访问）'
Write-Host '    3. 「反向代理」把内网服务映射成一个能在外网打开的网址'
Write-Host ''
