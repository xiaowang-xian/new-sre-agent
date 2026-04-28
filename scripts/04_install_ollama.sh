#!/usr/bin/env bash
set -euo pipefail
kubectl create namespace ai-services --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ollama
  namespace: ai-services
spec:
  replicas: 1
  selector: { matchLabels: { app: ollama } }
  template:
    metadata: { labels: { app: ollama } }
    spec:
      containers:
        - name: ollama
          image: ollama/ollama:latest
          ports: [{ containerPort: 11434 }]
          resources:
            requests: { cpu: "1", memory: "2Gi" }
            limits: { cpu: "4", memory: "8Gi" }
---
apiVersion: v1
kind: Service
metadata:
  name: ollama-service
  namespace: ai-services
spec:
  selector: { app: ollama }
  ports: [{ name: http, port: 11434, targetPort: 11434 }]
EOF
kubectl rollout status -n ai-services deploy/ollama --timeout=300s
kubectl exec -n ai-services deploy/ollama -- ollama pull qwen2:7b-instruct
