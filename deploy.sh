#!/bin/bash

set -e

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${YELLOW} Starting NewsFlow Deployment...${NC}"

# 1. Create Namespaces
echo "[1/7] Creating Namespaces..."
kubectl apply -f k8s/namespaces.yaml

# 2. Deploy Vault
echo "[2/7] Deploying Vault..."
kubectl apply -f k8s/vault/vault.yaml
echo "Waiting for Vault pod..."
kubectl wait --for=condition=Ready pod/vault-0 -n vault --timeout=60s

# 3. Initialize & Unseal Vault
echo "[3/7] Checking Vault Status..."
INIT_STATUS=$(kubectl exec -n vault vault-0 -- vault status -format=json || true)
IS_INIT=$(echo $INIT_STATUS | python3 -c "import sys, json; print(str(json.load(sys.stdin).get('initialized', False)).lower())" 2>/dev/null || echo "false")

if [ "$IS_INIT" != "true" ]; then
    echo "Initializing Vault..."
    VAULT_INIT=$(kubectl exec -n vault vault-0 -- vault operator init -key-shares=1 -key-threshold=1 -format=json)
    UNSEAL_KEY=$(echo $VAULT_INIT | python3 -c "import sys, json; print(json.load(sys.stdin)['unseal_keys_b64'][0])")
    ROOT_TOKEN=$(echo $VAULT_INIT | python3 -c "import sys, json; print(json.load(sys.stdin)['root_token'])")
    echo "Unseal Key: $UNSEAL_KEY" > .vault_init_output.txt
    echo "Root Token: $ROOT_TOKEN" >> .vault_init_output.txt
    
    echo "Unsealing Vault (Fresh)..."
    kubectl exec -n vault vault-0 -- vault operator unseal $UNSEAL_KEY
    kubectl exec -n vault vault-0 -- vault login $ROOT_TOKEN
else
    echo "Vault already initialized. Checking if unseal is needed..."
    IS_SEALED=$(echo $INIT_STATUS | python3 -c "import sys, json; print(str(json.load(sys.stdin).get('sealed', True)).lower())" 2>/dev/null || echo "true")
    if [ "$IS_SEALED" == "true" ]; then
        if [ -f .vault_init_output.txt ]; then
            UNSEAL_KEY=$(grep "Unseal Key" .vault_init_output.txt | cut -d' ' -f3)
            ROOT_TOKEN=$(grep "Root Token" .vault_init_output.txt | cut -d' ' -f3)
            echo "Unsealing Vault (Existing)..."
            kubectl exec -n vault vault-0 -- vault operator unseal $UNSEAL_KEY
            kubectl exec -n vault vault-0 -- vault login $ROOT_TOKEN
        else
            echo "Vault is sealed but .vault_init_output.txt is missing! Manual unseal required."
            exit 1
        fi
    fi
fi

# 4. Configure Vault
echo "[4/7] Configuring Vault Secrets & Auth..."
kubectl exec -n vault vault-0 -- vault secrets enable -path=secret kv || true
kubectl exec -n vault vault-0 -- vault auth enable kubernetes || true

# Clusterrole for Vault
kubectl create clusterrolebinding vault-auth-delegator --clusterrole=system:auth-delegator --serviceaccount=vault:default --dry-run=client -o yaml | kubectl apply -f -

kubectl exec -n vault vault-0 -- vault write auth/kubernetes/config \
    kubernetes_host="https://kubernetes.default.svc:443" \
    disable_iss_validation=true \
    issuer="https://kubernetes.default.svc.cluster.local"

# Copy and Write Policy
kubectl cp k8s/vault/newsflow-read.hcl vault/vault-0:/tmp/newsflow-read.hcl
kubectl exec -n vault vault-0 -- vault policy write newsflow-read /tmp/newsflow-read.hcl

# Roles & Secrets
kubectl exec -n vault vault-0 -- vault write auth/kubernetes/role/newsflow-role \
    bound_service_account_names="*" \
    bound_service_account_namespaces="news-api,news-processor,news-mysql" \
    policies=newsflow-read \
    ttl=24h

kubectl exec -n vault vault-0 -- vault kv put secret/newsflow/mysql \
    password="rootpass" \
    root_password="rootpass" \
    user_password="newspass" \
    dsn="newsuser:newspass@tcp(mysql.news-mysql.svc.cluster.local:3306)/newsdb?parseTime=true&charset=utf8mb4"

# 5. Deploy Infrastructure
echo "[5/7] Deploying MySQL & RedPanda..."
kubectl apply -f k8s/mysql.yaml
kubectl apply -f k8s/redpanda.yaml

# 6. Deploy Services
echo "[6/7] Deploying Scraper, Processor, API..."
kubectl apply -f k8s/services.yaml
kubectl apply -f k8s/ingress.yaml

# 7. Deploy Monitoring
echo "[7/7] Deploying Prometheus & Grafana..."
kubectl apply -f k8s/monitoring.yaml

echo "Waiting for pods to be ready..."
kubectl wait --for=condition=Ready pod -l app=news-api -n news-api --timeout=60s
kubectl wait --for=condition=Ready pod -l app=prometheus -n monitoring --timeout=60s
kubectl wait --for=condition=Ready pod -l app=grafana -n monitoring --timeout=60s

# 8. Port-Forwarding (Background)
echo "[8/8] Starting Port-Forwards..."
pkill -f "kubectl port-forward" || true
# Give processes a few seconds to start listening internally
sleep 5
nohup kubectl port-forward -n vault service/vault 8200:8200 > vault_pf.log 2>&1 &
nohup kubectl port-forward -n news-api service/api-service 8080:8080 > api_pf.log 2>&1 &
nohup kubectl port-forward -n monitoring service/grafana 3000:3000 > grafana_pf.log 2>&1 &
disown -a

echo -e "\n${GREEN} Deployment Complete!${NC}"
echo "------------------------------------------------"
echo "News Dashboard:  http://localhost:8080/user/"
echo "Vault UI:        http://localhost:8200"
echo "Grafana Dash:    http://localhost:3000"
echo "Vault Token:     $ROOT_TOKEN"
echo "------------------------------------------------"
echo "Note: Port-forwards are running in the background."
