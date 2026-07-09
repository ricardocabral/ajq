#!/usr/bin/env sh
set -eu

AJQ_REPO=${AJQ_REPO:-ricardocabral/ajq}
AJQ_VERSION=${AJQ_VERSION:-latest}
AJQ_INSTALL_DIR=${AJQ_INSTALL_DIR:-"$HOME/.local/bin"}
AJQ_DOWNLOAD_BASE_URL=${AJQ_DOWNLOAD_BASE_URL:-}

log() {
  printf '%s\n' "$*" >&2
}

fail() {
  log "ajq install: $*"
  if [ "${AJQ_INSTALL_SH_SOURCE_ONLY:-0}" = "1" ]; then
    return 1
  fi
  exit 1
}

normalize_os() {
  os=${1:-$(uname -s)}
  case "$os" in
    Darwin | darwin) printf 'Darwin' ;;
    Linux | linux) printf 'Linux' ;;
    *) fail "unsupported operating system: $os" ;;
  esac
}

normalize_arch() {
  arch=${1:-$(uname -m)}
  case "$arch" in
    x86_64 | amd64) printf 'x86_64' ;;
    arm64 | aarch64) printf 'arm64' ;;
    *) fail "unsupported architecture: $arch" ;;
  esac
}

archive_name_from_checksums() {
  checksums=$1
  os=$2
  arch=$3
  suffix="_${os}_${arch}.tar.gz"
  if archive=$(awk -v suffix="$suffix" '
    $2 ~ /^ajq_/ && substr($2, length($2) - length(suffix) + 1) == suffix {
      print $2
      count++
    }
    END {
      if (count == 0) exit 1
      if (count > 1) exit 2
    }
  ' "$checksums"); then
    printf '%s' "$archive"
  else
    status=$?
    if [ "$status" -eq 2 ]; then
      fail "multiple checksum entries found for ajq_*_${os}_${arch}.tar.gz"
    fi
    fail "checksum entry not found for ajq_*_${os}_${arch}.tar.gz"
  fi
}

release_ref() {
  case "$AJQ_VERSION" in
    latest | v*) printf '%s' "$AJQ_VERSION" ;;
    *) printf 'v%s' "$AJQ_VERSION" ;;
  esac
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

sha256_file() {
  file=$1
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
  else
    fail "required command not found: sha256sum or shasum"
  fi
}

fetch() {
  src=$1
  dest=$2
  case "$src" in
    file://*)
      cp "${src#file://}" "$dest"
      ;;
    /* | ./* | ../*)
      cp "$src" "$dest"
      ;;
    http://* | https://*)
      if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$src" -o "$dest"
      elif command -v wget >/dev/null 2>&1; then
        wget -qO "$dest" "$src"
      else
        fail "required command not found: curl or wget"
      fi
      ;;
    *)
      cp "$src" "$dest"
      ;;
  esac
}

asset_url() {
  name=$1
  if [ -n "$AJQ_DOWNLOAD_BASE_URL" ]; then
    base=${AJQ_DOWNLOAD_BASE_URL%/}
    printf '%s/%s' "$base" "$name"
  elif [ "$AJQ_VERSION" = "latest" ]; then
    printf 'https://github.com/%s/releases/latest/download/%s' "$AJQ_REPO" "$name"
  else
    printf 'https://github.com/%s/releases/download/%s/%s' "$AJQ_REPO" "$(release_ref)" "$name"
  fi
}

verify_checksum() {
  checksums=$1
  archive=$2
  archive_file=$3
  expected=$(awk -v name="$archive" '$2 == name {print $1; found=1} END {if (!found) exit 1}' "$checksums") || \
    fail "checksum entry not found for $archive"
  actual=$(sha256_file "$archive_file")
  [ "$actual" = "$expected" ] || fail "checksum mismatch for $archive"
}

install_archive() {
  archive_file=$1
  install_dir=$2
  need_cmd tar
  mkdir -p "$install_dir"
  tmp_extract=$3/extract
  mkdir -p "$tmp_extract"
  tar -xzf "$archive_file" -C "$tmp_extract"
  [ -f "$tmp_extract/ajq" ] || fail "archive did not contain ajq binary"
  chmod 0755 "$tmp_extract/ajq"
  cp "$tmp_extract/ajq" "$install_dir/ajq"
}

main() {
  os=$(normalize_os "${AJQ_TEST_OS:-}")
  arch=$(normalize_arch "${AJQ_TEST_ARCH:-}")

  tmp=${TMPDIR:-/tmp}/ajq-install.$$
  trap 'rm -rf "$tmp"' EXIT INT TERM
  mkdir -p "$tmp"

  fetch "$(asset_url checksums.txt)" "$tmp/checksums.txt"
  archive=$(archive_name_from_checksums "$tmp/checksums.txt" "$os" "$arch")

  log "Downloading $archive"
  fetch "$(asset_url "$archive")" "$tmp/$archive"
  verify_checksum "$tmp/checksums.txt" "$archive" "$tmp/$archive"
  install_archive "$tmp/$archive" "$AJQ_INSTALL_DIR" "$tmp"
  log "Installed ajq to $AJQ_INSTALL_DIR/ajq"
}

if [ "${AJQ_INSTALL_SH_SOURCE_ONLY:-0}" != "1" ]; then
  main "$@"
fi
