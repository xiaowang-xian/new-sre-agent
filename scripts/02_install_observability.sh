#!/usr/bin/env bash
set -euo pipefail
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts || true
helm repo update
helm upgrade --install prometheus-stack prometheus-community/kube-prometheus-stack \
  -n monitoring --create-namespace \
  --set grafana.service.type=NodePort \
  --set prometheus.service.type=NodePort \
  --set alertmanager.service.type=NodePort
kubectl rollout status -n monitoring deploy/prometheus-stack-grafana --timeout=300s
kubectl get svc -n monitoring
