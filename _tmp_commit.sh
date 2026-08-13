#!/bin/bash
set -euo pipefail
cd /mnt/c/Users/abdul/Desktop/AIVM/log-viewer
git add \
  README.md \
  cmd/log-viewer/main.go \
  go.mod \
  go.sum \
  internal/k8s/client.go \
  internal/k8s/workloads.go \
  internal/k8s/unhealthy.go \
  internal/k8s/unhealthy_test.go \
  internal/k8s/unhealthy_list_test.go \
  internal/viewer/app.js \
  internal/viewer/server.go \
  internal/viewer/viewer.html

git commit -m "Add Problems view for CrashLoop and failed pods.

Surface Admin-allowlisted unhealthy pods with a header alert, previous-container log streaming, and coverage for crash/error classification."

git push -u origin HEAD
git status -sb
git log -1 --oneline
