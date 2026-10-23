#!/usr/bin/env bash
set -euo pipefail
: "${LAB_NAMESPACE:?LAB_NAMESPACE is required}"
: "${KUBE_CONTEXT:?KUBE_CONTEXT is required}"
: "${RUNNER_TEMP:?Run inside GitHub Actions}"
kubectl config use-context "$KUBE_CONTEXT"
kubectl create namespace "$LAB_NAMESPACE"
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
  kubectl -n "$LAB_NAMESPACE" create secret generic "${service}-service-db" \
    --from-literal="DATABASE_URL=postgres://program:${app_password}@postgres:5432/${database}?sslmode=disable"
done
unset postgres_password app_password
helm upgrade --install postgres helm/postgres \
  --namespace "$LAB_NAMESPACE" --kube-context "$KUBE_CONTEXT" \
  --set image.pullPolicy=Never --wait --timeout 5m
for service in reservation payment loyalty gateway; do
  helm upgrade --install "${service}-service" helm/service \
    -f "helm/values/${service}.yaml" \
    --namespace "$LAB_NAMESPACE" --kube-context "$KUBE_CONTEXT" \
    --set "image.repository=lab5-${service}-service" --set image.tag=ci \
    --set image.pullPolicy=Never --set ingress.enabled=false \
    --set-string "env.AUTH0_ISSUER=$AUTH0_ISSUER" \
    --set-string "env.AUTH0_AUDIENCE=$AUTH0_AUDIENCE" \
    --set-string "env.AUTH0_JWKS_URL=$AUTH0_JWKS_URL" \
    --wait --timeout 5m
done
nohup kubectl -n "$LAB_NAMESPACE" port-forward service/gateway-service 8080:8080 \
  --address 127.0.0.1 > "$RUNNER_TEMP/lab5-port-forward.log" 2>&1 &
for attempt in {1..60}; do
  if curl --noproxy '*' --fail --silent --max-time 2 http://127.0.0.1:8080/manage/health > /dev/null; then
    kubectl -n "$LAB_NAMESPACE" get pods,services,pvc
    exit 0
  fi
  sleep 2
done
cat "$RUNNER_TEMP/lab5-port-forward.log"
echo 'Gateway health check timed out' >&2
exit 1
