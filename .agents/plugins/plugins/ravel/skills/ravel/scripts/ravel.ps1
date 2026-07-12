$ErrorActionPreference = "Stop"
$arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
  "X64" { "amd64" }
  "Arm64" { "arm64" }
  default { throw "ravel: unsupported architecture" }
}
$binary = Join-Path $PSScriptRoot "..\bin\ravel_windows_$arch.exe"
if (-not (Test-Path $binary -PathType Leaf)) {
  foreach ($relativeRoot in @("..\..\..", "..\..\..\..")) {
    $checkoutRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot $relativeRoot))
    $candidate = Join-Path $checkoutRoot ".agents\plugins\plugins\ravel\skills\ravel\bin\ravel_windows_$arch.exe"
    if (Test-Path $candidate -PathType Leaf) {
      $binary = $candidate
      break
    }
  }
}
if (-not (Test-Path $binary -PathType Leaf)) {
  throw "ravel: no compatible bundled binary found; update or reinstall Ravel with consent"
}
& $binary @args
exit $LASTEXITCODE
