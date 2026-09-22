# 从 PNG 源图生成符合 Windows/Inno Setup 要求的多尺寸 ICO 文件
# 用法: .\scripts\generate-ico.ps1 -Source <png路径> -Output <ico路径>
#
# 使用 WPF imaging 正确处理 PNG 的 ICC 颜色配置文件，
# 生成包含 16/32/48/256 四个尺寸的 ICO，Windows 会根据场景自动选用最佳尺寸。

param(
    [Parameter(Mandatory)]
    [string]$Source,
    [Parameter(Mandatory)]
    [string]$Output
)

Add-Type -AssemblyName PresentationCore
Add-Type -AssemblyName WindowsBase

# 固定生成四个尺寸，覆盖从小图标到高清桌面图标的所有场景
$Sizes = @(16, 32, 48, 256)

# 用 WPF 读取 PNG（自动处理 ICC 颜色配置）
$uri = [Uri]::new((Resolve-Path $Source).ProviderPath)
$decoder = [System.Windows.Media.Imaging.BitmapDecoder]::Create(
    $uri,
    [System.Windows.Media.Imaging.BitmapCreateOptions]::None,
    [System.Windows.Media.Imaging.BitmapCacheOption]::OnLoad
)
$frame = $decoder.Frames[0]
Write-Host "源图: $($frame.PixelWidth)x$($frame.PixelHeight)" -ForegroundColor Gray

if ($frame.PixelWidth -lt 256 -or $frame.PixelHeight -lt 256) {
    Write-Host "警告: 源图小于 256x256，高清尺寸可能模糊" -ForegroundColor Yellow
}

# === 收集每个尺寸的像素数据 ===
# 结构: [0]=16px, [1]=32px, [2]=48px, [3]=256px
$allPixels = @()
$allStrides = @()
foreach ($Size in $Sizes) {
    # 用 RenderTargetBitmap 精确缩放到目标尺寸
    $scale = $Size / $frame.PixelWidth
    $drawingVisual = New-Object System.Windows.Media.DrawingVisual
    $dc = $drawingVisual.RenderOpen()
    $dc.DrawImage($frame, [System.Windows.Rect]::new(0, 0, $Size, $Size))
    $dc.Close()

    $renderTarget = New-Object System.Windows.Media.Imaging.RenderTargetBitmap(
        $Size, $Size, 96, 96, [System.Windows.Media.PixelFormats]::Pbgra32
    )
    $renderTarget.Render($drawingVisual)

    # 提取像素
    $stride = $Size * 4
    $pixelBytes = New-Object byte[] ($stride * $Size)
    $renderTarget.CopyPixels($pixelBytes, $stride, 0)

    # 调试输出
    $cy = $Size / 2; $cx = $Size / 2
    $cIdx = $cy * $stride + $cx * 4
    Write-Host "  ${Size}x${Size} 中心像素: B=$($pixelBytes[$cIdx]) G=$($pixelBytes[$cIdx+1]) R=$($pixelBytes[$cIdx+2]) A=$($pixelBytes[$cIdx+3])" -ForegroundColor Gray

    $allPixels += ,$pixelBytes
    $allStrides += $stride
}

# === 计算每个尺寸的数据大小与偏移 ===
# ICONDIR(6) + N * ICONDIRENTRY(16) = 6 + N*16
$numImages = $Sizes.Count
$iconDirSize = 6
$entrySize = 16
$dirEntryTotal = $iconDirSize + $numImages * $entrySize

# 计算每个尺寸的 imageDataSize（BIH + 像素 + AND mask）
$imageDataSizes = @()
foreach ($i in 0..($numImages - 1)) {
    $Size = $Sizes[$i]
    $bihSize = 40
    $pixelDataSize = $Size * $Size * 4
    $maskWidth = [int][Math]::Ceiling($Size / 32.0) * 4
    $andMaskSize = $Size * $maskWidth
    $imageDataSizes += ($bihSize + $pixelDataSize + $andMaskSize)
}

# 计算每个尺寸的偏移
$imageOffsets = @()
$currentOffset = $dirEntryTotal
foreach ($ids in $imageDataSizes) {
    $imageOffsets += $currentOffset
    $currentOffset += $ids
}
$totalSize = $currentOffset

# === 构建 ICO 字节数组 ===
$icoBytes = New-Object byte[] $totalSize

# ICONDIR
$icoBytes[0] = 0; $icoBytes[1] = 0          # Reserved
$icoBytes[2] = 1; $icoBytes[3] = 0          # Type = ICO
[BitConverter]::GetBytes([uint16]$numImages).CopyTo($icoBytes, 4)  # Image count

