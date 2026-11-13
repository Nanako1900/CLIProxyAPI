#!/usr/bin/env bash
set -euo pipefail

# Rebuild the upstream panel with the small native CPA extension in this tree.
extension_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source_dir="${1:?Usage: build.sh SOURCE_DIR OUTPUT_HTML}"
output_html="${2:?Usage: build.sh SOURCE_DIR OUTPUT_HTML}"
revision=953fb2444350c6c36052dda46cac0abce6b23ea6
checksum=5a3020cbb15ca08f370f28684032d41327ffce881c0a9866fee5eba091e7bee4

if [ -d "$source_dir" ] && [ -n "$(ls -A "$source_dir")" ]; then
  echo 'SOURCE_DIR must be an empty build directory.' >&2
  exit 1
fi
mkdir -p "$source_dir" "$(dirname "$output_html")"
source_dir="$(cd "$source_dir" && pwd)"
output_html="$(cd "$(dirname "$output_html")" && pwd)/$(basename "$output_html")"
archive="$(mktemp)"
trap 'rm -f "$archive"' EXIT
curl --fail --location --silent --show-error \
  "https://codeload.github.com/router-for-me/Cli-Proxy-API-Management-Center/tar.gz/$revision" \
  -o "$archive"
actual="$(bun -e 'const f = Bun.file(process.argv[1]); console.log(new Bun.CryptoHasher("sha256").update(await f.arrayBuffer()).digest("hex"));' "$archive")"
if [ "$actual" != "$checksum" ]; then
  echo 'Management source checksum does not match the pinned revision.' >&2
  exit 1
fi
tar -xzf "$archive" --strip-components=1 -C "$source_dir"
cd "$source_dir"
git apply "$extension_dir/native-settings.patch"
git apply "$extension_dir/oauth-socks.patch"
git apply "$extension_dir/kiro-json-import.patch"
git apply "$extension_dir/account-pools.patch"
git apply "$extension_dir/user-agents.patch"
cp -R "$extension_dir/overlay/." .
bun install --frozen-lockfile
bun run verify
cp dist/index.html "$output_html"
cp LICENSE "$(dirname "$output_html")/management-LICENSE.txt"
