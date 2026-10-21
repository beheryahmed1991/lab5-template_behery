#!/usr/bin/env bash
# CI synchronization adapter; never changes instructor requests or assertions.
set -euo pipefail
: "${LAB4_REAL_KUBECTL:?Absolute path of the real kubectl is required}"
"$LAB4_REAL_KUBECTL" "$@"

if [[ "${1:-}" == scale && "${2:-}" == deployment ]]; then
  deployment="$3"
  namespace=default
  replicas=""
  shift 3
  while (($#)); do
    case "$1" in
      -n|--namespace) namespace="$2"; shift 2 ;;
      --namespace=*) namespace="${1#*=}"; shift ;;
      --replicas) replicas="$2"; shift 2 ;;
      --replicas=*) replicas="${1#*=}"; shift ;;
      *) shift ;;
    esac
  done
  if [[ "$replicas" == 0 ]]; then
    "$LAB4_REAL_KUBECTL" -n "$namespace" wait pod \
      -l "app.kubernetes.io/instance=$deployment" --for=delete --timeout=120s
  elif [[ "$replicas" == 1 ]]; then
    "$LAB4_REAL_KUBECTL" -n "$namespace" rollout status "deployment/$deployment" --timeout=120s
    # Gateway's read circuit needs its 10-second cooldown after an outage.
    sleep 11
  fi
fi
