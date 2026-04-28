#!/usr/bin/env bash
set -euo pipefail
: "${REGISTRY:?请先 export REGISTRY=你的镜像仓库地址}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
docker build -t "$REGISTRY/sre-agent:latest" "$ROOT/agent"
docker build -t "$REGISTRY/sre-rag-service:latest" "$ROOT/rag-service"
docker build -t "$REGISTRY/sre-backend:latest" "$ROOT/backend"
docker build -t "$REGISTRY/sre-frontend:latest" "$ROOT/frontend"
docker push "$REGISTRY/sre-agent:latest"
docker push "$REGISTRY/sre-rag-service:latest"
docker push "$REGISTRY/sre-backend:latest"
docker push "$REGISTRY/sre-frontend:latest"
