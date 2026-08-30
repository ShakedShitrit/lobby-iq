Add-Type -AssemblyName System.Drawing

$outDir = $args[0]
if (-not (Test-Path $outDir)) { New-Item -ItemType Directory -Force $outDir | Out-Null }

# Discord renders the small badge at roughly 24px, so the art has to survive a
# heavy downscale: one flat shape, a strong rim, no fine detail.
function New-Badge {
    param([string]$Path, [string]$Top, [string]$Bottom, [string]$Rim)

    $size = 512
    $bmp = New-Object System.Drawing.Bitmap($size, $size, [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $g = [System.Drawing.Graphics]::FromImage($bmp)
    $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
    $g.Clear([System.Drawing.Color]::Transparent)

    # A hair of padding keeps the antialiased edge off the bitmap border.
    $pad = 8
    $rect = New-Object System.Drawing.RectangleF($pad, $pad, ($size - 2*$pad), ($size - 2*$pad))

    $brush = New-Object System.Drawing.Drawing2D.LinearGradientBrush(
        $rect,
        [System.Drawing.ColorTranslator]::FromHtml($Top),
        [System.Drawing.ColorTranslator]::FromHtml($Bottom),
        90.0)
    $g.FillEllipse($brush, $rect)

    $pen = New-Object System.Drawing.Pen([System.Drawing.ColorTranslator]::FromHtml($Rim), 22.0)
    $inset = $pen.Width / 2
    $g.DrawEllipse($pen, ($rect.X + $inset), ($rect.Y + $inset), ($rect.Width - $pen.Width), ($rect.Height - $pen.Width))

    $bmp.Save($Path, [System.Drawing.Imaging.ImageFormat]::Png)

    $pen.Dispose(); $brush.Dispose(); $g.Dispose(); $bmp.Dispose()
    Write-Output "wrote $Path"
}

New-Badge -Path (Join-Path $outDir 'team_blue.png')   -Top '#4FB0FF' -Bottom '#0B6FD4' -Rim '#08417E'
New-Badge -Path (Join-Path $outDir 'team_orange.png') -Top '#FFB25A' -Bottom '#E8730C' -Rim '#8A3F00'
