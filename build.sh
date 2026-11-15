#!/bin/sh
# Build Junction and optionally deploy it to the gateway.
#   ./build.sh           build web + linux binary into ./out/
#   ./build.sh deploy    build, then install it on your gateway through safe-update.sh
#                        (rolls back by itself if the new version isn't healthy).
# Deploy target, in a git-ignored .deploy.env next to this script (or the environment):
#   DEPLOY_SSH=root@192.168.1.5     the gateway, or the Proxmox host that runs it
#   DEPLOY_CT=102                   only when DEPLOY_SSH is a Proxmox host: the container id
set -e
cd "$(dirname "$0")"
GO="${GO:-go}"
command -v "$GO" >/dev/null 2>&1 || GO=go

echo "== web"
(cd web && { [ -d node_modules ] || npm ci; } && npm run build)

echo "== go"
mkdir -p out
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "$GO" build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o out/junctiond .
ls -la out/junctiond

if [ "$1" = "deploy" ]; then
  [ -f .deploy.env ] && . ./.deploy.env
  [ -n "${DEPLOY_SSH:-}" ] || { echo "set DEPLOY_SSH (and DEPLOY_CT for a Proxmox container) in .deploy.env" >&2; exit 1; }
  echo "== deploy"
  rm -rf out/jdeploy && mkdir -p out/jdeploy
  cp out/junctiond packaging/gateway/safe-update.sh out/jdeploy/
  # A local deploy/ folder (git-ignored in the public repo) can add an install.sh
  # that also updates service files; without it, safe-update.sh does the swap.
  [ -f deploy/install.sh ] && cp deploy/config.json deploy/junctiond.init deploy/logrotate deploy/install.sh out/jdeploy/
  tar -C out -czf out/jdeploy.tgz jdeploy
  run='cd /tmp && rm -rf jdeploy && tar xzf jdeploy.tgz && { if [ -f jdeploy/install.sh ]; then sh jdeploy/install.sh; else sh jdeploy/safe-update.sh /tmp/jdeploy; fi; }; rm -rf /tmp/jdeploy /tmp/jdeploy.tgz'
  scp -q out/jdeploy.tgz "$DEPLOY_SSH:/tmp/jdeploy.tgz"
  if [ -n "${DEPLOY_CT:-}" ]; then
    ssh "$DEPLOY_SSH" "pct push $DEPLOY_CT /tmp/jdeploy.tgz /tmp/jdeploy.tgz && rm /tmp/jdeploy.tgz && pct exec $DEPLOY_CT -- sh -c '$run'"
  else
    ssh "$DEPLOY_SSH" "sh -c '$run'"
  fi
fi
