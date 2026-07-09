#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
INSTALL_SH="$ROOT_DIR/scripts/install.sh"
TMP_ROOT=$(mktemp -d)
trap 'rm -rf "$TMP_ROOT"' EXIT

fail() {
  echo "install_test: $*" >&2
  exit 1
}

assert_eq() {
  local want=$1 got=$2 msg=$3
  [[ "$got" == "$want" ]] || fail "$msg: want '$want', got '$got'"
}

sha256_write() {
  local file=$1 name=$2 out=$3
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk -v name="$name" '{print $1 "  " name}' >"$out"
  else
    shasum -a 256 "$file" | awk -v name="$name" '{print $1 "  " name}' >"$out"
  fi
}

# Source helper functions without running the installer.
# shellcheck disable=SC1090
AJQ_INSTALL_SH_SOURCE_ONLY=1 . "$INSTALL_SH"

assert_eq Darwin "$(normalize_os Darwin)" "Darwin OS mapping"
assert_eq Linux "$(normalize_os linux)" "Linux OS mapping"
assert_eq x86_64 "$(normalize_arch x86_64)" "x86_64 arch mapping"
assert_eq x86_64 "$(normalize_arch amd64)" "amd64 arch mapping"
assert_eq arm64 "$(normalize_arch arm64)" "arm64 arch mapping"
assert_eq arm64 "$(normalize_arch aarch64)" "aarch64 arch mapping"

assert_eq "https://github.com/owner/repo/releases/download/v0.1.2/checksums.txt" \
  "$(AJQ_REPO=owner/repo AJQ_DOWNLOAD_BASE_URL='' AJQ_VERSION=0.1.2 asset_url checksums.txt)" \
  "bare explicit version URL mapping"
assert_eq "https://github.com/owner/repo/releases/download/v0.1.2/checksums.txt" \
  "$(AJQ_REPO=owner/repo AJQ_DOWNLOAD_BASE_URL='' AJQ_VERSION=v0.1.2 asset_url checksums.txt)" \
  "tag explicit version URL mapping"
assert_eq "https://github.com/owner/repo/releases/latest/download/checksums.txt" \
  "$(AJQ_REPO=owner/repo AJQ_DOWNLOAD_BASE_URL='' AJQ_VERSION=latest asset_url checksums.txt)" \
  "latest URL mapping"

VERSION=9.9.9-next
OS_NAME=Darwin
ARCH_NAME=arm64
ARCHIVE="ajq_${VERSION}_${OS_NAME}_${ARCH_NAME}.tar.gz"
FIXTURE_DIR="$TMP_ROOT/fixture"
DIST_DIR="$TMP_ROOT/dist"
INSTALL_DIR="$TMP_ROOT/install"
mkdir -p "$FIXTURE_DIR" "$DIST_DIR" "$INSTALL_DIR"
cat >"$FIXTURE_DIR/ajq" <<'SH'
#!/usr/bin/env sh
echo "ajq 9.9.9-next"
SH
chmod +x "$FIXTURE_DIR/ajq"
tar -C "$FIXTURE_DIR" -czf "$DIST_DIR/$ARCHIVE" ajq
sha256_write "$DIST_DIR/$ARCHIVE" "$ARCHIVE" "$DIST_DIR/checksums.txt"

AJQ_VERSION=$VERSION \
AJQ_INSTALL_DIR=$INSTALL_DIR \
AJQ_DOWNLOAD_BASE_URL=$DIST_DIR \
AJQ_TEST_OS=$OS_NAME \
AJQ_TEST_ARCH=$ARCH_NAME \
  "$INSTALL_SH" >/"$TMP_ROOT/install.out" 2>/"$TMP_ROOT/install.err"

[[ -x "$INSTALL_DIR/ajq" ]] || fail "installer did not write executable"
assert_eq "ajq 9.9.9-next" "$("$INSTALL_DIR/ajq")" "installed binary output"

LATEST_VERSION=v10.0.0
LATEST_ARCHIVE="ajq_${LATEST_VERSION}_${OS_NAME}_${ARCH_NAME}.tar.gz"
LATEST_FIXTURE_DIR="$TMP_ROOT/latest-fixture"
LATEST_DIST="$TMP_ROOT/latest-dist"
LATEST_INSTALL_DIR="$TMP_ROOT/latest-install"
mkdir -p "$LATEST_FIXTURE_DIR" "$LATEST_DIST" "$LATEST_INSTALL_DIR"
cat >"$LATEST_FIXTURE_DIR/ajq" <<'SH'
#!/usr/bin/env sh
echo "ajq v10.0.0"
SH
chmod +x "$LATEST_FIXTURE_DIR/ajq"
tar -C "$LATEST_FIXTURE_DIR" -czf "$LATEST_DIST/$LATEST_ARCHIVE" ajq
sha256_write "$LATEST_DIST/$LATEST_ARCHIVE" "$LATEST_ARCHIVE" "$LATEST_DIST/checksums.txt"

AJQ_VERSION=latest \
AJQ_INSTALL_DIR=$LATEST_INSTALL_DIR \
AJQ_DOWNLOAD_BASE_URL=$LATEST_DIST \
AJQ_TEST_OS=$OS_NAME \
AJQ_TEST_ARCH=$ARCH_NAME \
  "$INSTALL_SH" >/"$TMP_ROOT/latest.out" 2>/"$TMP_ROOT/latest.err"

[[ -x "$LATEST_INSTALL_DIR/ajq" ]] || fail "latest installer did not write executable"
assert_eq "ajq v10.0.0" "$("$LATEST_INSTALL_DIR/ajq")" "latest installed binary output"

BAD_DIST="$TMP_ROOT/bad-dist"
mkdir -p "$BAD_DIST"
cp "$LATEST_DIST/$LATEST_ARCHIVE" "$BAD_DIST/$LATEST_ARCHIVE"
printf '0000000000000000000000000000000000000000000000000000000000000000  %s\n' "$LATEST_ARCHIVE" >"$BAD_DIST/checksums.txt"
if AJQ_VERSION=latest \
  AJQ_INSTALL_DIR="$TMP_ROOT/bad-install" \
  AJQ_DOWNLOAD_BASE_URL=$BAD_DIST \
  AJQ_TEST_OS=$OS_NAME \
  AJQ_TEST_ARCH=$ARCH_NAME \
  "$INSTALL_SH" >/"$TMP_ROOT/bad.out" 2>/"$TMP_ROOT/bad.err"; then
  fail "installer should fail on checksum mismatch"
fi
grep -q "checksum mismatch" "$TMP_ROOT/bad.err" || fail "checksum failure did not explain mismatch"

echo "install_test: ok"
