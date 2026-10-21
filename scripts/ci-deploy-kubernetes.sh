#!/usr/bin/env bash
set -euo pipefail

: "${LAB_NAMESPACE:?LAB_NAMESPACE is required}"
: "${KUBE_CONTEXT:?KUBE_CONTEXT is required}"
: "${RUNNER_TEMP:?This script is intended for GitHub Actions}"

kubectl config use-context "$KUBE_CONTEXT"
kubectl create namespace "$LAB_NAMESPACE"

# These credentials exist only in this disposable CI cluster.
postgres_password=$(openssl rand -hex 24)
app_password=$(openssl rand -hex 24)
echo "::add-mask::$postgres_password"
echo "::add-mask::$app_password"
kubectl -n "$LAB_NAMESPACE" create secret generic postgres-auth \
  --from-literal="postgres-password=$postgres_password" \
  --from-literal="app-password=$app_password"

for pair in reservation:reservations payment:payments loyalty:loyalties; do
  service="${pair%%:*}"
  database="${pair#*:}"
  kubectl -n "$LAB_NAMESPACE" create secret generic "$service-service-db" \
    --from-literal="DATABASE_URL=postgres://program:${app_password}@postgres:5432/$database?sslmode=disable"
done
unset postgres_password app_password

helm upgrade --install postgres helm/postgres \
  --namespace "$LAB_NAMESPACE" --kube-context "$KUBE_CONTEXT" --wait --timeout 3m

for service in reservation payment loyalty gateway; do
  helm upgrade --install "$service-service" helm/service \
    -f "helm/values/$service.yaml" --namespace "$LAB_NAMESPACE" \
    --kube-context "$KUBE_CONTEXT" --wait --timeout 3m
done

helm repo add traefik https://traefik.github.io/charts
helm repo update
helm upgrade --install traefik traefik/traefik --version 42.0.0 \
  --namespace ingress-system --create-namespace --kube-context "$KUBE_CONTEXT" \
  --set service.spec.type=ClusterIP --wait --timeout 3m

# Forward the controller, so requests still traverse Traefik and the Ingress rule.
nohup kubectl -n ingress-system port-forward service/traefik 8080:80 \
  --address 127.0.0.1 > "$RUNNER_TEMP/traefik-port-forward.log" 2>&1 &
echo "$!" > "$RUNNER_TEMP/traefik-port-forward.pid"

ready=false
for attempt in {1..60}; do
  if curl --noproxy '*' --fail --silent --max-time 2 http://127.0.0.1:8080/manage/health > /dev/null; then
    ready=true
    break
  fi
  sleep 2
done
if [[ "$ready" != true ]]; then
  cat "$RUNNER_TEMP/traefik-port-forward.log"
  exit 1
fi

# Only the runtime address changes; instructor requests/assertions stay original.
python3 - <<'PY'
import json
from pathlib import Path
path = Path("v2/postman/environment.json")
env = json.loads(path.read_text())
for value in env["values"]:
    if value["key"] == "baseUrl":
        value["value"] = "http://127.0.0.1:8080"
path.write_text(json.dumps(env, indent=2) + "\n")
PY
kubectl -n "$LAB_NAMESPACE" get pods,services,ingress,pvc
