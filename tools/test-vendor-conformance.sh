#!/usr/bin/env bash
# Exercises vendor-conformance.sh offline, against a stub `gh` that serves fake
# releases built from this repo's own vectors. Each release's commit is the sha1
# of its ref, and anything not in the stub's tag list is a branch. Like the real
# gh, the stub writes an error body to stdout when a request fails.
#
#   tools/test-vendor-conformance.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VENDOR="$ROOT/tools/vendor-conformance.sh"
VERIFY="$ROOT/tools/verify-pin.sh"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

export FAKE_ORIGIN="$work/origin"
export CONFORMANCE_REPO="fake/conformance"
mkdir -p "$work/bin" "$FAKE_ORIGIN"
PATH="$work/bin:$PATH"

cat > "$work/bin/gh" <<'STUB'
#!/usr/bin/env bash
commit_of() { printf '%s' "$1" | shasum | cut -d' ' -f1; }
not_found() { echo '{"message":"Not Found","status":"404"}'; echo "gh: Not Found (HTTP 404)" >&2; exit 1; }
for arg; do case "$arg" in repos/*) endpoint="$arg" ;; esac; done
case "$endpoint" in
  */tags) head -n1 "$FAKE_ORIGIN/tags" ;;
  */commits/refs/tags/*)
    tag="${endpoint##*/refs/tags/}"
    grep -qxF "$tag" "$FAKE_ORIGIN/tags" || not_found
    commit_of "$tag" ;;
  */contents/*)
    rest="${endpoint#*/contents/}"
    for ref in "$FAKE_ORIGIN"/*/; do
      ref="$(basename "$ref")"
      if [ "$(commit_of "$ref")" = "${rest#*\?ref=}" ] && [ -f "$FAKE_ORIGIN/$ref/${rest%%\?*}" ]; then
        exec cat "$FAKE_ORIGIN/$ref/${rest%%\?*}"
      fi
    done
    not_found ;;
  *) echo "stub gh: unexpected call: $*" >&2; exit 1 ;;
esac
STUB
chmod +x "$work/bin/gh"

# release <ref> <package>...: publishes a release at <ref> holding the named
# packages' vectors, with a manifest that matches them.
release() {
  local ref="$1" dir="$FAKE_ORIGIN/$1"
  shift
  mkdir -p "$dir/vectors"
  echo "$ref" > "$dir/VERSION"
  local files="[]"
  for pkg in "$@"; do
    cp "$ROOT/vectors/$pkg.json" "$dir/vectors/$pkg.json"
    files="$(jq --arg p "vectors/$pkg.json" \
      --arg s "$(shasum -a 256 "$dir/vectors/$pkg.json" | cut -d' ' -f1)" \
      --arg k "$pkg" --argjson c "$(jq '.cases | length' "$dir/vectors/$pkg.json")" \
      '. + [{path: $p, sha256: $s, package: $k, cases: $c}]' <<< "$files")"
  done
  jq -n --arg v "$ref" --argjson f "$files" \
    '{version: $v, source: "test", files: $f, totalCases: ($f | map(.cases) | add)}' > "$dir/manifest.json"
}

release v1 bitmask encoding errors numbers retry
release v2 bitmask encoding errors numbers

release main bitmask

release v3-tampered bitmask errors
printf ' ' >> "$FAKE_ORIGIN/v3-tampered/vectors/errors.json"

release v4-stale-version bitmask
echo v1 > "$FAKE_ORIGIN/v4-stale-version/VERSION"

release v5-escape bitmask
jq '.files[0].path = "vectors/../../escaped.json"' "$FAKE_ORIGIN/v5-escape/manifest.json" > "$work/m"
mv "$work/m" "$FAKE_ORIGIN/v5-escape/manifest.json"

# Newest first, as GitHub lists them. main is deliberately absent: it is a
# branch. v2 leads so verify-pin.sh accepts a successful v2 vendoring.
printf '%s\n' v2 v1 v3-tampered v4-stale-version v5-escape > "$FAKE_ORIGIN/tags"

failures=0
pass() { echo "ok   $1"; }
fail() { echo "FAIL $1: $2"; failures=$((failures + 1)); }

# fresh_port <name>: a port directory with v1 vendored, as a port that is behind.
fresh_port() {
  local port="$work/ports/$1/testdata/conformance"
  mkdir -p "$port"
  cp -R "$FAKE_ORIGIN/v1/." "$port/"
  echo "$port"
}

# refused <ref> <error> <name>: vendoring <ref> must fail with <error> and leave
# the port's v1 tree byte-for-byte as it was.
refused() {
  local ref="$1" want="$2" name="$3" id port snapshot log
  id="refused-$ref"
  port="$(fresh_port "$id")"
  snapshot="$work/snapshots/$id"
  log="$work/$id.log"
  mkdir -p "$snapshot"
  cp -R "$port/." "$snapshot/"
  if (cd "$work/ports/$id" && "$VENDOR" "$ref" > "$log" 2>&1); then
    fail "$name" "vendoring $ref succeeded"
  elif ! grep -qF "::error::$want" "$log"; then
    fail "$name" "expected '::error::$want', got: $(cat "$log")"
  elif ! diff -r "$snapshot" "$port" > /dev/null; then
    fail "$name" "the port's tree changed: $(diff -rq "$snapshot" "$port" | head -n1)"
  else
    pass "$name"
  fi
}

test_vendors_into_an_empty_port() {
  local name="vendors into an empty port" dir="$work/ports/empty"
  mkdir -p "$dir"
  if ! (cd "$dir" && "$VENDOR" v2 > "$work/empty.log" 2>&1); then
    fail "$name" "$(cat "$work/empty.log")"
  elif ! diff -r "$FAKE_ORIGIN/v2" "$dir/testdata/conformance" > /dev/null; then
    fail "$name" "vendored tree differs from v2"
  elif ! (cd "$dir" && "$VERIFY" testdata/conformance > /dev/null 2>&1); then
    fail "$name" "verify-pin.sh rejected the result"
  else
    pass "$name"
  fi
}

test_upgrade_removes_withdrawn_vectors() {
  local name="upgrade removes withdrawn vectors" port
  port="$(fresh_port upgrade)"
  echo '{}' > "$port/vectors/local-extra.json"
  if ! (cd "$work/ports/upgrade" && "$VENDOR" v2 > "$work/upgrade.log" 2>&1); then
    fail "$name" "$(cat "$work/upgrade.log")"
  elif [ -e "$port/vectors/retry.json" ] || [ -e "$port/vectors/local-extra.json" ]; then
    fail "$name" "unlisted vectors survived: $(ls "$port/vectors")"
  elif ! diff -r "$FAKE_ORIGIN/v2" "$port" > /dev/null; then
    fail "$name" "vendored tree differs from v2"
  elif ! (cd "$work/ports/upgrade" && "$VERIFY" testdata/conformance > /dev/null 2>&1); then
    fail "$name" "verify-pin.sh rejected the result"
  else
    pass "$name"
  fi
}

test_vendors_into_a_named_directory() {
  local name="vendors into a named directory" dir="$work/ports/named"
  mkdir -p "$dir"
  if ! (cd "$dir" && "$VENDOR" v2 conformance > "$work/named.log" 2>&1); then
    fail "$name" "$(cat "$work/named.log")"
  elif [ "$(cat "$dir/conformance/VERSION")" != v2 ]; then
    fail "$name" "VERSION is not v2"
  else
    pass "$name"
  fi
}

test_vendors_into_an_empty_port
test_upgrade_removes_withdrawn_vectors
test_vendors_into_a_named_directory
refused main "main is not a tag" "refuses a branch"
refused v9 "v9 is not a tag" "refuses an unknown tag"
refused v3-tampered "vectors/errors.json does not match" "refuses a vector that fails its checksum"
refused v4-stale-version "v4-stale-version carries VERSION v1" "refuses a tag whose VERSION was not bumped"
refused v5-escape "manifest lists an unexpected path" "refuses a manifest path outside vectors/"

[ "$failures" -eq 0 ] || { echo "$failures failed"; exit 1; }
echo "all passed"
