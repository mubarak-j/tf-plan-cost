#!/usr/bin/env bash
# Downloads the released tf-plan-cost binary that matches the action's ref,
# so the action doesn't need Go. Prints "done=true" to $GITHUB_OUTPUT when it
# installed $RUNNER_TEMP/tf-plan-cost/tf-plan-cost; otherwise (branch or SHA
# refs, non-Linux runners, no matching release) the action builds from source.
#
# Inputs (environment):
#   ACTION_REPO  owner/repo of the action (github.action_repository)
#   ACTION_REF   ref in `uses:` (github.action_ref): v1.2.3, v1, main, a SHA
#   GH_TOKEN     optional, avoids API rate limits
#   TPC_API_URL, TPC_DOWNLOAD_URL  override https://api.github.com and
#                                  https://github.com (tests)
set -euo pipefail

api="${TPC_API_URL:-https://api.github.com}"
dl="${TPC_DOWNLOAD_URL:-https://github.com}"
out="${RUNNER_TEMP:?}/tf-plan-cost"
ref="${ACTION_REF:-}"
repo="${ACTION_REPO:-}"

skip() { echo "Building from source: $1"; echo "done=false" >> "${GITHUB_OUTPUT:-/dev/null}"; exit 0; }

[ -n "$repo" ] && [ -n "$ref" ] || skip "no action ref (local action?)"
[ "${RUNNER_OS:-}" = "Linux" ] || skip "no prebuilt binary for ${RUNNER_OS:-this OS}"
case "${RUNNER_ARCH:-}" in
  X64) arch=amd64 ;;
  ARM64) arch=arm64 ;;
  *) skip "no prebuilt binary for ${RUNNER_ARCH:-this architecture}" ;;
esac

curl_api() {
  local auth=()
  [ -n "${GH_TOKEN:-}" ] && auth=(-H "Authorization: Bearer $GH_TOKEN")
  curl -fsSL --retry 3 ${auth[@]+"${auth[@]}"} -H "Accept: application/vnd.github+json" "$api/$1"
}

if [[ "$ref" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  tag="$ref"
elif [[ "$ref" =~ ^v[0-9]+$ ]]; then
  # A major tag (v1) points at the commit of the latest v1.x.y release: find
  # that release tag, so the binary matches the action's source exactly.
  tags=$(curl_api "repos/$repo/tags?per_page=100") || skip "could not list tags"
  tag=$(printf '%s' "$tags" | python3 -c '
import json, re, sys
ref = sys.argv[1]
tags = json.load(sys.stdin)
sha = next((t["commit"]["sha"] for t in tags if t["name"] == ref), None)
names = [t["name"] for t in tags if t["commit"]["sha"] == sha and re.fullmatch(re.escape(ref) + r"\.\d+\.\d+", t["name"])]
print(names[0] if names else "")
' "$ref") || skip "could not read tags"
  [ -n "$tag" ] || skip "no release found for $ref"
else
  skip "ref $ref is not a release tag"
fi

version="${tag#v}"
archive="tf-plan-cost_${version}_linux_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
base="$dl/$repo/releases/download/$tag"
curl -fsSL --retry 3 -o "$tmp/$archive" "$base/$archive" || skip "could not download $archive"
curl -fsSL --retry 3 -o "$tmp/checksums.txt" "$base/checksums.txt" || skip "could not download checksums.txt"

# A checksum mismatch is an error, not a reason to fall back.
(cd "$tmp" && grep "  $archive\$" checksums.txt | sha256sum -c --quiet -) || {
  echo "::error title=tf-plan-cost::checksum mismatch for $archive from $tag"
  exit 1
}

mkdir -p "$out"
tar -xzf "$tmp/$archive" -C "$out" tf-plan-cost
chmod 755 "$out/tf-plan-cost"
echo "Installed tf-plan-cost $tag ($arch) from the release."
echo "done=true" >> "${GITHUB_OUTPUT:-/dev/null}"
