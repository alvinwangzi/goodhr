# GoodHR 5 本地开发环境启动脚本
# 用法：.\scripts\dev.ps1
#
# 本脚本会：
# 1. 启动 PostgreSQL 数据库（Docker）
# 2. 启动后端 Go 服务（本地运行，端口 8084）
# 3. 启动前端 Next.js 服务（本地运行，端口 3000）

$ErrorActionPreference = "Stop"
$RootDir = Split-Path -Parent $PSScriptRoot
$BackendDir = Join-Path $RootDir "cloud\backend"
$FrontendDir = Join-Path $RootDir "cloud\frontend-next"

Write-Host "=== GoodHR 5 本地开发环境 ===" -ForegroundColor Cyan

# 1. 启动 PostgreSQL
Write-Host "`n[1/3] 启动 PostgreSQL 数据库..." -ForegroundColor Yellow
Set-Location $RootDir
docker compose -f docker-compose.local.yml up -d postgres
if ($LASTEXITCODE -ne 0) {
    Write-Host "PostgreSQL 启动失败" -ForegroundColor Red
    exit 1
}

# 等待数据库就绪
Write-Host "等待数据库就绪..." -ForegroundColor Gray
$MaxAttempts = 30
$Attempt = 0
while ($Attempt -lt $MaxAttempts) {
    $Result = docker compose -f docker-compose.local.yml exec -T postgres pg_isready -U goodhr5_dev -d goodhr5_dev 2>&1
    if ($Result -match "accepting connections") {
        Write-Host "数据库已就绪" -ForegroundColor Green
        break
    }
    $Attempt++
    Start-Sleep -Seconds 1
}
if ($Attempt -ge $MaxAttempts) {
    Write-Host "数据库启动超时" -ForegroundColor Red
    exit 1
}

# 2. 启动后端
Write-Host "`n[2/3] 启动后端 Go 服务..." -ForegroundColor Yellow
Set-Location $BackendDir

# 从 .env 文件加载环境变量，新增变量只需改 .env，不用动这个脚本
$EnvFile = Join-Path $BackendDir ".env"
if (Test-Path $EnvFile) {
    foreach ($line in Get-Content $EnvFile) {
        $line = $line.Trim()
        if ($line -eq "" -or $line.StartsWith("#")) { continue }
        $eqIdx = $line.IndexOf("=")
        if ($eqIdx -le 0) { continue }
        $key = $line.Substring(0, $eqIdx).Trim()
        $val = $line.Substring($eqIdx + 1).Trim()
        [System.Environment]::SetEnvironmentVariable($key, $val)
    }
    Write-Host "已从 .env 加载环境变量" -ForegroundColor Gray
} else {
    Write-Host ".env 文件不存在：$EnvFile" -ForegroundColor Red
    exit 1
}

$BackendJob = Start-Job -ScriptBlock {
    Set-Location $using:BackendDir
    go run ./cmd/server
}
Write-Host "后端启动中（端口 8084）..." -ForegroundColor Gray

# 等待后端就绪
Start-Sleep -Seconds 3
$Attempt = 0
while ($Attempt -lt 20) {
    try {
        $Response = Invoke-WebRequest -Uri "http://localhost:8084/health" -TimeoutSec 2 -ErrorAction Stop
        if ($Response.StatusCode -eq 200) {
            Write-Host "后端已就绪" -ForegroundColor Green
            break
        }
    } catch {}
    $Attempt++
    Start-Sleep -Seconds 1
}

# 3. 启动前端
Write-Host "`n[3/3] 启动前端 Next.js 服务..." -ForegroundColor Yellow
Set-Location $FrontendDir
$FrontendJob = Start-Job -ScriptBlock {
    Set-Location $using:FrontendDir
    $env:NEXT_PUBLIC_CLOUD_API_BASE = "http://localhost:8084"
    $env:NEXT_PUBLIC_SITE_URL = "http://localhost:3000"
    npm run dev -- --port 3000
}
Write-Host "前端启动中（端口 3000）..." -ForegroundColor Gray

# 等待前端就绪
Start-Sleep -Seconds 5
$Attempt = 0
while ($Attempt -lt 30) {
    try {
        $Response = Invoke-WebRequest -Uri "http://localhost:3000" -TimeoutSec 2 -ErrorAction Stop
        if ($Response.StatusCode -eq 200) {
            Write-Host "前端已就绪" -ForegroundColor Green
            break
        }
    } catch {}
    $Attempt++
    Start-Sleep -Seconds 1
}

Write-Host "`n=== 开发环境已启动 ===" -ForegroundColor Cyan
Write-Host "前端：http://localhost:3000" -ForegroundColor Green
Write-Host "后端：http://localhost:8084" -ForegroundColor Green
Write-Host "数据库：localhost:25432" -ForegroundColor Green
Write-Host "`n按 Ctrl+C 停止所有服务" -ForegroundColor Gray

# 等待用户中断
try {
    while ($true) {
        Start-Sleep -Seconds 1
    }
} finally {
    Write-Host "`n正在停止服务..." -ForegroundColor Yellow
    if ($BackendJob) { Stop-Job $BackendJob; Remove-Job $BackendJob }
    if ($FrontendJob) { Stop-Job $FrontendJob; Remove-Job $FrontendJob }
    Set-Location $RootDir
    docker compose -f docker-compose.local.yml down
    Write-Host "服务已停止" -ForegroundColor Green
}
