param(
  [Parameter(Mandatory)][string]$KeyPath,
  [string]$Server = 'ubuntu@140.238.160.99',
  [string]$Tag = '1.0.0',
  [string]$Go = 'go'
)
$ErrorActionPreference = 'Stop'
if ($Tag -notmatch '^[0-9]+\.[0-9]+\.[0-9]+$') { throw 'Supply a numeric release tag.' }
$buildDir = Join-Path ([IO.Path]::GetTempPath()) ('private-communication-' + [guid]::NewGuid())
New-Item -Type Directory -Path $buildDir | Out-Null
Push-Location $PSScriptRoot
try {
  $env:GOOS = 'linux'
  $env:GOARCH = 'arm64'
  $env:CGO_ENABLED = '0'
  & $Go build -trimpath -ldflags="-s -w" -o (Join-Path $buildDir 'communication') .
  if ($LASTEXITCODE) { throw 'Build failed.' }
  Copy-Item -LiteralPath Dockerfile.arm64 -Destination $buildDir
  $archive = Join-Path $buildDir 'image.tar.gz'
  tar.exe -czf $archive -C $buildDir communication Dockerfile.arm64
  if ($LASTEXITCODE) { throw 'Packaging failed.' }
  $release = "build-$Tag.tar.gz"
  scp -i $KeyPath -o BatchMode=yes -o StrictHostKeyChecking=yes $archive "${Server}:/opt/private-communication/$release"
  if ($LASTEXITCODE) { throw 'Upload failed.' }
  $remote = 'set -eu; cd /opt/private-communication; build=$(mktemp -d); trap ''rm -f "$build/communication" "$build/Dockerfile.arm64"; rmdir "$build"'' EXIT; tar -xzf {0} -C "$build"; sudo -n docker build -f "$build/Dockerfile.arm64" -t private-communication-backend:{1} "$build"; rm -f {0}' -f $release,$Tag
  ssh -i $KeyPath -o BatchMode=yes -o StrictHostKeyChecking=yes $Server $remote
  if ($LASTEXITCODE) { throw 'Remote image build failed.' }
} finally {
  Pop-Location
  foreach ($filename in 'communication','Dockerfile.arm64','image.tar.gz') {
    $file = Join-Path $buildDir $filename
    if (Test-Path -LiteralPath $file) { Remove-Item -LiteralPath $file }
  }
  [IO.Directory]::Delete($buildDir)
}

