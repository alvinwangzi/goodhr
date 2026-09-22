# Purpose: build the GoodHR Go Local Agent executable for release or installer packaging.
param(
  [string]$TargetOS = "windows",
  [string]$TargetArch = "amd64",
  [string]$Version = "0.1.1",
  [string]$Environment = $env:GOODHR_APP_ENV,
  [string]$ConfigFile = ""
)

$ErrorActionPreference = "Stop"

$RootDir = Resolve-Path (Join-Path $PSScriptRoot "..")
if ($Environment -cnotin @("dev", "prod")) {
  throw "请通过 -Environment dev 或 -Environment prod 选择打包环境。"
}
if ($ConfigFile) {
  $ConfigFile = (Resolve-Path -LiteralPath $ConfigFile).Path
}

# Write-Step prints the current build step.
# message is the build step text.
function Write-Step {
  param([string]$message)
  Write-Host "[GoodHR] $message" -ForegroundColor Cyan
}

Write-Step "构建本地程序：环境=$Environment GOOS=$TargetOS GOARCH=$TargetArch"
$PreviousGOOS = $env:GOOS
$PreviousGOARCH = $env:GOARCH
$PreviousCGO = $env:CGO_ENABLED
Push-Location $RootDir
try {
  # 构建工具在当前系统运行，目标系统由参数传递给统一构建入口。
  $env:GOOS = go env GOHOSTOS
  $env:GOARCH = go env GOHOSTARCH
  $env:CGO_ENABLED = "0"
  $BuildArgs = @("run", "./cmd/build-local-agent", "-env", $Environment, "-os", $TargetOS, "-arch", $TargetArch, "-version", $Version)
  if ($ConfigFile) {
    $BuildArgs += @("-config", $ConfigFile)
  }
  & go @BuildArgs
  if ($LASTEXITCODE -ne 0) {
    throw "Go 构建失败，退出码：$LASTEXITCODE"
  }
}
finally {
  $env:GOOS = $PreviousGOOS
  $env:GOARCH = $PreviousGOARCH
  $env:CGO_ENABLED = $PreviousCGO
  Pop-Location
}
