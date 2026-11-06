# syntax=docker/dockerfile:1
# The android test environment (docs/plans/test-environments.md, step 5): the
# Android SDK's command-line, platform and build tools, the emulator with an
# Android 16 (API 36) Google APIs system image for x86_64 and an AVD of it,
# theos, and JDK 17, so a run builds an app with its Gradle wrapper and runs
# its instrumented tests or takes its pictures on the emulator. The emulator
# needs KVM: the TestEnvironment asks the cluster's KVM device plugin for
# /dev/kvm on an x86_64 node (config/samples/webshop/testenvironment-android.yaml),
# and android-emulator start boots it once per lease (emulator.sh). The
# operator runs theos-env serve in it, copied in from the theos-agent image by
# the lease pod's init container, so this image holds nothing of the
# factory's; a CI pool's pods run their jobs on it with its bash and git.
# Published as theos/env-android with each release.
# Build: docker build -t theos/env-android:dev hack/env-android
FROM ubuntu:noble-20261024.1@sha256:fe81560990fae86dc4743202b2cb4d3df98d0bcf949173cecef15ce1127ef852

# The command-line tools 23.0. Google's repository (repository2-3.xml) gives
# the zip's SHA-1, e025545c62a8e64c7559119566a569fb1dec5f60, which the file
# of this SHA-256 has.
ARG CMDLINE_TOOLS=16111833
ARG CMDLINE_TOOLS_SHA256=0877a1d048fe4a24efe2eff536ca4223f7adeb58648bb81909d33c446918cfa8
# What sdkmanager installs, each checked against the SHA-1 Google's
# repository lists. platform-tools and the emulator have no version to name:
# they are the repository's current ones when the image is built.
ARG ANDROID_API=36
ARG BUILD_TOOLS=36.1.0
ARG SYSTEM_IMAGE=system-images;android-36;google_apis;x86_64

ENV ANDROID_HOME=/opt/android-sdk \
    ANDROID_SDK_ROOT=/opt/android-sdk \
    ANDROID_USER_HOME=/opt/android-user \
    JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64
ENV PATH=$ANDROID_HOME/cmdline-tools/latest/bin:$ANDROID_HOME/platform-tools:$ANDROID_HOME/emulator:$PATH

# JDK 17, which the Android Gradle plugin runs on; bash and git for a CI
# pool's jobs; the libraries the emulator loads, a window or none.
RUN apt-get -o Acquire::Retries=5 update \
    && DEBIAN_FRONTEND=noninteractive apt-get -o Acquire::Retries=5 install -y --no-install-recommends \
       bash ca-certificates curl git unzip openjdk-17-jdk-headless \
       libasound2t64 libdbus-1-3 libfontconfig1 libgl1 libnss3 libpulse0 libx11-xcb1 \
       libxcomposite1 libxcursor1 libxdamage1 libxi6 libxkbfile1 libxtst6 \
    && rm -rf /var/lib/apt/lists/*

RUN retry() { for i in 1 2 3 4 5; do "$@" && return; [ "$i" = 5 ] || sleep $((i * 10)); done; return 1; } \
    && curl -fsSL --retry 5 --retry-all-errors -o /tmp/tools.zip \
       "https://dl.google.com/android/repository/commandlinetools-linux-${CMDLINE_TOOLS}_latest.zip" \
    && echo "${CMDLINE_TOOLS_SHA256}  /tmp/tools.zip" | sha256sum -c - \
    && mkdir -p "$ANDROID_HOME/cmdline-tools" \
    && unzip -q /tmp/tools.zip -d /tmp/tools \
    && mv /tmp/tools/cmdline-tools "$ANDROID_HOME/cmdline-tools/latest" \
    && rm -rf /tmp/tools /tmp/tools.zip \
    && yes | retry sdkmanager --licenses >/dev/null \
    && retry sdkmanager --install platform-tools emulator "platforms;android-${ANDROID_API}" \
       "build-tools;${BUILD_TOOLS}" "${SYSTEM_IMAGE}" \
    && echo no | avdmanager create avd --name theos --package "${SYSTEM_IMAGE}" --device pixel_7 \
    && rm -rf "$ANDROID_USER_HOME/cache" /tmp/*

COPY --chmod=755 emulator.sh /usr/local/bin/android-emulator
WORKDIR /work
