#!/usr/bin/env bash
# deploy-pi.sh — Build, push, and deploy WealthFolio to the Raspberry Pi homelab.
#
# Usage:
#   ./scripts/deploy-pi.sh              # build + push + deploy
#   ./scripts/deploy-pi.sh --push-only  # skip the remote deploy step
#   ./scripts/deploy-pi.sh --deploy-only # skip build+push, just restart Pi

set -euo pipefail

# ── Configuration ────────────────────────────────────────────────────────────
IMAGE="${IMAGE:-hemanthhku/wealthfolio}"
PI_HOST="${PI_HOST:-192.168.0.100}"
PI_USER="${PI_USER:-hemanth}"
PI_KEY="${PI_KEY:-$HOME/.ssh/homelab_key}"
PI_DIR="${PI_DIR:-/homelab/wealthfolio}"
SSH="ssh -i ${PI_KEY} ${PI_USER}@${PI_HOST}"

# ── Flags ────────────────────────────────────────────────────────────────────
PUSH_ONLY=false
DEPLOY_ONLY=false
for arg in "$@"; do
  case "$arg" in
    --push-only)   PUSH_ONLY=true ;;
    --deploy-only) DEPLOY_ONLY=true ;;
    *) echo "Unknown flag: $arg"; exit 1 ;;
  esac
done

# ── Helpers ──────────────────────────────────────────────────────────────────
bold() { printf '\033[1m%s\033[0m\n' "$*"; }
step() { printf '\n\033[1;34m▶ %s\033[0m\n' "$*"; }
ok()   { printf '\033[1;32m✔ %s\033[0m\n' "$*"; }
err()  { printf '\033[1;31m✘ %s\033[0m\n' "$*" >&2; exit 1; }

bold "WealthFolio → Raspberry Pi Deploy"
bold "Image : ${IMAGE}"
bold "Pi    : ${PI_USER}@${PI_HOST}:${PI_DIR}"

# ── Step 1: Build & push ─────────────────────────────────────────────────────
if ! $DEPLOY_ONLY; then
  step "Building multi-platform image and pushing to registry"
  make docker-push IMAGE="${IMAGE}"
  ok "Image pushed: ${IMAGE}:latest"
fi

# ── Step 2: Deploy on Pi ─────────────────────────────────────────────────────
if ! $PUSH_ONLY; then
  step "Connecting to Pi (${PI_HOST})"

  # Verify SSH reachability before doing anything destructive.
  $SSH true 2>/dev/null || err "Cannot reach Pi at ${PI_HOST} — check VPN / network"

  step "Pulling latest image on Pi"
  $SSH "docker pull ${IMAGE}:latest"
  ok "Image pulled"

  step "Restarting container"
  $SSH "cd ${PI_DIR} && docker compose up -d --force-recreate"
  ok "Container restarted"

  step "Waiting for health check (30 s)"
  for i in $(seq 1 6); do
    sleep 5
    STATUS=$($SSH "docker inspect --format='{{.State.Status}}' wealthfolio-app" 2>/dev/null || echo "unknown")
    printf '  [%d/6] container status: %s\n' "$i" "$STATUS"
    if [ "$STATUS" = "running" ]; then
      ok "Container is running"
      break
    fi
    if [ "$i" -eq 6 ]; then
      err "Container did not reach 'running' state after 30 s — check: ssh pihub 'docker logs wealthfolio-app'"
    fi
  done

  step "Tail of container logs"
  $SSH "docker logs wealthfolio-app --tail 20 2>&1"
fi

printf '\n'
ok "Deploy complete → http://${PI_HOST}:3000"
