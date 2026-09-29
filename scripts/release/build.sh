#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"

build_macos() (
  if [[ "$(uname -s)/$(uname -m)" != "Darwin/arm64" ]]; then
    echo "macOS ODBC releases must be built on an Apple Silicon Mac" >&2
    exit 1
  fi
  binary="$1"
  ldflags="${2:--s -w}"
  unixodbc_prefix="${DBX_UNIXODBC_PREFIX:-$(brew --prefix unixodbc)}"
  libtool_prefix="${DBX_LIBTOOL_PREFIX:-$(brew --prefix libtool)}"

  cd "$repo_root"
  CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
    CGO_CFLAGS="-I$unixodbc_prefix/include ${CGO_CFLAGS:-}" \
    CGO_LDFLAGS="-L$unixodbc_prefix/lib ${CGO_LDFLAGS:-}" \
    go build -tags odbc -trimpath -ldflags "$ldflags" -o "$binary" ./cmd/dbx

  binary="$(cd "$(dirname "$binary")" && pwd)/$(basename "$binary")"
  package_dir="$(dirname "$binary")"
  sources=("$unixodbc_prefix/lib/libodbc.2.dylib"
           "$unixodbc_prefix/lib/libodbcinst.2.dylib"
           "$unixodbc_prefix/lib/libodbccr.2.dylib"
           "$libtool_prefix/lib/libltdl.7.dylib")
  dependencies="$(otool -L "$binary")"
  if [[ "$dependencies" != *"/libodbc.2.dylib ("* ]]; then
    echo "DBX must be built with CGO_ENABLED=1 and -tags odbc" >&2
    exit 1
  fi
  for path in "$binary" "${sources[@]}"; do
    archs="$(lipo -archs "$path")"
    if [[ " $archs " != *" arm64 "* ]]; then
      echo "missing ARM64 architecture: $path" >&2
      exit 1
    fi
  done

  mkdir -p "$package_dir/lib"
  libraries=()
  for source in "${sources[@]}"; do
    target="$package_dir/lib/$(basename "$source")"
    cp -L "$source" "$target"
    chmod 755 "$target"
    libraries+=("$target")
  done
  for path in "${libraries[@]}" "$binary"; do
    dependencies="$(otool -L "$path")"
    skip=2
    relative="@loader_path/"
    if [[ "$path" == "$binary" ]]; then
      skip=1
      relative="@executable_path/lib/"
    fi
    dependencies="$(printf '%s\n' "$dependencies" | sed "1,${skip}d; s/^[[:space:]]*//; s/ (compatibility version.*$//")"
    while IFS= read -r dependency; do
      case "$dependency" in ""|/usr/lib/*|/System/Library/*) continue ;; esac
      name="$(basename "$dependency")"
      case "$name" in
        libodbc.2.dylib|libodbcinst.2.dylib|libodbccr.2.dylib|libltdl.7.dylib) ;;
        *) echo "unbundled dependency in $path: $dependency" >&2; exit 1 ;;
      esac
      install_name_tool -change "$dependency" "$relative$name" "$path"
    done <<< "$dependencies"
    if [[ "$path" != "$binary" ]]; then
      install_name_tool -id "@rpath/$(basename "$path")" "$path"
    else
      commands="$(otool -l "$path")"
      if [[ "$commands" != *"path @executable_path/lib "* ]]; then
        install_name_tool -add_rpath "@executable_path/lib" "$path"
      fi
    fi
    # Only rewrite and sign package copies, never the installed libraries.
    codesign --force --sign - "$path"
    codesign --verify --strict "$path"
  done

  mkdir -p "$package_dir/licenses/unixODBC" "$package_dir/licenses/libltdl"
  cp "$unixodbc_prefix/COPYING" "$unixodbc_prefix/AUTHORS" "$package_dir/licenses/unixODBC/"
  cp "$libtool_prefix/share/libtool/COPYING.LIB" "$libtool_prefix/AUTHORS" "$package_dir/licenses/libltdl/"
  cat > "$package_dir/licenses/README.txt" <<'EOF'
unixODBC and libltdl are distributed as replaceable shared libraries.
Upstream source: https://www.unixodbc.org/ and https://www.gnu.org/software/libtool/
Release builders must provide the corresponding library sources with the release.
DBX packaging changes library load paths and applies local ad-hoc signatures.
Vendor database drivers and their dependencies are not included.
EOF
)

usage() {
  cat <<'EOF'
Usage: scripts/release/build.sh
       scripts/release/build.sh --macos-binary <output-binary> [go-ldflags]

Example:
  scripts/release/build.sh

This script reads the release version from the repository's VERSION file,
builds release archives locally, and does not upload anything.
--macos-binary builds only the macOS CLI and its bundled ODBC runtime.
EOF
}

if [[ "$#" -eq 1 ]] && { [[ "$1" == "-h" ]] || [[ "$1" == "--help" ]]; }; then
  usage
  exit 0
fi

if [[ "${1:-}" == "--macos-binary" ]]; then
  if [[ "$#" -lt 2 || "$#" -gt 3 ]]; then
    usage >&2
    exit 1
  fi
  shift
  build_macos "$@"
  exit 0
fi

if [[ "$#" -ne 0 ]]; then
  echo "release build does not accept arguments; set the version in VERSION" >&2
  usage >&2
  exit 1
fi

cd "$repo_root"
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
  "linux amd64"
  "linux arm64"
  "windows amd64"
)
if [[ "$(uname -s)/$(uname -m)" == "Darwin/arm64" ]]; then
  targets=("darwin arm64" "${targets[@]}")
else
  echo "macOS unixODBC package must be built on Apple Silicon; building Linux/Windows only"
fi

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
  ldflags="-s -w -X github.com/linlay/cli-dbx/internal/buildinfo.Version=$version -X github.com/linlay/cli-dbx/internal/buildinfo.Commit=$commit -X github.com/linlay/cli-dbx/internal/buildinfo.BuildTime=$build_time"
  if [[ "$goos" == "darwin" ]]; then
    build_macos "$package_dir/$binary_name" "$ldflags"
  else
    env \
    CGO_ENABLED=0 \
    GOOS="$goos" \
    GOARCH="$goarch" \
    go build \
      -trimpath \
      -ldflags "$ldflags" \
      -o "$package_dir/$binary_name" \
      ./cmd/dbx
  fi

  go run "$repo_root/scripts/release/package-connector.go" --root "$repo_root" --binary "$package_dir/$binary_name" --os "$goos" --arch "$goarch"
  archives+=("builtin.dbx_${version}_${goos}_${goarch}.zip")

  cp "$repo_root/README.md" "$package_dir/README.md"
  mkdir -p "$package_dir/examples"
  cp "$repo_root"/examples/config.*.toml "$package_dir/examples/"
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
