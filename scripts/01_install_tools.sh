#!/usr/bin/env bash
set -euo pipefail
if command -v apt >/dev/null 2>&1; then
  sudo apt update
  sudo apt install -y git curl wget jq docker.io python3-venv
else
  sudo dnf install -y git curl wget jq docker python3 python3-venv
  sudo systemctl enable --now docker || true
fi
if ! command -v helm >/dev/null 2>&1; then
  curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
fi
helm version
kubectl version --client=true
