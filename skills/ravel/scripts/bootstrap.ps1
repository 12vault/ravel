$ErrorActionPreference = "Stop"
if (Get-Command ravel -ErrorAction SilentlyContinue) { ravel version; exit 0 }
$repo = if ($env:RAVEL_REPO) { $env:RAVEL_REPO } else { "12vault/ravel" }
$installDir = if ($env:RAVEL_INSTALL_DIR) { $env:RAVEL_INSTALL_DIR } else { Join-Path $HOME ".local\bin" }
$arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) { "X64" { "amd64" } "Arm64" { "arm64" } default { throw "ravel: unsupported architecture" } }
$asset = "ravel_windows_$arch.zip"
$base = "https://github.com/$repo/releases/latest/download"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("ravel-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Invoke-WebRequest "$base/$asset" -OutFile (Join-Path $tmp $asset)
  Invoke-WebRequest "$base/checksums.txt" -OutFile (Join-Path $tmp "checksums.txt")
  $line = Get-Content (Join-Path $tmp "checksums.txt") | Where-Object { $_ -match "  $([regex]::Escape($asset))$" }
  $expected = ($line -split "  ")[0]
  $actual = (Get-FileHash (Join-Path $tmp $asset) -Algorithm SHA256).Hash.ToLowerInvariant()
  if (-not $expected -or $actual -ne $expected.ToLowerInvariant()) { throw "ravel: checksum verification failed" }
  Expand-Archive (Join-Path $tmp $asset) -DestinationPath $tmp
  New-Item -ItemType Directory -Force -Path $installDir | Out-Null
  Copy-Item (Join-Path $tmp "ravel.exe") (Join-Path $installDir "ravel.exe") -Force
  & (Join-Path $installDir "ravel.exe") version
} finally { Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue }
