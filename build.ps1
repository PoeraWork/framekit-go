# build.ps1 — 构建 framekit 发布包到 dist\ 目录。
#
# FFmpeg 的运行时 DLL 是隐式链接的：Windows 在进程启动时就要解析它们，且只在
# exe 自身所在目录查找，因此 DLL 必须和 framekit.exe 放在同一个文件夹。dist\
# 就是这个自包含的绿色包，整体可直接压缩分发；源码根目录保持干净。
#
# 需要把 FFmpeg 开发包（BtbN shared build）解压到项目内的 ffmpeg-dev\ 目录。
# 下载地址：https://github.com/BtbN/FFmpeg-Builds/releases
$ErrorActionPreference = 'Stop'

$root   = $PSScriptRoot
$ffmpeg = Join-Path $root 'ffmpeg-dev'
if (-not (Test-Path $ffmpeg)) {
    throw "FFmpeg 开发包未找到：$ffmpeg`n请从 https://github.com/BtbN/FFmpeg-Builds/releases 下载 shared build 并解压到该目录。"
}
$dist = Join-Path $root 'dist'

$ffmpegUnix = $ffmpeg -replace '\\', '/'
$env:CGO_ENABLED     = '1'
$env:CGO_CFLAGS      = "-I$ffmpegUnix/include"
$env:CGO_LDFLAGS     = "-L$ffmpegUnix/lib"
$env:PKG_CONFIG_PATH = "$ffmpegUnix/lib/pkgconfig"

Push-Location $root
try {
    # 每次构建前清空输出目录，避免残留旧文件
    if (Test-Path $dist) { Remove-Item $dist -Recurse -Force }
    New-Item -ItemType Directory -Path $dist | Out-Null

    $version = (& git describe --tags --always --dirty 2>$null)
    if ($LASTEXITCODE -ne 0 -or -not $version) { $version = 'dev' }
    $version = $version.Trim()
    $ldflags = "-s -w -X framekit/internal/buildinfo.Version=$version"

    Write-Host "building framekit.exe ($version) ..."
    go build -trimpath -ldflags $ldflags -o (Join-Path $dist 'framekit.exe') .
    if ($LASTEXITCODE -ne 0) { throw 'go build failed' }

    Write-Host 'copying FFmpeg runtime DLLs ...'
    Copy-Item "$ffmpeg\bin\*.dll" -Destination $dist -Force

    $actualVersion = (& (Join-Path $dist 'framekit.exe') --version).Trim()
    $expectedVersion = "framekit $version"
    if ($actualVersion -ne $expectedVersion) {
        throw "version check failed: got '$actualVersion', want '$expectedVersion'"
    }
    Write-Host "verified: $actualVersion"

    $count = (Get-ChildItem $dist -File).Count
    Write-Host "done: dist\ 发布包就绪（$count 个文件），可直接压缩分发"
}
finally {
    Pop-Location
}
