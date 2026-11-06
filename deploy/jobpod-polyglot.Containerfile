# Polyglot Job Pod base image (optional; ADR-0101).
#
# The default athanor-jobpod image is Python-only by design. This image adds the
# common language toolchains so a repository whose detected test command is
# `go test`, `cargo test`, `npm test`, `mvn test`, `gradle test`, `composer
# test`, or `bundle exec rspec` can actually run inside the pod.
#
# Build with:
#   make jobpod-polyglot-image
# then set `job_pod.image: athanor-jobpod-polyglot:latest` in config.yaml.
#
# Caveat: the Job Pod mounts /tmp with noexec. Python tests work because the
# interpreter reads source; compiled test runners that execute a freshly built
# binary from /tmp need an exec-capable scratch mount. Use a Python-only
# repository or extend the pod spec if you need compiled tests; this image does
# not silently weaken the default hardening.
FROM python:3.12-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      git build-essential ca-certificates \
      golang-go nodejs npm cargo \
      openjdk-17-jdk-headless maven gradle \
      ruby-full php-cli composer \
 && rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir pytest ruff

RUN useradd --create-home --uid 10001 job
USER job

WORKDIR /workspace
