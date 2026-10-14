#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/build-drivers.sh [--host | --os <os> --arch <arch>] [--engine <engine>] [--output-dir <path>]

Build and package independent postgres/oracle/dm/sqlserver drivers.
Default: all four engines for darwin/linux/windows, amd64/arm64.
--host builds only the current Go host platform; --engine selects one driver.
Uses the installed toolchain; Oracle requires Go 1.26.6 or newer. No packages are uploaded.
EOF
}

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
host_only=false
target_os=""
target_arch=""
selected_engine=""
output_dir=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --help|-h) usage; exit 0 ;;
    --host) host_only=true; shift ;;
    --os|--arch|--engine|--output-dir)
      if [[ $# -lt 2 ]]; then usage >&2; exit 1; fi
      case "$1" in
        --os) target_os="$2" ;;
        --arch) target_arch="$2" ;;
        --engine) selected_engine="$2" ;;
        --output-dir) output_dir="$2" ;;
      esac
      shift 2
      ;;
    *) usage >&2; exit 1 ;;
  esac
done

if [[ "$host_only" == true ]]; then
  if [[ -n "$target_os$target_arch" ]]; then echo '--host cannot be combined with --os/--arch' >&2; exit 1; fi
  target_os="$(go env GOHOSTOS)"
  target_arch="$(go env GOHOSTARCH)"
fi
targets=("darwin amd64" "darwin arm64" "linux amd64" "linux arm64" "windows amd64" "windows arm64")
if [[ -n "$target_os$target_arch" ]]; then
  case "$target_os/$target_arch" in
    darwin/amd64|darwin/arm64|linux/amd64|linux/arm64|windows/amd64|windows/arm64) targets=("$target_os $target_arch") ;;
    *) echo 'unsupported target; provide both --os and --arch' >&2; exit 1 ;;
  esac
fi
engines=(postgres oracle dm sqlserver)
if [[ -n "$selected_engine" ]]; then
  case "$selected_engine" in
    postgres|oracle|dm|sqlserver) engines=("$selected_engine") ;;
    *) echo 'unsupported external engine' >&2; exit 1 ;;
  esac
fi

stage_dir="$(mktemp -d "${TMPDIR:-/tmp}/dbx-drivers.XXXXXX")"
trap 'rm -rf "$stage_dir"' EXIT
# Build the packaging tool for the host even when GOOS/GOARCH are set by a caller.
packager="$stage_dir/package-driver"
host_os="$(go env GOHOSTOS)"
host_arch="$(go env GOHOSTARCH)"
if [[ "$host_os" == windows ]]; then packager+=.exe; fi
(
  cd "$repo_root"
  env CGO_ENABLED=0 GOOS="$host_os" GOARCH="$host_arch" go build -trimpath -o "$packager" ./scripts/release/package-driver
)

for target in "${targets[@]}"; do
  read -r goos goarch <<<"$target"
  for engine in "${engines[@]}"; do
    executable="dbx-driver-$engine"
    if [[ "$goos" == windows ]]; then executable+=.exe; fi
    binary="$stage_dir/$executable"
    build_env=(CGO_ENABLED=0 "GOOS=$goos" "GOARCH=$goarch")
    echo "building driver $engine for $goos/$goarch"
    (
      cd "$repo_root/drivers/$engine"
      env "${build_env[@]}" go build -trimpath -ldflags '-s -w' -o "$binary" .
    )
    package_args=(--root "$repo_root" --binary "$binary" --engine "$engine" --os "$goos" --arch "$goarch")
    if [[ -n "$output_dir" ]]; then package_args+=(--output-dir "$output_dir"); fi
    "$packager" "${package_args[@]}"
  done
done
