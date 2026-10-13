#!/bin/sh
# Installs cicoria from its releases on GitHub:
#
#   curl -fsSL https://henomis.github.io/cicoria/install.sh | sh
#
# CICORIA_VERSION picks a release, such as v0.0.4, instead of the latest.
# CICORIA_INSTALL_DIR is where the binary goes, instead of ~/.local/bin.

set -eu

repo="https://github.com/henomis/cicoria"

fail() {
	echo "cicoria: $*" >&2
	exit 1
}

# Everything runs from a function, so that a script cut short while it was
# downloaded does nothing.
main() {
	command -v curl >/dev/null || fail "curl is needed to download it"
	command -v tar >/dev/null || fail "tar is needed to unpack it"

	# The archives are named as "uname" names the system and the processor.
	os=$(uname -s)
	arch=$(uname -m)
	case "$arch" in
	amd64) arch=x86_64 ;;
	aarch64) arch=arm64 ;;
	esac
	case "${os}_${arch}" in
	Linux_x86_64 | Linux_arm64 | Darwin_x86_64 | Darwin_arm64) ;;
	*) fail "there is no release for $os on $arch: build it with 'go install github.com/henomis/cicoria@latest'" ;;
	esac

	# The page of the latest release leads to the one of its tag.
	version=${CICORIA_VERSION:-}
	if [ -z "$version" ]; then
		url=$(curl -fsSL -o /dev/null -w '%{url_effective}' "$repo/releases/latest") ||
			fail "could not find the latest release"
		version=${url##*/}
	fi
	case "$version" in
	v[0-9]*) ;;
	*) fail "'$version' is not a release: one is named like v0.0.4" ;;
	esac

	dir=${CICORIA_INSTALL_DIR:-$HOME/.local/bin}
	archive="cicoria_${os}_${arch}.tar.gz"
	sums="cicoria_${version#v}_checksums.txt"
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT

	echo "downloading cicoria $version for $os on $arch"
	curl -fsSL -o "$tmp/$archive" "$repo/releases/download/$version/$archive" ||
		fail "could not download $archive of $version"
	curl -fsSL -o "$tmp/$sums" "$repo/releases/download/$version/$sums" ||
		fail "could not download the checksums of $version"

	# What was downloaded must be what was released.
	want=$(awk -v name="$archive" '$2 == name { print $1 }' "$tmp/$sums")
	if command -v sha256sum >/dev/null; then
		got=$(sha256sum "$tmp/$archive" | awk '{ print $1 }')
	elif command -v shasum >/dev/null; then
		got=$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')
	else
		fail "sha256sum or shasum is needed to check the download"
	fi
	[ -n "$want" ] && [ "$want" = "$got" ] || fail "the checksum of $archive is wrong: nothing was installed"

	tar -xzf "$tmp/$archive" -C "$tmp" cicoria
	mkdir -p "$dir"
	# Moved into place in one step, so that a cicoria that is running keeps
	# the file it was started from.
	cp "$tmp/cicoria" "$dir/.cicoria.new"
	chmod 755 "$dir/.cicoria.new"
	mv "$dir/.cicoria.new" "$dir/cicoria"

	echo "installed $dir/cicoria"
	case ":$PATH:" in
	*":$dir:"*) ;;
	*) echo "$dir is not on your PATH: add it, or run $dir/cicoria" ;;
	esac
}

main
