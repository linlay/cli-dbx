#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
host_os="$(go env GOHOSTOS)"
host_arch="$(go env GOHOSTARCH)"
goos="$host_os"
goarch="$host_arch"
engine=""
output_dir=""
usage() {
  cat <<'EOF'
Usage: scripts/build-odbc-drivers.sh [--host | --os <os> --arch <arch>] [--engine oracle|dm|sqlserver] [--output-dir <path>]
Defaults to host platform and all three ODBC adapters. Native Go drivers are built separately.
macOS: native host build with unixODBC/libtool; runtime libraries are bundled flat.
Linux: native host build with unixODBC development/runtime libraries installed.
Windows: amd64 only, cross-build without CGO, using the Windows ODBC Driver Manager.
DBX_UNIXODBC_PREFIX / DBX_LIBTOOL_PREFIX override Homebrew prefixes on macOS.
EOF
}
while [[ $# -gt 0 ]]; do
  case "$1" in
    --help|-h) usage; exit 0 ;;
    --host) goos="$host_os"; goarch="$host_arch"; shift ;;
    --os|--arch|--engine|--output-dir)
      if [[ $# -lt 2 ]]; then usage >&2; exit 1; fi
      case "$1" in
        --os) goos="$2" ;; --arch) goarch="$2" ;;
        --engine) engine="$2" ;; --output-dir) output_dir="$2" ;;
      esac
      shift 2 ;;
    *) usage >&2; exit 1 ;;
  esac
done
case "$goos/$goarch" in
  darwin/arm64|darwin/amd64|linux/amd64|linux/arm64)
    if [[ "$goos/$goarch" != "$host_os/$host_arch" ]]; then
      echo 'ODBC Unix builds require a matching native build host' >&2; exit 1
    fi ;;
  windows/amd64) ;;
  *) echo 'unsupported ODBC target' >&2; exit 1 ;;
esac
engines=(oracle dm sqlserver)
if [[ -n "$engine" ]]; then
  case "$engine" in oracle|dm|sqlserver) engines=("$engine") ;; *) echo 'unsupported ODBC engine' >&2; exit 1 ;; esac
fi
version="$(cat "$repo_root/VERSION")"
stage="$(mktemp -d "${TMPDIR:-/tmp}/dbx-odbc-drivers.XXXXXX")"
trap 'rm -rf "$stage"' EXIT
packager="$stage/package-driver"
if [[ "$host_os" == windows ]]; then packager+=.exe; fi
(cd "$repo_root"; env CGO_ENABLED=0 GOOS="$host_os" GOARCH="$host_arch" go build -trimpath -o "$packager" ./scripts/release/package-driver)
if [[ "$goos" == darwin ]]; then
  unixodbc_prefix="${DBX_UNIXODBC_PREFIX:-$(brew --prefix unixodbc)}"
  libtool_prefix="${DBX_LIBTOOL_PREFIX:-$(brew --prefix libtool)}"
fi
for driver_engine in "${engines[@]}"; do
  binary="$stage/dbx-driver-$driver_engine-odbc"
  build_env=("GOOS=$goos" "GOARCH=$goarch")
  case "$goos" in
    windows) binary+=.exe; build_env+=(CGO_ENABLED=0) ;;
    darwin) build_env+=(CGO_ENABLED=1 "CGO_CFLAGS=-I$unixodbc_prefix/include ${CGO_CFLAGS:-}" "CGO_LDFLAGS=-L$unixodbc_prefix/lib ${CGO_LDFLAGS:-}") ;;
    linux) build_env+=(CGO_ENABLED=1) ;;
  esac
  echo "building ODBC adapter $driver_engine for $goos/$goarch"
  (cd "$repo_root/drivers/odbc"; env "${build_env[@]}" go build -trimpath -ldflags "-s -w -X main.engine=$driver_engine" -o "$binary" .)
  package_args=(--root "$repo_root" --binary "$binary" --engine "$driver_engine" --os "$goos" --arch "$goarch" --adapter odbc)
  if [[ "$goos" == darwin ]]; then
    resources="$stage/$driver_engine-resources"
    python3 "$repo_root/scripts/release/bundle-odbc-macos.py" --binary "$binary" --resources "$resources" --unixodbc "$unixodbc_prefix" --libtool "$libtool_prefix" --version "$version" --arch "$goarch"
    package_args+=(--resources "$resources")
  fi
  if [[ -n "$output_dir" ]]; then package_args+=(--output-dir "$output_dir"); fi
  "$packager" "${package_args[@]}"
done
