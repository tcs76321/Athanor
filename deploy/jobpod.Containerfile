# Athanor Job Pod base image (ARCHITECTURE §21.2, §32; ROADMAP M7-T7).
#
# Ephemeral, rootless, network=none at run time. This image provides the
# language toolchain the `code` archetype's execute_code/run_tests/lint
# sub-steps need. Build with `make jobpod-image`, then set job_pod.image.
FROM python:3.12-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends git build-essential \
 && rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir pytest

# Run as a non-root user (the pod also drops all caps and sets
# no-new-privileges; see jobpod hardening flags).
RUN useradd --create-home --uid 10001 job
USER job

WORKDIR /workspace
