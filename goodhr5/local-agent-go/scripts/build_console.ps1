# 本文件构建 HRPlus 本地控制台静态页面，固定公开后端地址，验证计划路由，不安装或下载依赖。
param(
  [Parameter(Mandatory = $true)][string]$CloudAPIBase
)

$ErrorActionPreference = 'Stop'
$CloudAPIBase = $CloudAPIBase.Trim().TrimEnd('/')
$FrontendDir = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..\cloud\frontend-next'))
$ApiUri = $null
if (-not [Uri]::TryCreate($CloudAPIBase, [UriKind]::Absolute, [ref]$ApiUri) -or $ApiUri.Scheme -notin @('http','https') -or $ApiUri.UserInfo -or $ApiUri.Query -or $ApiUri.Fragment) {
  throw '控制台后端地址必须是公开 HTTP/HTTPS 地址，不得包含登录信息、查询参数或片段。'
}
$NextCommand = Join-Path $FrontendDir 'node_modules\.bin\next.cmd'
if (-not (Test-Path -LiteralPath $NextCommand)) {
  throw '缺少已安装的前端依赖。请先按开发环境说明配置依赖及下载代理，再重新构建。'
}
if (-not (Get-Command 'node.exe' -ErrorAction SilentlyContinue)) { throw '未找到 Node.js，请先配置已安装的 Node.js 路径。' }
$PriorStatic = $env:GOODHR_STATIC_EXPORT
$PriorCloud = $env:NEXT_PUBLIC_CLOUD_API_BASE
$PriorServerCloud = $env:CLOUD_API_BASE
Push-Location $FrontendDir
try {
  $env:GOODHR_STATIC_EXPORT = '1'
  $env:NEXT_PUBLIC_CLOUD_API_BASE = $CloudAPIBase.TrimEnd('/')
  $env:CLOUD_API_BASE = $env:NEXT_PUBLIC_CLOUD_API_BASE
  & $NextCommand build
  if ($LASTEXITCODE -ne 0) { throw "本地控制台构建失败，退出码：$LASTEXITCODE" }
  $OutputDir = Join-Path $FrontendDir 'out'
  foreach ($RequiredFile in @('index.html','login.html','admin.html','admin\execution-plans.html')) {
    if (-not (Test-Path -LiteralPath (Join-Path $OutputDir $RequiredFile) -PathType Leaf)) { throw "静态控制台缺少页面：$RequiredFile" }
  }
  if (-not (Test-Path -LiteralPath (Join-Path $OutputDir '_next\static') -PathType Container)) { throw '静态控制台缺少脚本资源。' }
  $Manifest = [ordered]@{ schema_version=1; cloud_api_base=$env:NEXT_PUBLIC_CLOUD_API_BASE; execution_plans_sha256=(Get-FileHash -LiteralPath (Join-Path $OutputDir 'admin\execution-plans.html') -Algorithm SHA256).Hash; generated_at=[DateTime]::UtcNow.ToString('o') }
  $Manifest | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $OutputDir 'hrplus-console-build.json') -Encoding utf8
  Write-Output "[HRPlus] 静态控制台已构建：$OutputDir"
}
finally {
  $env:GOODHR_STATIC_EXPORT = $PriorStatic
  $env:NEXT_PUBLIC_CLOUD_API_BASE = $PriorCloud
  $env:CLOUD_API_BASE = $PriorServerCloud
  Pop-Location
}
