#!/usr/bin/env bash
# Vendor a tagged release of the conformance vectors into a port.
#
# Run from a port's root, naming the tag verify-pin.sh asked for:
#   tools/vendor-conformance.sh v2 testdata/conformance
#
# Everything is fetched from the tag's commit and checked against that commit's
# manifest before anything in the port is touched, and VERSION is written last,
# so a failed or interrupted run leaves the old pin in place. Needs `gh` and `jq`.
set -euo pipefail

TAG="${1:?usage: tools/vendor-conformance.sh <tag> [vendored-dir]}"
VENDORED="${2:-testdata/conformance}"
REPO="${CONFORMANCE_REPO:-primandproper/primitives-conformance}"

# refs/tags/ makes a branch name fail here instead of vendoring an unpinned tree,
# and fetching every file at one commit means a tag moved mid-run cannot mix trees.
# On an HTTP error gh prints the error body to stdout even with --jq, so the
# exit status and the shape of the answer are both checked.
commit="$(gh api "repos/$REPO/commits/refs/tags/$TAG" --jq '.sha' 2>/dev/null)" || commit=""
[[ "$commit" =~ ^[0-9a-f]{40}$ ]] || { echo "::error::$TAG is not a tag in $REPO"; exit 1; }

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT

fetch() {
  mkdir -p "$stage/$(dirname "$1")"
  gh api -H 'Accept: application/vnd.github.raw+json' "repos/$REPO/contents/$1?ref=$commit" < /dev/null > "$stage/$1" \
    || { echo "::error::could not fetch $1 at $TAG ($commit)"; exit 1; }
}

fetch VERSION
fetch manifest.json

# A tag whose files were not bumped would vendor a pin that verify-pin.sh then
# reports as behind forever.
version="$(tr -d '[:space:]' < "$stage/VERSION")"
[ "$version" = "$TAG" ] || { echo "::error::$TAG carries VERSION $version"; exit 1; }
manifest_version="$(jq -r '.version' "$stage/manifest.json")"
[ "$manifest_version" = "$TAG" ] || { echo "::error::$TAG carries a manifest for $manifest_version"; exit 1; }

listed=()
while read -r path sha; do
  # The manifest decides what gets written into the port, so it does not get to
  # write outside vectors/.
  [[ "$path" =~ ^vectors/[A-Za-z0-9._-]+\.json$ && "$path" != *..* ]] \
    || { echo "::error::manifest lists an unexpected path: $path"; exit 1; }
  fetch "$path"
  actual="$(shasum -a 256 "$stage/$path" | cut -d' ' -f1)"
  [ "$actual" = "$sha" ] || { echo "::error::$path does not match the $TAG manifest (expected $sha, got $actual)"; exit 1; }
  listed+=("$path")
done < <(jq -r '.files[] | "\(.path) \(.sha256)"' "$stage/manifest.json")
[ "${#listed[@]}" -gt 0 ] || { echo "::error::the $TAG manifest lists no files"; exit 1; }

mkdir -p "$VENDORED/vectors"
for path in "${listed[@]}"; do
  cp "$stage/$path" "$VENDORED/$path"
done
cp "$stage/manifest.json" "$VENDORED/manifest.json"

for existing in "$VENDORED"/vectors/*.json; do
  [ -e "$existing" ] || continue
  path="vectors/$(basename "$existing")"
  if ! printf '%s\n' "${listed[@]}" | grep -qxF "$path"; then
    echo "removing $path: $TAG no longer publishes it"
    rm "$existing"
  fi
done

cp "$stage/VERSION" "$VENDORED/VERSION"
echo "conformance vectors: vendored $TAG ($commit), $(jq -r '.totalCases' "$VENDORED/manifest.json") cases into $VENDORED"
