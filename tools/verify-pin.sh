#!/usr/bin/env bash
# Verify a port's vendored conformance vectors against the newest published tag.
#
# Run from a port's CI, pointed at its vendored directory:
#   tools/verify-pin.sh testdata/conformance
#
# Exits non-zero when the port is behind, so drift is a red build rather than a
# silent divergence. Needs `gh` (or any authenticated curl) and `jq`.
set -euo pipefail

VENDORED="${1:-testdata/conformance}"
REPO="${CONFORMANCE_REPO:-primandproper/primitives-conformance}"

[ -f "$VENDORED/VERSION" ] || { echo "::error::no $VENDORED/VERSION — is the suite vendored?"; exit 1; }
pinned="$(tr -d '[:space:]' < "$VENDORED/VERSION")"

latest="$(gh api "repos/$REPO/tags" --jq '.[0].name' 2>/dev/null || true)"
[ -n "$latest" ] || { echo "::error::could not read tags from $REPO"; exit 1; }

if [ "$pinned" != "$latest" ]; then
  echo "::error::conformance vectors are pinned at $pinned but $latest is published."
  echo "Update with: tools/vendor-conformance.sh $latest"
  exit 1
fi

# The pin matches; now prove the vendored bytes were not edited locally.
fail=0
while read -r path sha; do
  [ -f "$VENDORED/$path" ] || { echo "::error::missing vendored file: $path"; fail=1; continue; }
  actual="$(shasum -a 256 "$VENDORED/$path" | cut -d' ' -f1)"
  if [ "$actual" != "$sha" ]; then
    echo "::error::$path has been modified locally (expected $sha, got $actual)"
    fail=1
  fi
done < <(jq -r '.files[] | "\(.path) \(.sha256)"' "$VENDORED/manifest.json")

[ "$fail" -eq 0 ] || exit 1
echo "conformance vectors: $pinned, $(jq -r '.totalCases' "$VENDORED/manifest.json") cases, unmodified ✓"
