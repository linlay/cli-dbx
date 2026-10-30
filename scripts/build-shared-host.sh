#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
test_run=false
case "${1:-}" in --test) test_run=true ;; '') ;; *) echo 'Usage: bash scripts/build-shared-host.sh [--test]' >&2; exit 2 ;; esac
export GOTOOLCHAIN=local
go_version="$(go env GOVERSION)"
export GOOS="$(go env GOHOSTOS)" GOARCH="$(go env GOHOSTARCH)"
[[ "$GOOS/$GOARCH" == darwin/arm64 ]] || { echo 'Use the PowerShell script for Windows x64; this entry requires native Apple Silicon' >&2; exit 1; }
export CGO_ENABLED=1 DBX_MODULE_BUILD_TAGS=dbmod
python_bin="${PYTHON:-python3}"
# SDK preparation validates the supported minor series and dependency minimum.
"$python_bin" -B scripts/prepare-dbmod-toolchain.py --output "dist/$go_version-dbmod"
export GOROOT="$PWD/dist/$go_version-dbmod"
"$python_bin" -B scripts/prepare-shared-workspace.py --output dist/shared-host.go.work
export GOWORK="$PWD/dist/shared-host.go.work"
version="$(tr -d '\r\n' < VERSION)"
output="$PWD/dist/$version/shared-host"
mkdir -p "$output"
host="$output/dbx"
packer="$PWD/dist/dbmod-pack"
commit="$(git rev-parse --short HEAD)"
build_time="$(git show -s --format=%cI HEAD)"
go build -tags dbmod,dbmod_runtime -trimpath -ldflags "-w -X github.com/linlay/cli-dbx/internal/buildinfo.Version=$version -X github.com/linlay/cli-dbx/internal/buildinfo.Commit=$commit -X github.com/linlay/cli-dbx/internal/buildinfo.BuildTime=$build_time" -o "$host" ./cmd/dbx
codesign --force --sign - --identifier com.linlay.dbx --entitlements scripts/shared-host-entitlements.plist "$host"
"$python_bin" -B scripts/verify-shared-host.py --host "$host" --ldflags="-w -X github.com/linlay/cli-dbx/internal/buildinfo.Version=$version -X github.com/linlay/cli-dbx/internal/buildinfo.Commit=$commit -X github.com/linlay/cli-dbx/internal/buildinfo.BuildTime=$build_time"
go build -tags dbmod -trimpath -ldflags '-w' -o "$packer" ./cmd/dbmod-pack
for engine in postgres oracle dm sqlserver; do
  "$packer" --host "$host" --package "github.com/linlay/cli-dbx/modules/$engine" --out "$output/dbx-driver-$engine-${version#v}.dbmod"
done
if [[ "$test_run" == true ]]; then
  fixture="$PWD/dist/postgres-sqlite-fixture.dbmod"
  DBX_MODULE_BUILD_TAGS=dbmod,dbmod_testfixture "$packer" --host "$host" --package github.com/linlay/cli-dbx/modules/postgres --out "$fixture"
  DBX_TEST_SHARED_HOST="$host" DBX_TEST_SHARED_MODULE="$fixture" go test -tags dbmod ./...
  go test github.com/pkujhd/goloader/link -run 'TestFarCompare|TestZeroSized|TestLinkerConstruction|TestPrelink|TestHostRelative|TestInterfaceMethodPointer|TestWeakARM64|TestFarWeakARM64' -count=1
  "$python_bin" -B -m unittest discover -s scripts/tests
  "$python_bin" -B scripts/test-shared-host-repro.py --host "$host" --packer "$packer"
  (cd modules && go test -tags dbmod ./...)
  (cd drivers/dm/fork && GOWORK=off go test ./...)
fi
go run scripts/release/package-connector.go --root "$PWD" --binary "$host" --os "$GOOS" --arch "$GOARCH"
cp "dist/$version/builtin.dbx_${version}_${GOOS}_${GOARCH}.zip" "$output/"
"$python_bin" -B scripts/package-shared-host.py --directory "$output" --os "$GOOS" --arch "$GOARCH"
