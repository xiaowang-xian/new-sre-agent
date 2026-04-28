#!/usr/bin/env bash
set -euo pipefail
helm repo add bitnami https://charts.bitnami.com/bitnami || true
helm repo update
kubectl create namespace middleware --dry-run=client -o yaml | kubectl apply -f -
helm upgrade --install redis bitnami/redis -n middleware \
  --set auth.enabled=false \
  --set architecture=standalone \
  --set master.resources.requests.cpu=100m \
  --set master.resources.requests.memory=128Mi \
  --set master.resources.limits.memory=512Mi
helm upgrade --install rabbitmq bitnami/rabbitmq -n middleware \
  --set auth.username=user \
  --set auth.password=password \
  --set service.type=ClusterIP \
  --set resources.requests.cpu=100m \
  --set resources.requests.memory=256Mi \
  --set resources.limits.memory=768Mi
helm upgrade --install mysql bitnami/mysql -n middleware \
  --set auth.rootPassword=rootpass \
  --set auth.database=sre_assistant \
  --set auth.username=sre \
  --set auth.password=srepass \
  --set primary.resources.requests.cpu=100m \
  --set primary.resources.requests.memory=256Mi \
  --set primary.resources.limits.memory=1Gi
helm upgrade --install elasticsearch bitnami/elasticsearch -n middleware \
  --set master.replicaCount=1 \
  --set data.replicaCount=1 \
  --set coordinating.replicaCount=0 \
  --set ingest.replicaCount=0 \
  --set security.enabled=false \
  --set master.resources.requests.cpu=200m \
  --set master.resources.requests.memory=512Mi \
  --set master.resources.limits.memory=1Gi \
  --set data.resources.requests.cpu=200m \
  --set data.resources.requests.memory=512Mi \
  --set data.resources.limits.memory=1Gi
kubectl get pods -n middleware
