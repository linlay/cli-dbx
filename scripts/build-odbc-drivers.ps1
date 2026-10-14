param(
    [ValidateSet('oracle', 'dm', 'sqlserver')][string]$Engine,
    [string]$OutputDir
)
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$hostOS = (& go env GOHOSTOS).Trim()
$hostArch = (& go env GOHOSTARCH).Trim()
if ($LASTEXITCODE -ne 0 -or $hostOS -ne 'windows') { throw 'Use the shell build script on macOS/Linux.' }
$stage = Join-Path ([System.IO.Path]::GetTempPath()) ('dbx-odbc-' + [guid]::NewGuid())
New-Item -ItemType Directory -Path $stage | Out-Null
$oldOS, $oldArch, $oldCGO = $env:GOOS, $env:GOARCH, $env:CGO_ENABLED
Push-Location $repoRoot
try {
    $env:GOOS = $hostOS; $env:GOARCH = $hostArch; $env:CGO_ENABLED = '0'
    $packager = Join-Path $stage 'package-driver.exe'
    & go build -trimpath -o $packager ./scripts/release/package-driver
    if ($LASTEXITCODE -ne 0) { throw 'Packaging tool build failed' }
    $engines = @('oracle', 'dm', 'sqlserver')
    if ($Engine) { $engines = @($Engine) }
    $env:GOOS = 'windows'; $env:GOARCH = 'amd64'
    foreach ($driverEngine in $engines) {
        $binary = Join-Path $stage "dbx-driver-$driverEngine-odbc.exe"
        Push-Location (Join-Path $repoRoot 'drivers/odbc')
        try {
            & go build -trimpath -ldflags "-s -w -X main.engine=$driverEngine" -o $binary .
            if ($LASTEXITCODE -ne 0) { throw "ODBC adapter build failed: $driverEngine" }
        } finally { Pop-Location }
        $packageArgs = @('--root', $repoRoot, '--binary', $binary, '--engine', $driverEngine,
                         '--os', 'windows', '--arch', 'amd64', '--adapter', 'odbc')
        if ($OutputDir) { $packageArgs += @('--output-dir', $OutputDir) }
        & $packager @packageArgs
        if ($LASTEXITCODE -ne 0) { throw "ODBC adapter packaging failed: $driverEngine" }
    }
} finally {
    Pop-Location
    $env:GOOS = $oldOS; $env:GOARCH = $oldArch; $env:CGO_ENABLED = $oldCGO
    Remove-Item -Recurse -Force $stage
}