# 写入每个 ICONDIRENTRY 和对应的图像数据
for ($i = 0; $i -lt $numImages; $i++) {
    $Size = $Sizes[$i]
    $pixelBytes = $allPixels[$i]
    $stride = $allStrides[$i]
    $imageDataSize = $imageDataSizes[$i]
    $imageOffset = $imageOffsets[$i]

    # ICONDIRENTRY
    $entryOffset = $iconDirSize + $i * $entrySize
    $widthByte = if ($Size -ge 256) { 0 } else { [byte]$Size }
    $heightByte = if ($Size -ge 256) { 0 } else { [byte]$Size }
    $icoBytes[$entryOffset] = $widthByte
    $icoBytes[$entryOffset + 1] = $heightByte
    $icoBytes[$entryOffset + 2] = 0   # Color palette
    $icoBytes[$entryOffset + 3] = 0   # Reserved
    $icoBytes[$entryOffset + 4] = 1   # Color planes
    $icoBytes[$entryOffset + 5] = 0
    $icoBytes[$entryOffset + 6] = 32  # Bits per pixel
    $icoBytes[$entryOffset + 7] = 0
    [BitConverter]::GetBytes([uint32]$imageDataSize).CopyTo($icoBytes, $entryOffset + 8)
    [BitConverter]::GetBytes([uint32]$imageOffset).CopyTo($icoBytes, $entryOffset + 12)

    # === 写入图像数据 ===
    $bihSize = 40
    $pixelDataSize = $Size * $Size * 4
    $maskWidth = [int][Math]::Ceiling($Size / 32.0) * 4
    $andMaskSize = $Size * $maskWidth

    # BITMAPINFOHEADER
    $bihOffset = $imageOffset
    [BitConverter]::GetBytes([uint32]$bihSize).CopyTo($icoBytes, $bihOffset)
    [BitConverter]::GetBytes([int32]$Size).CopyTo($icoBytes, $bihOffset + 4)
    [BitConverter]::GetBytes([int32]($Size * 2)).CopyTo($icoBytes, $bihOffset + 8)
    [BitConverter]::GetBytes([uint16]1).CopyTo($icoBytes, $bihOffset + 12)
    [BitConverter]::GetBytes([uint16]32).CopyTo($icoBytes, $bihOffset + 14)
    [BitConverter]::GetBytes([uint32]0).CopyTo($icoBytes, $bihOffset + 16)
    [BitConverter]::GetBytes([uint32]$pixelDataSize).CopyTo($icoBytes, $bihOffset + 20)
    [BitConverter]::GetBytes([int32]0).CopyTo($icoBytes, $bihOffset + 24)
    [BitConverter]::GetBytes([int32]0).CopyTo($icoBytes, $bihOffset + 28)
    [BitConverter]::GetBytes([uint32]0).CopyTo($icoBytes, $bihOffset + 32)
    [BitConverter]::GetBytes([uint32]0).CopyTo($icoBytes, $bihOffset + 36)

    # 像素数据（bottom-up，反预乘 alpha：WPF Pbgra32 是预乘 alpha，ICO 需要直线 alpha）
    $pixelStart = $bihOffset + $bihSize
    for ($y = 0; $y -lt $Size; $y++) {
        $srcRow = ($Size - 1 - $y) * $stride
        $dstRow = $y * $Size * 4
        for ($x = 0; $x -lt $Size; $x++) {
            $si = $srcRow + $x * 4
            $di = $pixelStart + $dstRow + $x * 4
            $a = $pixelBytes[$si + 3]
            $icoBytes[$di + 3] = $a  # A 不变
            if ($a -eq 0) {
                $icoBytes[$di] = 0; $icoBytes[$di + 1] = 0; $icoBytes[$di + 2] = 0
            } elseif ($a -eq 255) {
                # 完全不透明，预乘=直线，直接复制
                $icoBytes[$di] = $pixelBytes[$si]
                $icoBytes[$di + 1] = $pixelBytes[$si + 1]
                $icoBytes[$di + 2] = $pixelBytes[$si + 2]
            } else {
                # 反预乘：straight = premultiplied * 255 / alpha
                $icoBytes[$di] = [byte]([math]::Min(255, $pixelBytes[$si] * 255 / $a))
                $icoBytes[$di + 1] = [byte]([math]::Min(255, $pixelBytes[$si + 1] * 255 / $a))
                $icoBytes[$di + 2] = [byte]([math]::Min(255, $pixelBytes[$si + 2] * 255 / $a))
            }
        }
    }

    # AND mask（全零，已初始化为 0，无需额外操作）
}

# 写入文件
$outputDir = Split-Path $Output -Parent
if (!(Test-Path $outputDir)) { New-Item -ItemType Directory -Path $outputDir -Force | Out-Null }
$fullOutput = Join-Path (Resolve-Path $outputDir) (Split-Path $Output -Leaf)
[System.IO.File]::WriteAllBytes($fullOutput, $icoBytes)
Write-Host "已生成 ICO: $fullOutput ($($icoBytes.Length) bytes, 尺寸: $($Sizes -join ', '))" -ForegroundColor Green
