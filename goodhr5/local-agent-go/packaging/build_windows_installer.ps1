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
  if (Test-Path $dir) {
    Remove-Item -Recurse -Force $dir
    Write-Step "已删除: $dir"
  }
}
# $ConsoleInputDir = Join-Path $DistInputDir "console"
$SourceExe = Join-Path $RootDir "dist\bin\$Environment\hrplus-agent-$Environment-windows-amd64.exe"
$TargetExe = Join-Path $DistInputDir "hrplus-agent.exe"
$IssPath = Join-Path $PSScriptRoot "GoodHRLocalAgentGo.iss"
# 暂时不把 frontend-next 打进本地程序安装包，避免前端构建影响本地程序打包。
# $FrontendDir = Resolve-Path (Join-Path $RootDir "..\cloud\frontend-next")
# $FrontendOutDir = Join-Path $FrontendDir "out"

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

# Find-Npm locates npm.cmd to avoid PowerShell execution policy issues.
# Returns the npm.cmd path.
function Find-Npm {
  $npm = Get-Command "npm.cmd" -ErrorAction SilentlyContinue
  if ($npm) {
    return $npm.Source
  }
  $candidates = @(
    "$env:ProgramFiles\nodejs\npm.cmd",
    "${env:ProgramFiles(x86)}\nodejs\npm.cmd",
    "$env:APPDATA\npm\nnpm.cmd"
  )
  foreach ($candidate in $candidates) {
    if (Test-Path $candidate) {
      return $candidate
    }
  }
  throw "npm.cmd was not found. Please reinstall Node.js LTS or reopen PowerShell after installation."
}

# Ensure-NodeOnPath makes node.exe visible to npm child scripts.
# npm install may call "node install.js", so node.exe must be on PATH.
function Ensure-NodeOnPath {
  if (Get-Command "node.exe" -ErrorAction SilentlyContinue) {
    return
  }
  $candidates = @(
    "$env:ProgramFiles\nodejs\node.exe",
    "${env:ProgramFiles(x86)}\nodejs\node.exe"
  )
  foreach ($candidate in $candidates) {
    if (Test-Path $candidate) {
      $nodeDir = Split-Path $candidate -Parent
      $env:Path = "$nodeDir;$env:Path"
      Write-Step "Node added to PATH: $nodeDir"
      return
    }
  }
  throw "node.exe was not found. Please reinstall Node.js LTS or reopen PowerShell after installation."
}

$iscc = Find-InnoSetup
if (!(Test-Path (Join-Path $RootDir "worker-node\node_modules"))) {
  throw "缺少 Worker 依赖，请先在 worker-node 目录安装依赖后再创建安装包。"
}
Write-Step "构建 Windows x64 本地程序：环境=$Environment"
$buildStartedAt = Get-Date
& (Join-Path $RootDir "scripts\build_go_binary.ps1") -TargetOS windows -TargetArch amd64 -Version $Version -Environment $Environment -ConfigFile $ConfigFile
if ($LASTEXITCODE -ne 0) {
  throw "Go build script failed with exit code $LASTEXITCODE."
}
if (!(Test-Path $SourceExe)) {
  throw "Go executable was not found after build: $SourceExe"
}
if ((Get-Item $SourceExe).LastWriteTime -lt $buildStartedAt.AddSeconds(-2)) {
  throw "Go executable was not refreshed; refusing to package a stale file: $SourceExe"
}

# Write-Step "Build local console frontend"
# $npm = Find-Npm
# Ensure-NodeOnPath
# Push-Location $FrontendDir
# try {
#   if (!(Test-Path (Join-Path $FrontendDir "node_modules"))) {
#     Write-Step "Install frontend dependencies"
#     & $npm install
#     if ($LASTEXITCODE -ne 0) {
#       throw "Frontend npm install failed with exit code $LASTEXITCODE."
#     }
#   }
#   $env:GOODHR_STATIC_EXPORT = "1"
#   & $npm run build
#   if ($LASTEXITCODE -ne 0) {
#     throw "Frontend build failed with exit code $LASTEXITCODE."
#   }
#   if (!(Test-Path (Join-Path $FrontendOutDir "index.html"))) {
#     throw "Frontend static export output was not found: $FrontendOutDir"
#   }
# }
# finally {
#   Remove-Item Env:\GOODHR_STATIC_EXPORT -ErrorAction SilentlyContinue
#   Pop-Location
# }

Write-Step "Prepare installer input directory"
New-Item -ItemType Directory -Force -Path $DistInputDir | Out-Null
Copy-Item -Force $SourceExe $TargetExe
if (Test-Path (Join-Path $RootDir "worker-node")) {
  Remove-Item -Recurse -Force (Join-Path $DistInputDir "worker-node") -ErrorAction SilentlyContinue
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
# Remove-Item -Recurse -Force $ConsoleInputDir -ErrorAction SilentlyContinue
# New-Item -ItemType Directory -Force -Path $ConsoleInputDir | Out-Null
# Copy-Item -Recurse -Force (Join-Path $FrontendOutDir "*") $ConsoleInputDir

Write-Step "创建 Windows 安装包：环境=$Environment"
& $iscc "/DMyAppVersion=$Version" "/DBuildEnvironment=$Environment" $IssPath
if ($LASTEXITCODE -ne 0) {
  throw "Inno Setup build failed with exit code $LASTEXITCODE."
}

$InstallerOutputDir = Join-Path $RootDir "dist\installers\$Environment"
Write-Step "Installer build completed: $InstallerOutputDir"
