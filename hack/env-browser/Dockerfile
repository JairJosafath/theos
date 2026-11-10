# syntax=docker/dockerfile:1
# The browser test environment (docs/plans/done/test-environments.md, T7 and T14):
# Playwright's image, with Chromium, Firefox and WebKit and their system
# libraries, and a virtual display, so a test or picture script that drives a
# browser, VS Code or another Electron app runs: headless as it is, headed
# under `xvfb-run -a`. The operator runs theos-env serve in it, copied in
# from the theos-agent image by the lease pod's init container, so this image
# holds nothing of the factory's. Published as theos/env-browser with each
# release; the chart's browser TestEnvironment names it.
# Build: docker build -t theos/env-browser:dev hack/env-browser
FROM mcr.microsoft.com/playwright:v1.65.0-noble@sha256:6e3fb709361521eb27161606e6da6d2674e5e89fa16516cb37d6fdd91372273d

# xvfb-run, and the xauth it needs, for headed runs.
RUN apt-get -o Acquire::Retries=5 update \
    && apt-get -o Acquire::Retries=5 install -y --no-install-recommends xauth xvfb \
    && rm -rf /var/lib/apt/lists/*

# Playwright finds the image's browsers here; a project on another
# Playwright version installs its own with npx playwright install, over
# public HTTPS, which the environment's fence allows.
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright
WORKDIR /work
