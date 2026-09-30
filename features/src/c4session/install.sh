#!/bin/sh
# Installs c4session, the C4 Factory Session client, and c4session-start, which
# the feature's postStartCommand runs on every start. It never fails the build:
# without the client the devcontainer still works, it just isn't a Session.
set -u

BINARY_URL="${BINARYURL:-}"
VERSION="${VERSION:-latest}"
ACTOR="${ACTOR:-human}"
SUMMARY="${SUMMARY:-}"
MODULE="github.com/JairJosafath/c4factory/cmd/c4session"
DEST=/usr/local/bin/c4session

warn() { echo "c4session feature: $*" >&2; }

arch() {
    case "$(uname -m)" in
        x86_64 | amd64) echo amd64 ;;
        aarch64 | arm64) echo arm64 ;;
        *) uname -m ;;
    esac
}

download() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$1" -o "$2"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO "$2" "$1"
    else
        warn "neither curl nor wget is available"
        return 1
    fi
}

install_client() {
    if [ -n "$BINARY_URL" ]; then
        url=$(echo "$BINARY_URL" | sed "s/\${arch}/$(arch)/g")
        echo "c4session feature: downloading $url"
        download "$url" "$DEST.tmp" && chmod 0755 "$DEST.tmp" && mv "$DEST.tmp" "$DEST"
    elif command -v go >/dev/null 2>&1; then
        echo "c4session feature: go install $MODULE@$VERSION"
        GOBIN=/usr/local/bin go install "$MODULE@$VERSION"
    else
        warn "set the binaryUrl option or add Go to install the client"
        return 1
    fi
}

if ! install_client; then
    rm -f "$DEST.tmp"
    warn "c4session was not installed; this devcontainer won't show up as a Session"
fi

# The start script bakes in the options. Values are single-quoted for the shell.
quote() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }
cat > /usr/local/bin/c4session-start <<SCRIPT
#!/bin/sh
# Written by the c4session devcontainer feature; run as its postStartCommand.
if ! command -v c4session >/dev/null 2>&1; then
    echo "c4session is not installed; this devcontainer won't show up as a Session"
    exit 0
fi
exec c4session start --actor $(quote "$ACTOR") --summary $(quote "$SUMMARY")
SCRIPT
chmod 0755 /usr/local/bin/c4session-start
exit 0
