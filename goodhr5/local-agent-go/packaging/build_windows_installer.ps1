# Purpose: build the HRPlus Local Agent and create the Windows installer.
param(
  [string]$Version = "0.1.4",
  [string]$Environment = $env:GOODHR_APP_ENV,
  [string]$ConfigFile = ""
)

$ErrorActionPreference = "Stop"
if ($Environment -cnotin @("dev", "prod")) {
  throw "请通过 -Environment dev 或 -Environment prod 选择安装包环境。"
}

$RootDir = Resolve-Path (Join-Path $PSScriptRoot "..")
$DistBinDir = Join-Path $RootDir "dist\bin\$Environment"
$DistInputDir = Join-Path $RootDir "dist\installer-input\$Environment"
$DistInstallerDir = Join-Path $RootDir "dist\installers\$Environment"

# Write-Step prints the current build step.
# message is the build step text.
function Write-Step {
  param([string]$message)
  Write-Host "[HRPlus] $message" -ForegroundColor Cyan
}

# 清理旧的构建产物，避免残留文件影响新包。
Write-Step "清理旧构建产物"
foreach ($dir in @($DistBinDir, $DistInputDir, $DistInstallerDir)) {
  $distBoundary = [System.IO.Path]::GetFullPath((Join-Path $RootDir 'dist')) + [System.IO.Path]::DirectorySeparatorChar
  $resolvedTarget = [System.IO.Path]::GetFullPath($dir)
  if (-not $resolvedTarget.StartsWith($distBoundary, [StringComparison]::OrdinalIgnoreCase)) { throw "构建清理目录不在工作区 dist 内：$resolvedTarget" }
  foreach ($checkedPath in @((Join-Path $RootDir 'dist'), (Split-Path -Parent $resolvedTarget), $resolvedTarget)) {
    if ((Test-Path -LiteralPath $checkedPath) -and ((Get-Item -LiteralPath $checkedPath).Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw "构建清理目录包含链接，停止清理：$checkedPath" }
  }
  if (Test-Path $dir) {
    Remove-Item -LiteralPath $resolvedTarget -Recurse -Force
    Write-Step "已删除: $dir"
  }
}
$ConsoleInputDir = Join-Path $DistInputDir "console"
$SourceExe = Join-Path $RootDir "dist\bin\$Environment\hrplus-agent-$Environment-windows-amd64.exe"
$TargetExe = Join-Path $DistInputDir "hrplus-agent.exe"
$IssPath = Join-Path $PSScriptRoot "GoodHRLocalAgentGo.iss"
$FrontendDir = Resolve-Path (Join-Path $RootDir "..\cloud\frontend-next")
$FrontendOutDir = Join-Path $FrontendDir "out"
if (-not $ConfigFile) { $ConfigFile = Join-Path $PSScriptRoot "environments\$Environment.json" }
$ConfigFile = (Resolve-Path -LiteralPath $ConfigFile).Path
$ConsoleConfigHash = (Get-FileHash -LiteralPath $ConfigFile -Algorithm SHA256).Hash
New-Item -ItemType Directory -Force -Path $DistBinDir | Out-Null
$BuildConfigSnapshot = Join-Path $DistBinDir 'console-build-config.json'
Copy-Item -LiteralPath $ConfigFile -Destination $BuildConfigSnapshot
if ($ConsoleConfigHash -ne (Get-FileHash -LiteralPath $BuildConfigSnapshot -Algorithm SHA256).Hash) { throw '环境配置快照不一致，停止构建。' }
$ConsoleBuildConfig = Get-Content -LiteralPath $BuildConfigSnapshot -Raw | ConvertFrom-Json
$ConsoleCloudAPIBase = [string]$ConsoleBuildConfig.cloud_api_base

# Find-InnoSetup locates the Inno Setup compiler.
# Returns the ISCC.exe path.
function Find-InnoSetup {
  $candidates = @(
    "ISCC.exe",
    "${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe",
    "$env:ProgramFiles\Inno Setup 6\ISCC.exe",
    "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe"
  )
  foreach ($candidate in $candidates) {
    if (Get-Command $candidate -ErrorAction SilentlyContinue) {
      return (Get-Command $candidate).Source
    }
    if (Test-Path $candidate) {
      return $candidate
    }
  }
  throw "Inno Setup compiler ISCC.exe was not found. Please install Inno Setup 6 first."
}

$iscc = Find-InnoSetup
if (!(Test-Path (Join-Path $RootDir "worker-node\node_modules"))) {
  throw "缺少 Worker 依赖，请先在 worker-node 目录安装依赖后再创建安装包。"
}
Write-Step "构建 Windows x64 本地程序：环境=$Environment"
$buildStartedAt = Get-Date
& (Join-Path $RootDir "scripts\build_go_binary.ps1") -TargetOS windows -TargetArch amd64 -Version $Version -Environment $Environment -ConfigFile $BuildConfigSnapshot
if ($LASTEXITCODE -ne 0) {
  throw "Go build script failed with exit code $LASTEXITCODE."
}
if (!(Test-Path $SourceExe)) {
  throw "Go executable was not found after build: $SourceExe"
}
if ((Get-Item $SourceExe).LastWriteTime -lt $buildStartedAt.AddSeconds(-2)) {
  throw "Go executable was not refreshed; refusing to package a stale file: $SourceExe"
}

Write-Step "构建本地控制台静态页面"
& (Join-Path $RootDir 'scripts\build_console.ps1') -CloudAPIBase $ConsoleCloudAPIBase
if ($ConsoleConfigHash -ne (Get-FileHash -LiteralPath $ConfigFile -Algorithm SHA256).Hash -or $ConsoleConfigHash -ne (Get-FileHash -LiteralPath $BuildConfigSnapshot -Algorithm SHA256).Hash) { throw '构建期间环境配置发生变化，请重新打包。' }
Write-Step "Prepare installer input directory"
New-Item -ItemType Directory -Force -Path $DistInputDir | Out-Null
Copy-Item -Force $SourceExe $TargetExe
if (Test-Path (Join-Path $RootDir "worker-node")) {
  Copy-Item -Recurse -Force (Join-Path $RootDir "worker-node") (Join-Path $DistInputDir "worker-node")
}
$sourceWorkerEntry = Join-Path $RootDir "worker-node\src\index.js"
$targetWorkerEntry = Join-Path $DistInputDir "worker-node\src\index.js"
if (!(Test-Path $sourceWorkerEntry) -or !(Test-Path $targetWorkerEntry)) {
  throw "Worker entry was not copied into installer input."
}
$sourceWorkerHash = (Get-FileHash -Algorithm SHA256 $sourceWorkerEntry).Hash
$targetWorkerHash = (Get-FileHash -Algorithm SHA256 $targetWorkerEntry).Hash
if ($sourceWorkerHash -ne $targetWorkerHash) {
  throw "Worker entry hash mismatch; refusing to package mixed versions."
}
if (-not (Select-String -LiteralPath $targetWorkerEntry -SimpleMatch 'const workerVersion = String(process.env.GOODHR_WORKER_VERSION || "").trim();' -Quiet)) {
  throw "Worker automatic version marker is missing from installer input."
}
Write-Step "Worker source verified: SHA256=$sourceWorkerHash"
New-Item -ItemType Directory -Force -Path $ConsoleInputDir | Out-Null
Get-ChildItem -LiteralPath $FrontendOutDir | ForEach-Object { Copy-Item -LiteralPath $_.FullName -Destination $ConsoleInputDir -Recurse -Force }
$consoleSourceHash = (Get-FileHash -LiteralPath (Join-Path $FrontendOutDir 'admin\execution-plans.html') -Algorithm SHA256).Hash
$consoleTargetHash = (Get-FileHash -LiteralPath (Join-Path $ConsoleInputDir 'admin\execution-plans.html') -Algorithm SHA256).Hash
if ($consoleSourceHash -ne $consoleTargetHash) { throw '执行计划页面复制校验失败，拒绝打包。' }
foreach ($consoleSourceFile in (Get-ChildItem -LiteralPath $FrontendOutDir -Recurse -File)) {
  $consoleRelativePath = $consoleSourceFile.FullName.Substring(([string]$FrontendOutDir).Length).TrimStart([IO.Path]::DirectorySeparatorChar)
  $consoleTargetFile = Join-Path $ConsoleInputDir $consoleRelativePath
  if (-not (Test-Path -LiteralPath $consoleTargetFile -PathType Leaf) -or (Get-FileHash -LiteralPath $consoleSourceFile.FullName -Algorithm SHA256).Hash -ne (Get-FileHash -LiteralPath $consoleTargetFile -Algorithm SHA256).Hash) { throw "控制台静态资源复制不一致：$consoleRelativePath" }
}
Write-Step '控制台全部静态文件复制校验通过'

Write-Step "创建 Windows 安装包：环境=$Environment"
& $iscc '/Q' "/DMyAppVersion=$Version" "/DBuildEnvironment=$Environment" $IssPath
if ($LASTEXITCODE -ne 0) {
  throw "Inno Setup build failed with exit code $LASTEXITCODE."
}

$InstallerOutputDir = Join-Path $RootDir "dist\installers\$Environment"
Write-Step "Installer build completed: $InstallerOutputDir"
