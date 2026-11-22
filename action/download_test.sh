#!/usr/bin/env bash
# Tests action/download.sh against a local fake of GitHub's API and release
# downloads. Run: action/download_test.sh (needs python3, curl, tar).
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null || true; wait 2>/dev/null || true; rm -rf "$work"' EXIT

# sha256sum is GNU; macOS has shasum.
if ! command -v sha256sum >/dev/null; then
  mkdir -p "$work/bin"; printf '#!/bin/sh\nexec shasum -a 256 "$@"\n' > "$work/bin/sha256sum"; chmod 755 "$work/bin/sha256sum"
  export PATH="$work/bin:$PATH"
fi

# A fake release v1.2.3 with an amd64 archive.
rel="$work/srv/o/r/releases/download/v1.2.3"; mkdir -p "$rel" "$work/pkg"
printf '#!/bin/sh\necho fake tf-plan-cost\n' > "$work/pkg/tf-plan-cost"; chmod 755 "$work/pkg/tf-plan-cost"
tar -czf "$rel/tf-plan-cost_1.2.3_linux_amd64.tar.gz" -C "$work/pkg" tf-plan-cost
(cd "$rel" && sha256sum tf-plan-cost_1.2.3_linux_amd64.tar.gz > checksums.txt)
mkdir -p "$work/srv/api/repos/o/r"
cat > "$work/srv/api/repos/o/r/tags" <<'JSON'
[{"name":"v1","commit":{"sha":"aaa"}},{"name":"v1.2.3","commit":{"sha":"aaa"}},{"name":"v1.2.2","commit":{"sha":"bbb"}},{"name":"v2","commit":{"sha":"ccc"}}]
JSON

port=$((20000 + RANDOM % 20000))
(cd "$work/srv" && python3 -c '
import http.server, sys
class H(http.server.SimpleHTTPRequestHandler):
    def translate_path(self, path): return super().translate_path(path.split("?")[0])
    def log_message(self, *a): pass
http.server.ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()' "$port" >/dev/null 2>&1) &
for _ in $(seq 50); do curl -fs "http://127.0.0.1:$port/api/repos/o/r/tags" >/dev/null && break; sleep 0.1; done

fail=0
run() { # name, expected done value, env...
  local name=$1 want=$2; shift 2
  local tmp="$work/run-$RANDOM"; mkdir -p "$tmp"
  if ! env RUNNER_TEMP="$tmp" GITHUB_OUTPUT="$tmp/out" RUNNER_OS=Linux RUNNER_ARCH=X64 ACTION_REPO=o/r \
      TPC_API_URL="http://127.0.0.1:$port/api" TPC_DOWNLOAD_URL="http://127.0.0.1:$port" "$@" \
      "$here/download.sh" > "$tmp/log" 2>&1; then
    got=error
  else
    got=$(sed -n 's/^done=//p' "$tmp/out" 2>/dev/null || true)
  fi
  if [ "$got" = "$want" ]; then
    if [ "$want" = true ] && [ "$("$tmp/tf-plan-cost/tf-plan-cost")" != "fake tf-plan-cost" ]; then
      echo "FAIL $name: binary not installed"; fail=1; return
    fi
    echo "ok   $name"
  else
    echo "FAIL $name: done=$got, want $want"; sed 's/^/     /' "$tmp/log"; fail=1
  fi
}

run "exact release tag"           true  ACTION_REF=v1.2.3
run "major tag resolves to v1.2.3" true ACTION_REF=v1
run "major tag without release"    false ACTION_REF=v2
run "with a token"                 true  ACTION_REF=v1 GH_TOKEN=t
run "branch builds from source"    false ACTION_REF=main
run "SHA builds from source"       false ACTION_REF=0123456789abcdef0123456789abcdef01234567
run "missing release"              false ACTION_REF=v9.9.9
run "no prebuilt arm64 archive"    false ACTION_REF=v1.2.3 RUNNER_ARCH=ARM64
run "macOS runner"                 false ACTION_REF=v1.2.3 RUNNER_OS=macOS
run "local action (no ref)"        false ACTION_REF=

# A tampered archive must fail, not fall back.
echo "tampered" >> "$rel/tf-plan-cost_1.2.3_linux_amd64.tar.gz"
run "checksum mismatch fails"      error ACTION_REF=v1.2.3

exit $fail
