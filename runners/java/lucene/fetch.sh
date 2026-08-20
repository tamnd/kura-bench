#!/bin/sh
# Downloads the pinned Lucene jars.
#
# The text suite builds without them. This is what a machine runs once before
# it can build the Lucene runner, and it is separate from the build so that a
# benchmark run never reaches the network.
#
# Everything it needs is in lucene.json next to it: the release, the repository
# and the sha256 of every jar. A download whose checksum does not match is
# deleted rather than used, because an engine that might not be the engine we
# pinned makes every number in the table worthless.
set -eu

dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
manifest="$dir/lucene.json"

field() {
	# One field out of the manifest without a JSON parser on the box. The
	# manifest is written by hand and stays one key per line, which is what
	# makes this safe.
	sed -n "s/^[[:space:]]*\"$1\": \"\([^\"]*\)\".*/\1/p" "$manifest" | head -1
}

version=$(field version)
repository=$(field repository)
group=$(field group)
if [ -z "$version" ] || [ -z "$repository" ] || [ -z "$group" ]; then
	echo "$manifest: no version, repository or group in it" >&2
	exit 1
fi

# The group is a Maven coordinate and the path is the same string with the dots
# turned into slashes.
grouppath=$(printf '%s\n' "$group" | tr '.' '/')
out="$dir/$version"
mkdir -p "$out"

# The artifact and the checksum come off adjacent lines of the same block, so
# they cannot be read off different jars.
artifacts=$(sed -n 's/^[[:space:]]*"artifact": "\([^"]*\)".*/\1/p' "$manifest")
sums=$(sed -n 's/^[[:space:]]*"sha256": "\([^"]*\)".*/\1/p' "$manifest")

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

i=0
for artifact in $artifacts; do
	i=$((i + 1))
	want=$(printf '%s\n' "$sums" | sed -n "${i}p")
	jar="$artifact-$version.jar"
	if [ -f "$out/$jar" ] && [ "$(sha256 "$out/$jar")" = "$want" ]; then
		echo "$jar is already here and matches"
		continue
	fi

	url="$repository/$grouppath/$artifact/$version/$jar"
	echo "fetching $url"
	curl -fsSL -o "$out/$jar.part" "$url"

	got=$(sha256 "$out/$jar.part")
	if [ "$got" != "$want" ]; then
		rm -f "$out/$jar.part"
		echo "$jar has checksum $got, the manifest says $want" >&2
		exit 1
	fi
	mv "$out/$jar.part" "$out/$jar"
done

echo "lucene $version is in $out"
