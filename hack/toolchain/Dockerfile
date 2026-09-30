# The Flutter toolchain every mobile-app Container uses: CI runner, agent sandbox
# and devcontainer all build on it, so "passes in CI" means "passes for the agent".
#
# The cirruslabs image brings the Android SDK and a JDK; Flutter itself is moved to
# a pinned release, and one throwaway debug and release build warms the Gradle and
# SDK caches so CI and agents don't download them on every run. The result is
# flattened into one layer without the emulator and the replaced Flutter, which
# roughly halves its size.
FROM ghcr.io/cirruslabs/flutter@sha256:46691e311715845de03a3ba4753a475476936805b29431b1f00f1816981033f8 AS build

ARG FLUTTER_VERSION=3.47.5

RUN cd "$FLUTTER_HOME" \
 && git fetch --depth 1 origin tag "$FLUTTER_VERSION" \
 && git checkout -q "$FLUTTER_VERSION" \
 && git gc -q --prune=all \
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

# make, jq and curl are what the Makefile and release workflow use; nodejs runs
# JavaScript actions and the agent CLI.
RUN apt-get update && apt-get install -y --no-install-recommends make jq curl ca-certificates nodejs npm \
 && rm -rf /var/lib/apt/lists/* \
 && rm -rf "$ANDROID_HOME/emulator" /root/.gradle/daemon /root/.android/cache /tmp/*

FROM scratch
COPY --from=build / /
ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/opt/android-sdk-linux/cmdline-tools/latest/bin:/opt/android-sdk-linux/platform-tools:/sdks/flutter/bin:/sdks/flutter/bin/cache/dart-sdk/bin \
    ANDROID_HOME=/opt/android-sdk-linux \
    ANDROID_SDK_ROOT=/opt/android-sdk-linux \
    FLUTTER_HOME=/sdks/flutter \
    FLUTTER_ROOT=/sdks/flutter \
    FLUTTER_VERSION=3.47.5 \
    LANG=en_US.UTF-8 \
    LC_ALL=en_US.UTF-8 \
    LANGUAGE=en_US:en
CMD ["/bin/bash"]
