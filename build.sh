#!/bin/bash
# Builds "dist/iPhone Mirror.app" (universal: Apple silicon + Intel).
# Requires Go 1.25+ and Xcode or the Xcode Command Line Tools (cgo compiles
# the FairPlay and Cocoa code).
set -euo pipefail
cd "$(dirname "$0")"

APP="dist/iPhone Mirror.app"
VERSION="${VERSION:-1.0.0}"
ARCHS="${ARCHS:-arm64 amd64}"

export CGO_ENABLED=1
export MACOSX_DEPLOYMENT_TARGET=14.0
# Use the SDK that matches the selected toolchain (a newer Command Line Tools
# SDK can otherwise be picked up and fail to link).
export SDKROOT="${SDKROOT:-$(xcrun --sdk macosx --show-sdk-path)}"
export CGO_CFLAGS="-O2 -mmacosx-version-min=$MACOSX_DEPLOYMENT_TARGET"
export CGO_LDFLAGS="-mmacosx-version-min=$MACOSX_DEPLOYMENT_TARGET"

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" dist/bin

bins=()
for arch in $ARCHS; do
	GOARCH=$arch go build -trimpath -ldflags "-s -w" -o "dist/bin/iPhoneMirror-$arch" .
	bins+=("dist/bin/iPhoneMirror-$arch")
done
lipo -create -output "$APP/Contents/MacOS/iPhoneMirror" "${bins[@]}"

sed "s/__VERSION__/$VERSION/g" res/Info.plist > "$APP/Contents/Info.plist"
if [ -f res/AppIcon.icns ]; then
	cp res/AppIcon.icns "$APP/Contents/Resources/"
fi

# Ad-hoc signature: enough to run locally and keep a stable identity for the
# Local Network and firewall permissions.
codesign --force --sign - "$APP"
echo "Built $APP ($(lipo -archs "$APP/Contents/MacOS/iPhoneMirror"))"
