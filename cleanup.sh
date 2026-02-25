#!/bin/bash
# ──────────────────────────────────────────────────────────────
# NewsFlow Full Cleanup Script (K8s Multi-Namespace)
# ──────────────────────────────────────────────────────────────

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${RED}  Cleaning up all NewsFlow resources...${NC}"

# Delete Ingress
kubectl delete -f k8s/ingress.yaml --ignore-not-found

# Delete Services (Scraper, Processor, API)
kubectl delete -f k8s/services.yaml --ignore-not-found
kubectl delete -f k8s/monitoring.yaml --ignore-not-found

# Delete Infrastructure
kubectl delete -f k8s/redpanda.yaml --ignore-not-found
kubectl delete -f k8s/mysql.yaml --ignore-not-found

# Delete Vault
kubectl delete -f k8s/vault/vault.yaml --ignore-not-found

# Delete Namespaces
kubectl delete -f k8s/namespaces.yaml --ignore-not-found
kubectl delete namespace vault --ignore-not-found

# Delete RBAC
kubectl delete clusterrolebinding vault-auth-delegator --ignore-not-found

# Cleanup local temp files
rm -f .vault_init_output.json

echo -e "${GREEN} Cleanup complete!${NC}"
