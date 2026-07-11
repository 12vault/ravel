$ErrorActionPreference = "Stop"
$arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
  "X64" { "amd64" }
  "Arm64" { "arm64" }
  default { throw "ravel: unsupported architecture" }
}
$binary = Join-Path $PSScriptRoot "..\bin\ravel_windows_$arch.exe"
if (-not (Test-Path $binary -PathType Leaf)) {
  $checkoutRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\..\..\.."))
  $binary = Join-Path $checkoutRoot ".agents\plugins\plugins\ravel\skills\ravel\bin\ravel_windows_$arch.exe"
}
if (-not (Test-Path $binary -PathType Leaf)) {
  throw "ravel: no compatible bundled binary found; update or reinstall Ravel with consent"
}
& $binary @args
exit $LASTEXITCODE
