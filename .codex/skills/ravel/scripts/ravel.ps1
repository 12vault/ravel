$ErrorActionPreference = "Stop"
$arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
  "X64" { "amd64" }
  "Arm64" { "arm64" }
  default { throw "ravel: unsupported architecture" }
}
$binary = Join-Path $PSScriptRoot "..\bin\ravel_windows_$arch.exe"
if (-not (Test-Path $binary -PathType Leaf)) { throw "ravel: bundled binary is missing: $binary" }
& $binary @args
exit $LASTEXITCODE
