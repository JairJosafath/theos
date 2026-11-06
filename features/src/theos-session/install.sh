#!/bin/sh
# Installs theos-session, the theos Session client, and theos-session-start, which
# the feature's postStartCommand runs on every start. It never fails the build:
# without the client the devcontainer still works, it just isn't a Session.
# A downloaded binary is checked against the SHA256SUMS served next to it, and
# refused on a mismatch or without one (#1175). That SHA256SUMS comes from the
# same origin as the binary, so it guards the transport only; the release signs
# it, keyless, with cosign (SHA256SUMS.sigstore.json, the signature and its
# certificate), and when cosign is on PATH a SHA256SUMS whose signature doesn't
# verify as the signer's is refused too (#1706). Without cosign, or where the
# origin serves no signature (the UI's /downloads/), a notice says the
# signature was not checked.
set -u

BINARY_URL="${BINARYURL:-}"
VERSION="${VERSION:-latest}"
ACTOR="${ACTOR:-human}"
SUMMARY="${SUMMARY:-}"
# Who signs SHA256SUMS: the certificate's identity, a regular expression, and
# its OIDC issuer. The defaults are this repository's Release workflow on a
# version tag, through GitHub Actions; a fork that releases its own sets both.
SIGNER_IDENTITY="${SIGNERIDENTITY:-^https://github\.com/JairJosafath/theos/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$}"
SIGNER_ISSUER="${SIGNERISSUER:-https://token.actions.githubusercontent.com}"
MODULE="github.com/JairJosafath/theos/cmd/theos-session"
# Where the client and the start script go; a test points it elsewhere.
BIN_DIR="${THEOS_SESSION_BIN_DIR:-/usr/local/bin}"
DEST=$BIN_DIR/theos-session

warn() { echo "theos-session feature: $*" >&2; }

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

sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | cut -d' ' -f1
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | cut -d' ' -f1
    else
        warn "neither sha256sum nor shasum is available to verify the download"
        return 1
    fi
}

# verify FILE NAME SUMS: FILE's SHA-256 is the one SUMS lists for NAME, in
# sha256sum's format (the release's and the UI image's SHA256SUMS).
verify() {
    want=$(awk -v name="$2" '$2 == name { print $1; exit }' "$3")
    if [ -z "$want" ]; then
        warn "SHA256SUMS doesn't list $2; refusing the download"
        return 1
    fi
    got=$(sha256 "$1") || return 1
    if [ "$got" != "$want" ]; then
        warn "checksum mismatch for $2: SHA256SUMS says $want, the download is $got; refusing it"
        return 1
    fi
    echo "theos-session feature: $2 matches SHA256SUMS"
}

# verify_signature SUMS URL: when cosign is on PATH and the origin serves
# SHA256SUMS.sigstore.json next to SUMS's URL, SUMS's signature verifies as
# SIGNER_IDENTITY's from SIGNER_ISSUER, or the download is refused. Without
# cosign or a signature it says so and goes on: the checksum still holds.
verify_signature() {
    if ! command -v cosign >/dev/null 2>&1; then
        echo "theos-session feature: cosign is not on PATH; SHA256SUMS's signature was not checked"
        return 0
    fi
    if ! download "$2.sigstore.json" "$DEST.bundle" 2>/dev/null; then
        echo "theos-session feature: no SHA256SUMS.sigstore.json next to SHA256SUMS; its signature was not checked"
        return 0
    fi
    if ! cosign verify-blob --bundle "$DEST.bundle" \
        --certificate-identity-regexp "$SIGNER_IDENTITY" \
        --certificate-oidc-issuer "$SIGNER_ISSUER" "$1" >"$DEST.cosign" 2>&1; then
        sed 's/^/  cosign: /' "$DEST.cosign" >&2
        warn "SHA256SUMS's signature doesn't verify as $SIGNER_IDENTITY's; refusing the download"
        return 1
    fi
    echo "theos-session feature: SHA256SUMS's signature verifies as the release's"
}

install_client() {
    if [ -n "$BINARY_URL" ]; then
        url=$(echo "$BINARY_URL" | sed "s/\${arch}/$(arch)/g")
        name=${url##*/}
        sums=${url%/*}/SHA256SUMS
        echo "theos-session feature: downloading $url"
        download "$url" "$DEST.tmp" || return 1
        echo "theos-session feature: verifying against $sums"
        if ! download "$sums" "$DEST.sums"; then
            warn "no SHA256SUMS next to the binary; refusing the download"
            return 1
        fi
        verify_signature "$DEST.sums" "$sums" || return 1
        verify "$DEST.tmp" "$name" "$DEST.sums" || return 1
        chmod 0755 "$DEST.tmp" && mv "$DEST.tmp" "$DEST"
    elif command -v go >/dev/null 2>&1; then
        echo "theos-session feature: go install $MODULE@$VERSION"
        GOBIN=$BIN_DIR go install "$MODULE@$VERSION"
    else
        warn "set the binaryUrl option or add Go to install the client"
        return 1
    fi
}

if ! install_client; then
    rm -f "$DEST.tmp"
    warn "theos-session was not installed; this devcontainer won't show up as a Session"
fi
rm -f "$DEST.sums" "$DEST.bundle" "$DEST.cosign"

# The start script bakes in the options. Values are single-quoted for the shell.
quote() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }
cat > "$BIN_DIR/theos-session-start" <<SCRIPT
#!/bin/sh
# Written by the theos-session devcontainer feature; run as its postStartCommand.
if ! command -v theos-session >/dev/null 2>&1; then
    echo "theos-session is not installed; this devcontainer won't show up as a Session"
    exit 0
fi
exec theos-session start --actor $(quote "$ACTOR") --summary $(quote "$SUMMARY")
SCRIPT
chmod 0755 "$BIN_DIR/theos-session-start"
exit 0
