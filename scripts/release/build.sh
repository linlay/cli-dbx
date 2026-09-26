#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/release/build.sh

Example:
  scripts/release/build.sh

This script reads the release version from the repository's VERSION file,
builds release archives locally, and does not upload anything.
EOF
}

if [[ "$#" -eq 1 ]] && { [[ "$1" == "-h" ]] || [[ "$1" == "--help" ]]; }; then
  usage
  exit 0
fi

if [[ "$#" -ne 0 ]]; then
  echo "release build does not accept arguments; set the version in VERSION" >&2
  usage >&2
  exit 1
fi

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
version_file="$repo_root/VERSION"
if [[ ! -f "$version_file" ]]; then
  echo "VERSION file not found: $version_file" >&2
  exit 1
fi

version="$(<"$version_file")"
if [[ -z "$version" ]]; then
  echo "VERSION file is empty: $version_file" >&2
  exit 1
fi

if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$ ]]; then
  echo "invalid VERSION value: $version" >&2
  echo "expected a git tag style version such as v0.1.0" >&2
  exit 1
fi

dist_dir="$repo_root/dist/$version"
stage_dir="$dist_dir/.stage"
build_time="$(git -C "$repo_root" show -s --format=%cI HEAD)"
commit="$(git -C "$repo_root" rev-parse --short HEAD)"

mkdir -p "$dist_dir"
rm -rf "$stage_dir"
mkdir -p "$stage_dir"
trap 'rm -rf "$stage_dir"' EXIT

include_license=false
if [[ -f "$repo_root/LICENSE" ]]; then
  include_license=true
else
  echo "warning: LICENSE not found; archives will not include it" >&2
fi

targets=(
  "darwin amd64"
  "darwin arm64"
  "linux amd64"
  "linux arm64"
  "windows amd64"
  "windows arm64"
)

archives=()

for target in "${targets[@]}"; do
  read -r goos goarch <<<"$target"
  binary_name="dbx"
  archive_ext="tar.gz"
  if [[ "$goos" == "windows" ]]; then
    binary_name="dbx.exe"
    archive_ext="zip"
  fi
  archive_name="dbx_${version}_${goos}_${goarch}.${archive_ext}"
  archives+=("$archive_name")
  package_dir="$stage_dir/dbx_${version}_${goos}_${goarch}"

  rm -rf "$package_dir"
  mkdir -p "$package_dir"

  echo "building $goos/$goarch"
  env \
    CGO_ENABLED=0 \
    GOOS="$goos" \
    GOARCH="$goarch" \
    go build \
      -trimpath \
      -ldflags "-s -w -X github.com/linlay/cli-dbx/internal/buildinfo.Version=$version -X github.com/linlay/cli-dbx/internal/buildinfo.Commit=$commit -X github.com/linlay/cli-dbx/internal/buildinfo.BuildTime=$build_time" \
      -o "$package_dir/$binary_name" \
      ./cmd/dbx

  go run "$repo_root/scripts/release/package-connector.go" --root "$repo_root" --binary "$package_dir/$binary_name" --os "$goos" --arch "$goarch"
  archives+=("builtin.dbx_${version}_${goos}_${goarch}.zip")

  cp "$repo_root/README.md" "$package_dir/README.md"
  if [[ "$include_license" == "true" ]]; then
    cp "$repo_root/LICENSE" "$package_dir/LICENSE"
  fi

  python3 "$repo_root/scripts/reproducible-package.py" --stage "$package_dir" --output "$dist_dir/$archive_name"
done

(
  cd "$dist_dir"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "${archives[@]}" > "dbx_${version}_checksums.txt"
  else
    sha256sum "${archives[@]}" > "dbx_${version}_checksums.txt"
  fi
)

echo "release artifacts written to $dist_dir"
