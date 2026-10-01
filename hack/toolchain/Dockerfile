# The Flutter toolchain every mobile-app Container uses: CI runner, agent sandbox
# and devcontainer all build on it, so "passes in CI" means "passes for the agent".
#
# The cirruslabs image brings the Android SDK; Flutter itself is moved to a pinned
# release, and one throwaway debug build warms the Gradle and SDK caches so CI and
# agents don't download them on every run.
#
# It is not flattened into one layer: exporting a ~11 GB layer overheated and
# restarted the development machine twice. It stays large (about 20 GB); the
# layers below the last are the ones that already built.
FROM ghcr.io/cirruslabs/flutter@sha256:46691e311715845de03a3ba4753a475476936805b29431b1f00f1816981033f8

ARG FLUTTER_VERSION=3.47.5

RUN cd "$FLUTTER_HOME" \
 && git fetch --depth 1 origin tag "$FLUTTER_VERSION" \
 && git checkout -q "$FLUTTER_VERSION" \
 && flutter --version \
 && flutter config --no-analytics --no-cli-animations \
 && dart --disable-analytics \
 && flutter precache --android

RUN yes | sdkmanager --licenses >/dev/null \
 && cd /tmp \
 && flutter create --org io.factory.warmup --platforms android warmup \
 && cd warmup \
 && flutter pub add --dev very_good_analysis \
 && flutter build apk --debug \
 && flutter build apk --release \
 && cd / && rm -rf /tmp/warmup

# make, jq and curl are what the Makefile and release workflow use.
RUN apt-get update && apt-get install -y --no-install-recommends make jq curl ca-certificates \
 && rm -rf /var/lib/apt/lists/*

# The agent CLI runs on Node.
RUN apt-get update && apt-get install -y --no-install-recommends nodejs npm \
 && rm -rf /var/lib/apt/lists/*
