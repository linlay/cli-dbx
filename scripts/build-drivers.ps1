#Requires -Version 5.1
param(
    [Alias("Host")][switch]$HostOnly,
    [ValidateSet("darwin", "linux", "windows")][string]$TargetOS,
    [ValidateSet("amd64", "arm64")][string]$TargetArch,
    [ValidateSet("postgres", "oracle", "dm", "sqlserver")][string]$Engine,
    [string]$OutputDir
)
# Builds independent driver archives; Oracle's toolchain override is scoped to its build.
$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
if ($HostOnly -and ($TargetOS -or $TargetArch)) { throw "HostOnly cannot be combined with TargetOS/TargetArch" }
$hostOS = (& go env GOHOSTOS).Trim()
if ($LASTEXITCODE -ne 0) { throw "Cannot determine Go host OS" }
$hostArch = (& go env GOHOSTARCH).Trim()
if ($LASTEXITCODE -ne 0) { throw "Cannot determine Go host architecture" }
if ($HostOnly) { $TargetOS = $hostOS; $TargetArch = $hostArch }
if (($TargetOS -and -not $TargetArch) -or ($TargetArch -and -not $TargetOS)) { throw "TargetOS and TargetArch must be provided together" }
$targets = @("darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64")
if ($TargetOS) { $targets = @("$TargetOS/$TargetArch") }
$engines = @("postgres", "oracle", "dm", "sqlserver")
if ($Engine) { $engines = @($Engine) }
$stageDir = Join-Path ([IO.Path]::GetTempPath()) ("dbx-drivers-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $stageDir | Out-Null
$savedEnvironment = @{}
foreach ($key in @("CGO_ENABLED", "GOOS", "GOARCH", "GOTOOLCHAIN")) { $savedEnvironment[$key] = [Environment]::GetEnvironmentVariable($key, "Process") }
try {
    $packager = Join-Path $stageDir "package-driver"
    if ($hostOS -eq "windows") { $packager += ".exe" }
    $env:CGO_ENABLED = "0"
    $env:GOOS = $hostOS
    $env:GOARCH = $hostArch
    Push-Location $repoRoot
    try {
        & go build -trimpath -o $packager ./scripts/release/package-driver
        if ($LASTEXITCODE -ne 0) { throw "Driver packager build failed" }
    } finally { Pop-Location }
    foreach ($target in $targets) {
        $goos, $goarch = $target.Split("/")
        foreach ($driverEngine in $engines) {
            $executable = "dbx-driver-$driverEngine"
            if ($goos -eq "windows") { $executable += ".exe" }
            $binary = Join-Path $stageDir $executable
            $env:GOOS = $goos
            $env:GOARCH = $goarch
            [Environment]::SetEnvironmentVariable("GOTOOLCHAIN", $savedEnvironment["GOTOOLCHAIN"], "Process")
            Write-Host "building driver $driverEngine for $goos/$goarch"
            Push-Location (Join-Path $repoRoot "drivers/$driverEngine")
            try {
                & go build -trimpath -ldflags '-s -w' -o $binary .
                if ($LASTEXITCODE -ne 0) { throw "Driver build failed for $driverEngine $goos/$goarch" }
            } finally { Pop-Location }
            $packageArgs = @("--root", $repoRoot, "--binary", $binary, "--engine", $driverEngine, "--os", $goos, "--arch", $goarch)
            if ($OutputDir) { $packageArgs += @("--output-dir", $OutputDir) }
            & $packager @packageArgs
            if ($LASTEXITCODE -ne 0) { throw "Driver packaging failed" }
        }
    }
} finally {
    foreach ($key in $savedEnvironment.Keys) { [Environment]::SetEnvironmentVariable($key, $savedEnvironment[$key], "Process") }
    Remove-Item -LiteralPath $stageDir -Recurse -Force
}
