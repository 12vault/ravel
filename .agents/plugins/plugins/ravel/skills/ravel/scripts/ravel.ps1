$ErrorActionPreference = "Stop"

$requiredVersion = (Get-Content (Join-Path $PSScriptRoot "..\VERSION") -Raw).Trim().TrimStart("v")
$arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
  "X64" { "amd64" }
  "Arm64" { "arm64" }
  default { throw "ravel: unsupported architecture" }
}

$bundled = Join-Path $PSScriptRoot "..\bin\ravel_windows_$arch.exe"
if (-not (Test-Path $bundled -PathType Leaf)) {
  $bundled = $null
  foreach ($relativeRoot in @("..\..\..", "..\..\..\..")) {
    $checkoutRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot $relativeRoot))
    $candidate = Join-Path $checkoutRoot ".agents\plugins\plugins\ravel\skills\ravel\bin\ravel_windows_$arch.exe"
    if (Test-Path $candidate -PathType Leaf) { $bundled = $candidate; break }
  }
}

function Get-RavelVersion([string]$Command) {
  try {
    $line = (& $Command version 2>$null | Select-Object -First 1)
    if ($line -match '^ravel v(.+)$') { return $Matches[1] }
  } catch {}
  return $null
}

function Compare-SemVer([string]$Left, [string]$Right) {
  $pattern = '^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$'
  if ($Left -notmatch $pattern) { return -1 }
  $leftParts = @([int]$Matches[1], [int]$Matches[2], [int]$Matches[3]); $leftPre = $Matches[4]
  if ($Right -notmatch $pattern) { return 1 }
  $rightParts = @([int]$Matches[1], [int]$Matches[2], [int]$Matches[3]); $rightPre = $Matches[4]
  for ($i = 0; $i -lt 3; $i++) {
    if ($leftParts[$i] -lt $rightParts[$i]) { return -1 }
    if ($leftParts[$i] -gt $rightParts[$i]) { return 1 }
  }
  if ($leftPre -eq $rightPre) { return 0 }
  if (-not $leftPre) { return 1 }
  if (-not $rightPre) { return -1 }
  $leftIds = $leftPre.Split('.'); $rightIds = $rightPre.Split('.')
  for ($i = 0; $i -lt [Math]::Min($leftIds.Count, $rightIds.Count); $i++) {
    if ($leftIds[$i] -ceq $rightIds[$i]) { continue }
    $leftNumber = $leftIds[$i] -match '^\d+$'; $rightNumber = $rightIds[$i] -match '^\d+$'
    if ($leftNumber -and $rightNumber) { return ([int64]$leftIds[$i]).CompareTo([int64]$rightIds[$i]) }
    if ($leftNumber -ne $rightNumber) { if ($leftNumber) { return -1 } else { return 1 } }
    return [string]::CompareOrdinal($leftIds[$i], $rightIds[$i])
  }
  return $leftIds.Count.CompareTo($rightIds.Count)
}

$globalCommand = Get-Command ravel -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
$global = if ($globalCommand) { $globalCommand.Source } else { $null }
$globalVersion = if ($global) { Get-RavelVersion $global } else { $null }
$selected = $global
$usingBundle = $false
# The bundle is the exact binary paired with this skill. Prefer it for equal
# versions too; only a strictly newer global CLI supersedes it.
if ($bundled -and ((-not $globalVersion) -or ((Compare-SemVer $globalVersion $requiredVersion) -le 0))) {
  $selected = $bundled
  $usingBundle = $true
}
if (-not $selected) { throw "ravel: no compatible CLI found; update or reinstall Ravel with consent" }

if ($args.Count -gt 0 -and $args[0] -eq "version" -and $globalVersion -and ((Compare-SemVer $globalVersion $requiredVersion) -lt 0)) {
  [Console]::Error.WriteLine("Your global Ravel is v$globalVersion; this skill requires v$requiredVersion.")
  if ($usingBundle) {
    [Console]::Error.WriteLine("Using the bundled v$requiredVersion binary for this task.")
  } else {
    [Console]::Error.WriteLine("No compatible bundled binary is available; continuing with the global CLI.")
  }
  [Console]::Error.WriteLine("To update globally: ravel self-update --platforms codex")
}

& $selected @args
exit $LASTEXITCODE
