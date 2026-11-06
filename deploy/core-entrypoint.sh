#!/bin/sh
# Core Pod supervisor. The daemon exits 75 when a configuration change has
# been written and validated; relaunch it in place so applying a resource or
# model change never asks the operator to restart the container.
while true; do
  /usr/local/bin/athanor "$@"
  code=$?
  if [ "$code" -eq 75 ]; then
    echo "athanor: configuration applied — restarting Core"
    continue
  fi
  exit "$code"
done
