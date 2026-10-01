# The slim Flutter toolchain: Flutter and Dart without the Android SDK, enough for
# `make check` (format, analyze, test). The CI runner and the agents use it; only
# release builds need the full image in hack/toolchain. About a tenth of its size,
# so it builds and loads into Kind without straining a laptop.
FROM debian:bookworm-slim

ARG FLUTTER_VERSION=3.47.5

RUN apt-get update && apt-get install -y --no-install-recommends \
      bash ca-certificates curl git jq make unzip xz-utils zip \
      libglu1-mesa nodejs npm \
 && rm -rf /var/lib/apt/lists/*

ENV FLUTTER_HOME=/sdks/flutter \
    FLUTTER_ROOT=/sdks/flutter \
    PUB_CACHE=/root/.pub-cache \
    PATH=/sdks/flutter/bin:/sdks/flutter/bin/cache/dart-sdk/bin:/root/.pub-cache/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

RUN git clone --depth 1 --branch "$FLUTTER_VERSION" https://github.com/flutter/flutter.git "$FLUTTER_HOME" \
 && flutter config --no-analytics --no-cli-animations --no-enable-web \
      --no-enable-linux-desktop --no-enable-ios \
 && dart --disable-analytics \
 && flutter precache --universal \
 && flutter --version

# Warm the pub cache with what the templates use, so checks start quickly.
RUN cd /tmp \
 && flutter create --org io.factory.warmup --platforms android warmup >/dev/null \
 && cd warmup \
 && flutter pub add --dev very_good_analysis \
 && flutter analyze \
 && flutter test \
 && cd / && rm -rf /tmp/warmup
