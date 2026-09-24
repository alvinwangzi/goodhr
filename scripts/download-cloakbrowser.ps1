# 分块并行下载 CloakBrowser Windows x64 安装包到本地后端 uploads 目录。
# 原因：官方源单连接速度极慢，多连接并行可叠加带宽。
$ErrorActionPreference = 'Continue'

$url          = 'https://cloakbrowser.dev/chromium-v146.0.7680.177.5/cloakbrowser-windows-x64.zip'
$total        = 562004238
$expectedSha  = 'b213795cb32c3169f766c74ce1d0275fc89d3df256de39c04da7fb4c23b7fdbe'
$uploadsDir   = 'f:\AIWorkplace\hrplus\goodhr5\cloud\backend\uploads'
$chunksDir    = Join-Path $uploadsDir '.chunks'
$finalPath    = Join-Path $uploadsDir 'cloakbrowser-windows-x64.zip'
$logPath      = Join-Path $uploadsDir 'download-status.log'
$chunkCount   = 64

New-Item -ItemType Directory -Force $chunksDir | Out-Null

function Log($msg) {
    $line = "{0}  {1}" -f (Get-Date -Format 'HH:mm:ss'), $msg
    Add-Content -Path $logPath -Value $line
    Write-Host $line
}

# 每个分块的起止偏移（闭区间）
$chunkSize = [math]::Ceiling($total / $chunkCount)
$chunks = 0..($chunkCount - 1) | ForEach-Object {
    $s = $_ * $chunkSize
    $e = [math]::Min($s + $chunkSize - 1, $total - 1)
    [pscustomobject]@{ Index = $_; Start = $s; End = $e; Size = $e - $s + 1 }
}

function Test-Chunk($c) {
    $p = Join-Path $chunksDir ("{0:D4}.part" -f $c.Index)
    if (Test-Path $p) { return (Get-Item $p).Length -eq $c.Size }
    return $false
}

Log "开始下载：$url"
Log "总大小 $total 字节，分 $chunkCount 块，每块约 $chunkSize 字节"

$maxRounds = 12
for ($round = 1; $round -le $maxRounds; $round++) {
    $pending = @($chunks | Where-Object { -not (Test-Chunk $_) })
    if ($pending.Count -eq 0) { break }
    Log "第 $round 轮：剩余 $($pending.Count) 块待下载"
    $pending | ForEach-Object -Parallel {
        $c = $_
        $p = Join-Path $using:chunksDir ("{0:D4}.part" -f $c.Index)
        $range = "{0}-{1}" -f $c.Start, $c.End
        curl.exe -L -r $range -o $p --connect-timeout 15 --retry 2 -sS $using:url 2>$null
    } -ThrottleLimit $chunkCount
    Start-Sleep -Seconds 2
}

$bad = @($chunks | Where-Object { -not (Test-Chunk $_) })
if ($bad.Count -gt 0) {
    Log "FAILED：$($bad.Count) 块下载失败，已放弃"
    exit 1
}

Log "全部分块下载完成，开始合并"
$fs = [System.IO.File]::Open($finalPath, 'Create')
try {
    foreach ($c in $chunks) {
        $p = Join-Path $chunksDir ("{0:D4}.part" -f $c.Index)
        $rs = [System.IO.File]::OpenRead($p)
        try {
            $buf = New-Object byte[] 1MB
            while (($n = $rs.Read($buf, 0, $buf.Length)) -gt 0) {
                $fs.Write($buf, 0, $n)
            }
        } finally { $rs.Dispose() }
    }
} finally { $fs.Dispose() }

if ((Get-Item $finalPath).Length -ne $total) {
    Log "FAILED：合并后文件大小不一致"
    exit 1
}

Log "合并完成，开始校验 sha256"
$sha = (Get-FileHash -Path $finalPath -Algorithm SHA256).Hash.ToLower()
if ($sha -ne $expectedSha) {
    Log "FAILED：sha256 不一致，期望 $expectedSha，实际 $sha"
    exit 1
}

Remove-Item -Recurse -Force $chunksDir
Log "DONE：文件已就绪 $finalPath"
