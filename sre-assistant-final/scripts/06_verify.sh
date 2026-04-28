#!/usr/bin/env bash
set -euo pipefail
kubectl get nodes -o wide
kubectl get pods -A
kubectl get svc -n sre-assistant || true
kubectl get pods -n middleware
kubectl get pods -n monitoring
kubectl get pods -n ai-services
AGENT_POD=$(kubectl get pod -n sre-assistant -l app=sre-agent -o jsonpath='{.items[0].metadata.name}')
kubectl logs -n sre-assistant "$AGENT_POD" --tail=80
