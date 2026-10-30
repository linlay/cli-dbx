#Requires -Version 5.1
param(
    [ValidateSet('postgres','oracle','dm','sqlserver','all')][string]$Engine = 'all',
    [switch]$Test
)
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$saved = @{}
foreach ($name in @('GOWORK','GOROOT','GOTOOLCHAIN','CGO_ENABLED','GOPROXY','GOOS','GOARCH','DBX_MODULE_BUILD_TAGS','DBX_TEST_SHARED_HOST','DBX_TEST_SHARED_MODULE')) { $saved[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
Push-Location $root
try {
    $env:GOTOOLCHAIN = 'local'
    $goVersion = (& go env GOVERSION).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Cannot read installed Go version' }
    $env:CGO_ENABLED = '0'
    $env:DBX_MODULE_BUILD_TAGS = 'dbmod'
    $env:GOOS = (& go env GOHOSTOS).Trim()
    $env:GOARCH = (& go env GOHOSTARCH).Trim()
    if ("$($env:GOOS)/$($env:GOARCH)" -notin @('windows/amd64','darwin/arm64')) { throw 'Supported shared hosts: windows/amd64, darwin/arm64' }
    if ($env:GOOS -eq 'darwin') { $env:CGO_ENABLED = '1' }
    if (-not $env:GOPROXY) { $env:GOPROXY = 'https://proxy.golang.org,direct' }
    $python = Get-Command python -ErrorAction Stop
    # SDK preparation validates the minor series and dependency minimum.
    $privateSDK = "dist/$goVersion-dbmod"
    & $python.Source -B scripts/prepare-dbmod-toolchain.py --output $privateSDK
    if ($LASTEXITCODE -ne 0) { throw 'Private toolchain setup failed' }
    $env:GOROOT = Join-Path $root $privateSDK
    & $python.Source -B scripts/prepare-shared-workspace.py --output dist/shared-host.go.work
    if ($LASTEXITCODE -ne 0) { throw 'Shared workspace setup failed' }
    $env:GOWORK = Join-Path $root 'dist/shared-host.go.work'
    $version = (Get-Content -LiteralPath VERSION -Raw).Trim()
    $output = Join-Path $root "dist/$version/shared-host"
    New-Item -ItemType Directory -Path $output -Force | Out-Null
    $suffix = if ((& go env GOHOSTOS).Trim() -eq 'windows') { '.exe' } else { '' }
    $hostPath = Join-Path $output "dbx$suffix"
    $packPath = Join-Path $root "dist/dbmod-pack$suffix"
    $commit = (& git rev-parse --short HEAD).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Cannot read source commit' }
    $buildTime = (& git show -s --format=%cI HEAD).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Cannot read source commit time' }
    $linkFlags = "-w -X github.com/linlay/cli-dbx/internal/buildinfo.Version=$version -X github.com/linlay/cli-dbx/internal/buildinfo.Commit=$commit -X github.com/linlay/cli-dbx/internal/buildinfo.BuildTime=$buildTime"
    & go build -tags dbmod,dbmod_runtime -trimpath -ldflags $linkFlags -o $hostPath ./cmd/dbx
    if ($LASTEXITCODE -ne 0) { throw 'Shared host build failed' }
    if ($env:GOOS -eq 'darwin') {
        & codesign --force --sign - --identifier com.linlay.dbx --entitlements scripts/shared-host-entitlements.plist $hostPath
        if ($LASTEXITCODE -ne 0) { throw 'Host signing failed' }
    }
    & $python.Source -B scripts/verify-shared-host.py --host $hostPath "--ldflags=$linkFlags"
    if ($LASTEXITCODE -ne 0) { throw 'Canonical host verification failed' }
    & go build -tags dbmod -trimpath -ldflags '-w' -o $packPath ./cmd/dbmod-pack
    if ($LASTEXITCODE -ne 0) { throw 'Module builder failed' }
    $engines = if ($Engine -eq 'all') { @('postgres','oracle','dm','sqlserver') } else { @($Engine) }
    foreach ($item in $engines) {
        $module = Join-Path $output "dbx-driver-$item-$($version.TrimStart('v')).dbmod"
        & $packPath --host $hostPath --package "github.com/linlay/cli-dbx/modules/$item" --out $module
        if ($LASTEXITCODE -ne 0) { throw "Module build failed: $item" }
    }
    if ($Test) {
        $fixture = Join-Path $root 'dist/postgres-sqlite-fixture.dbmod'
        $env:DBX_MODULE_BUILD_TAGS = 'dbmod,dbmod_testfixture'
        & $packPath --host $hostPath --package 'github.com/linlay/cli-dbx/modules/postgres' --out $fixture
        if ($LASTEXITCODE -ne 0) { throw 'Test fixture build failed' }
        [Environment]::SetEnvironmentVariable('DBX_MODULE_BUILD_TAGS', $saved['DBX_MODULE_BUILD_TAGS'], 'Process')
        $env:DBX_TEST_SHARED_HOST = $hostPath
        $env:DBX_TEST_SHARED_MODULE = $fixture
        & go test -tags dbmod ./...
        if ($LASTEXITCODE -ne 0) { throw 'Shared host tests failed' }
        & go test github.com/pkujhd/goloader/link -run 'TestFarCompare|TestZeroSized|TestLinkerConstruction|TestPrelink|TestHostRelative|TestInterfaceMethodPointer|TestWeakARM64|TestFarWeakARM64' -count=1
        if ($LASTEXITCODE -ne 0) { throw 'Loader regression tests failed' }
        & $python.Source -B -m unittest discover -s scripts/tests
        if ($LASTEXITCODE -ne 0) { throw 'Canonical host regression tests failed' }
        & $python.Source -B scripts/test-shared-host-repro.py --host $hostPath --packer $packPath
        if ($LASTEXITCODE -ne 0) { throw 'Module reproducibility tests failed' }
        Push-Location modules
        try { & go test -tags dbmod ./...; if ($LASTEXITCODE -ne 0) { throw 'Vendor module tests failed' } } finally { Pop-Location }
        Push-Location drivers/dm/fork
        try { $env:GOWORK = 'off'; & go test ./...; if ($LASTEXITCODE -ne 0) { throw 'DM fork tests failed' } } finally { Pop-Location; $env:GOWORK = Join-Path $root 'dist/shared-host.go.work' }
    }
    & go run scripts/release/package-connector.go --root $root --binary $hostPath --os $env:GOOS --arch $env:GOARCH
    if ($LASTEXITCODE -ne 0) { throw 'Shared connector packaging failed' }
    Copy-Item -LiteralPath (Join-Path $root "dist/$version/builtin.dbx_$($version)_$($env:GOOS)_$($env:GOARCH).zip") -Destination $output
    & $python.Source -B scripts/package-shared-host.py --directory $output --os $env:GOOS --arch $env:GOARCH
    if ($LASTEXITCODE -ne 0) { throw 'Shared host packaging failed' }
    Get-ChildItem -LiteralPath $output -File | Select-Object Name, Length
} finally {
    Pop-Location
    foreach ($name in $saved.Keys) { [Environment]::SetEnvironmentVariable($name, $saved[$name], 'Process') }
}
