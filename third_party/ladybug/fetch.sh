#!/bin/sh
# Downloads the pinned ladybug shared library for this machine.
#
# The graph suite builds without it. This script is what a machine runs once
# before it can build the ladybug runner, and it is separate from the build so
# that a benchmark run never reaches the network.
#
# Everything it needs is in ladybug.json next to it: the release tag, the
# archive for each platform, and the sha256 of each archive. A download whose
# checksum does not match is deleted rather than used, because an engine that
# might not be the engine we pinned makes every number in the table worthless.
set -eu

dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
manifest="$dir/ladybug.json"

# The platform key is the Go one, so that the Makefile and the cgo build agree
# on the directory without either of them having to translate.
goos=$(go env GOOS)
goarch=$(go env GOARCH)
platform="$goos-$goarch"

field() {
	# One field out of the manifest without a JSON parser on the box. The
	# manifest is written by hand and stays one key per line, which is what
	# makes this safe.
	sed -n "s/^[[:space:]]*\"$1\": \"\([^\"]*\)\".*/\1/p" "$2" | head -1
}

version=$(field version "$manifest")
repo=$(field repo "$manifest")
if [ -z "$version" ] || [ -z "$repo" ]; then
	echo "$manifest: no repo or version in it" >&2
	exit 1
fi

# The asset block for this platform, so that the file name and the checksum
# come from the same two lines and cannot be read off different platforms.
block=$(sed -n "/\"$platform\": {/,/}/p" "$manifest")
if [ -z "$block" ]; then
	echo "ladybug does not publish a library for $platform" >&2
	echo "the graph suite runs without it, so this is not fatal" >&2
	exit 2
fi
file=$(printf '%s\n' "$block" | sed -n 's/^[[:space:]]*"file": "\([^"]*\)".*/\1/p')
want=$(printf '%s\n' "$block" | sed -n 's/^[[:space:]]*"sha256": "\([^"]*\)".*/\1/p')

out="$dir/$version/$platform"
if [ -f "$out/lbug.h" ]; then
	echo "ladybug $version is already in $out"
	exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

url="https://github.com/$repo/releases/download/$version/$file"
echo "fetching $url"
curl -fsSL -o "$tmp/$file" "$url"

got=$(sha256 "$tmp/$file" 2>/dev/null || shasum -a 256 "$tmp/$file" 2>/dev/null || sha256sum "$tmp/$file")
got=$(printf '%s\n' "$got" | tr ' ' '\n' | grep -E '^[0-9a-f]{64}$' | head -1)
if [ "$got" != "$want" ]; then
	echo "$file: sha256 is $got, the manifest says $want" >&2
	exit 1
fi

mkdir -p "$out"
case "$file" in
*.zip) unzip -q -o "$tmp/$file" -d "$out" ;;
*) tar xzf "$tmp/$file" -C "$out" ;;
esac

if [ ! -f "$out/lbug.h" ]; then
	echo "$file: no lbug.h in it" >&2
	exit 1
fi
echo "ladybug $version is in $out"
