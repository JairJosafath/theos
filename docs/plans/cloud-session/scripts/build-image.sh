#!/bin/bash
# build-image.sh NAME SRC_DOCKERFILE TAG [extra docker build args...]
# Builds an image from a copy of SRC_DOCKERFILE in which every stage trusts the
# session proxy's CA (ca-dockerfile.py), on the host network, through the proxy.
# Run from the theos checkout root (the build context).
set -euo pipefail
W=${THEOS_SESSION_DIR:?set THEOS_SESSION_DIR to the folder this session keeps its factory in}
name=$1 src=$2 tag=$3; shift 3
mkdir -p "$W/dockerfiles/$name"
python3 "$W/cs/ca-dockerfile.py" "$src" "$W/dockerfiles/$name/Dockerfile"
[ -f "$src.dockerignore" ] && cp "$src.dockerignore" "$W/dockerfiles/$name/Dockerfile.dockerignore"
docker build --network host \
  --build-arg HTTPS_PROXY="$HTTPS_PROXY" --build-arg https_proxy="$HTTPS_PROXY" \
  --build-arg NO_PROXY="$NO_PROXY" --build-arg no_proxy="$NO_PROXY" \
  --build-context ccr=/root/.ccr \
  -f "$W/dockerfiles/$name/Dockerfile" -t "$tag" "$@" .
