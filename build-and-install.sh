#!/usr/bin/env bash
# Build the patched waybar-niri-windows module and install it.
#
# The source is upstream calico32/waybar-niri-windows plus local commits on top
# (see `git log`). Remotes: `origin` = jwu/waybar-niri-windows (your fork),
# `upstream` = calico32/waybar-niri-windows. To pick up a new upstream release:
#
#   git fetch upstream && git rebase upstream/main
#
# Usage: ./build-and-install.sh
set -euo pipefail

src_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
dest="${WAYBAR_DIR:-$HOME/.config/waybar}"
out="$dest/waybar-niri-windows.so"

cd "$src_dir"
go test ./niri/
go vet ./niri/
go build -trimpath -ldflags="-s -w" -buildmode=c-shared -o waybar-niri-windows.so ./main

mkdir -p "$dest"
if [ -f "$out" ]; then
	backup="$out.bak-$(date +%Y%m%d-%H%M%S)"
	cp -a "$out" "$backup"
	echo "backup:    $backup"
fi
install -m 644 waybar-niri-windows.so "$out"
echo "installed: $out"
echo
echo "waybar only loads cffi modules at startup, so restart it:"
echo "  pkill -x waybar; sleep 1; setsid waybar >\"\$HOME/.cache/waybar.log\" 2>&1 &"
