param(
  [string]$Tag = (Get-Date -Format yyyyMMddHHmm)
)

Set-StrictMode -Version Latest
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Push-Location $root\..\

Write-Host "Building frontend..."
npm --prefix frontend ci
npm --prefix frontend run build

$matrix = @(
  @{ GOOS='windows'; GOARCH='amd64'; ARCH='zip'; EXT='.exe' },
  @{ GOOS='windows'; GOARCH='arm64'; ARCH='zip'; EXT='.exe' },
  @{ GOOS='linux'; GOARCH='amd64'; ARCH='tar.gz'; EXT='' },
  @{ GOOS='linux'; GOARCH='arm64'; ARCH='tar.gz'; EXT='' },
  @{ GOOS='darwin'; GOARCH='amd64'; ARCH='tar.gz'; EXT='' },
  @{ GOOS='darwin'; GOARCH='arm64'; ARCH='tar.gz'; EXT='' }
)

New-Item -ItemType Directory -Path release_artifacts -Force | Out-Null

foreach ($m in $matrix) {
  $goos = $m.GOOS; $goarch = $m.GOARCH; $arch = $m.ARCH; $ext = $m.EXT
  Write-Host "Building for $goos/$goarch..."
  $bin = 'easysync'
  if ($goos -eq 'windows') { $bin = $bin + $ext }
  $env:GOOS = $goos
  $env:GOARCH = $goarch
  go build -o "build\$bin" .

  $pkg = 'tmp_pkg'
  Remove-Item -Recurse -Force $pkg -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Path $pkg | Out-Null
  Copy-Item -Path "build\$bin" -Destination $pkg
  New-Item -ItemType Directory -Path "$pkg\frontend" | Out-Null
  Copy-Item -Path "frontend\dist\*" -Destination "$pkg\frontend" -Recurse
  if (Test-Path data) { Copy-Item -Path data -Destination $pkg -Recurse }
  Copy-Item -Path README.md -Destination $pkg -ErrorAction SilentlyContinue

  $name = "easysync-$Tag-$goos-$goarch"
  if ($arch -eq 'zip') {
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [System.IO.Compression.ZipFile]::CreateFromDirectory($pkg, "release_artifacts\$name.zip")
  } else {
    tar -czf "release_artifacts\$name.tar.gz" -C $pkg .
  }
  Write-Host "Packaged release_artifacts\$name.*"
}

Pop-Location
