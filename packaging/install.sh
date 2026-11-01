#!/usr/bin/env bash
# Installs the Superstrike Control app for the current user: binary on PATH,
# icon, and desktop launcher (so it shows up in your app menu with the icon).
set -euo pipefail

cd "$(dirname "$0")/.."

if ! command -v npm >/dev/null 2>&1; then
	echo "error: npm is required to build the interface (install Node.js 22 or newer)." >&2
	exit 1
fi

# Use the system Go toolchain when available. For desktop users who cloned the
# project without Go installed, bootstrap the exact version declared in go.mod
# into the repository. This does not modify /usr, $HOME, or the shell profile.
GO_BIN="$(command -v go || true)"
if [ -z "$GO_BIN" ]; then
	GO_VERSION="$(awk '$1 == "go" { print $2; exit }' go.mod)"
	case "$(uname -m)" in
		x86_64) GO_ARCH="amd64" ;;
		aarch64|arm64) GO_ARCH="arm64" ;;
		*) echo "error: unsupported CPU architecture: $(uname -m)" >&2; exit 1 ;;
	esac
	GO_BIN="$PWD/.tools/go/bin/go"
	if [ ! -x "$GO_BIN" ]; then
		command -v curl >/dev/null 2>&1 || {
			echo "error: curl is required to download the Go toolchain." >&2
			exit 1
		}
		echo "Go is not installed; downloading Go $GO_VERSION for $GO_ARCH…"
		mkdir -p .tools
		TOOLCHAIN_ARCHIVE="$(mktemp -p /tmp superstrike-go.XXXXXX.tar.gz)"
		trap 'rm -f "$TOOLCHAIN_ARCHIVE"' EXIT
		curl --fail --location --progress-bar \
			"https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" \
			--output "$TOOLCHAIN_ARCHIVE"
		tar -C .tools -xzf "$TOOLCHAIN_ARCHIVE"
	fi
fi

echo "building…"
npm --prefix frontend ci --include=dev
npm --prefix frontend run build
"$GO_BIN" build -tags "desktop,production,webkit2_41" -o superstrike .

BIN="$HOME/.local/bin"
ICONS="$HOME/.local/share/icons/hicolor/512x512/apps"
APPS="$HOME/.local/share/applications"
mkdir -p "$BIN" "$ICONS" "$APPS"

install -m755 superstrike "$BIN/superstrike"
base64 --decode packaging/superstrike-icon.b64 > "$ICONS/superstrike.png"
chmod 644 "$ICONS/superstrike.png"
install -m644 packaging/superstrike.desktop "$APPS/superstrike.desktop"
# Desktop launchers do not consistently inherit the user's shell PATH. Point
# Exec at the installed binary directly so launching works on every desktop.
sed -i "s|^Exec=.*|Exec=$BIN/superstrike|" "$APPS/superstrike.desktop"
sed -i "s|^Icon=.*|Icon=$ICONS/superstrike.png|" "$APPS/superstrike.desktop"

# refresh caches if the tools exist
command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$APPS" || true
command -v gtk-update-icon-cache  >/dev/null 2>&1 && gtk-update-icon-cache -f "$HOME/.local/share/icons/hicolor" 2>/dev/null || true

# udev rule: grants the logged-in user access to the mouse without root. MUST be
# numbered below 73 so systemd's 73-seat-late.rules turns the uaccess tag into a
# per-user ACL. (A 99-* rule sets the tag too late — the device stays root-only.)
RULE="packaging/70-logitech-superstrike.rules"
DEST="/etc/udev/rules.d/70-logitech-superstrike.rules"
if [ ! -f "$DEST" ] || ! cmp -s "$RULE" "$DEST"; then
	echo "installing udev rule (needs sudo)…"
	sudo rm -f /etc/udev/rules.d/99-logitech-superstrike.rules   # drop the old, too-late rule
	sudo install -m644 "$RULE" "$DEST"
	sudo udevadm control --reload-rules
	sudo udevadm trigger
	echo "  udev rule installed; replug the mouse if it isn't detected."
else
	echo "udev rule already current."
fi

echo "installed:"
echo "  binary : $BIN/superstrike"
echo "  icon   : $ICONS/superstrike.png"
echo "  launcher: $APPS/superstrike.desktop"
echo "Look for 'Superstrike Control' in your application menu."
