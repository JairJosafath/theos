# syntax=docker/dockerfile:1
# The godot test environment (docs/plans/test-environments.md, step 5): the
# Godot 4.7 engine for Linux, with the libraries its renderers load and a
# virtual display, so a run tests a game and takes its pictures. Tests run
# headless, with no GPU (godot --headless --path . ...); a picture or a movie
# needs a renderer and a window: xvfb-run -a godot ... renders with the GPU
# driver the node's runtime class mounts (an NVIDIA GPU's, which
# NVIDIA_DRIVER_CAPABILITIES asks for), else with Mesa's software renderers
# (config/samples/webshop/testenvironment-godot.yaml). The operator runs
# theos-env serve in it, copied in from the theos-agent image by the lease
# pod's init container, so this image holds nothing of the factory's; a CI
# pool's pods run their jobs on it with its bash and git. No export
# templates: a project that exports installs them over public HTTPS, which
# the environment's fence allows. Published as theos/env-godot with each
# release.
# Build: docker build -t theos/env-godot:dev hack/env-godot
FROM ubuntu:noble-20261024.1@sha256:fe81560990fae86dc4743202b2cb4d3df98d0bcf949173cecef15ce1127ef852

ARG GODOT_VERSION=4.7.2
# The SHA-256 of the zip godotengine.org's download page served for the
# version.
ARG GODOT_SHA256=cadd3204e728a35d3f13adb7fd0d7902636b79f6b95c40c265eb73b6c35329e4

# bash and git for a CI pool's jobs; Xvfb; Vulkan's loader and OpenGL, with
# Mesa's drivers for a node without a GPU; and what Godot loads for fonts,
# input, sound and the window.
RUN apt-get -o Acquire::Retries=5 update \
    && DEBIAN_FRONTEND=noninteractive apt-get -o Acquire::Retries=5 install -y --no-install-recommends \
       bash ca-certificates curl git unzip xauth xvfb \
       libegl1 libgl1 libgl1-mesa-dri libvulkan1 mesa-vulkan-drivers \
       libasound2t64 libdbus-1-3 libfontconfig1 libpulse0 libudev1 libxcursor1 libxi6 libxinerama1 \
       libxkbcommon0 libxrandr2 \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL --retry 5 --retry-all-errors -o /tmp/godot.zip \
       "https://downloads.godotengine.org/?version=${GODOT_VERSION}&flavor=stable&slug=linux.x86_64.zip&platform=linux.64" \
    && echo "${GODOT_SHA256}  /tmp/godot.zip" | sha256sum -c - \
    && unzip -q /tmp/godot.zip -d /tmp/godot \
    && mv "/tmp/godot/Godot_v${GODOT_VERSION}-stable_linux.x86_64" /usr/local/bin/godot \
    && rm -rf /tmp/godot /tmp/godot.zip

# The NVIDIA container runtime mounts the driver's graphics libraries, Vulkan's
# among them, only into a container that asks for them.
ENV NVIDIA_DRIVER_CAPABILITIES=graphics,compute,utility
WORKDIR /work
