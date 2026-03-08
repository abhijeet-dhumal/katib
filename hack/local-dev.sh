#!/bin/bash
# Local development script for Katib controller
# Run this to develop and test changes without building images

set -e

NAMESPACE="${NAMESPACE:-kubeflow}"
LOCAL_CONFIG_DIR="/tmp/katib-local-dev"
CERT_DIR="${LOCAL_CONFIG_DIR}/certs"

echo "=== Katib Local Development ==="
echo "Namespace: ${NAMESPACE}"
echo ""

# Step 1: Scale down in-cluster controller
echo "[1/5] Scaling down in-cluster controller..."
kubectl scale deployment katib-controller -n ${NAMESPACE} --replicas=0 2>/dev/null || true
echo "Waiting for controller to stop..."
kubectl wait --for=delete pod -l katib.kubeflow.org/component=controller -n ${NAMESPACE} --timeout=60s 2>/dev/null || true

# Step 2: Extract katib-config
echo "[2/5] Extracting katib-config..."
mkdir -p ${LOCAL_CONFIG_DIR}
kubectl get configmap katib-config -n ${NAMESPACE} -o jsonpath='{.data.katib-config\.yaml}' > ${LOCAL_CONFIG_DIR}/katib-config.yaml

# Add TrainerStatus collector if not present
if ! grep -q "TrainerStatus" ${LOCAL_CONFIG_DIR}/katib-config.yaml; then
    echo "Adding TrainerStatus collector to config..."
    # Insert TrainerStatus collector
    sed -i.bak '/metricsCollectors:/a\
        - kind: TrainerStatus\
          image: ghcr.io/kubeflow/katib/trainerstatus-metrics-collector:latest' ${LOCAL_CONFIG_DIR}/katib-config.yaml
fi

# Disable cert generation for local development
echo "[3/5] Configuring for local development..."
cat > ${LOCAL_CONFIG_DIR}/katib-config-local.yaml << 'EOF'
---
apiVersion: config.kubeflow.org/v1beta1
kind: KatibConfig
init:
  certGenerator:
    enable: false
  controller:
    webhookPort: 8443
    trialResources:
      - TrainJob.v1alpha1.trainer.kubeflow.org
      - Job.v1.batch
      - TFJob.v1.kubeflow.org
      - PyTorchJob.v1.kubeflow.org
      - MPIJob.v1.kubeflow.org
      - XGBoostJob.v1.kubeflow.org
runtime:
  metricsCollectors:
    - kind: StdOut
      image: ghcr.io/kubeflow/katib/file-metrics-collector:v0.19.0
    - kind: File
      image: ghcr.io/kubeflow/katib/file-metrics-collector:v0.19.0
    - kind: TensorFlowEvent
      image: ghcr.io/kubeflow/katib/tfevent-metrics-collector:v0.19.0
      resources:
        limits:
          memory: 1Gi
    - kind: TrainerStatus
      image: ghcr.io/kubeflow/katib/trainerstatus-metrics-collector:latest
  suggestions:
    - algorithmName: random
      image: ghcr.io/kubeflow/katib/suggestion-hyperopt:v0.19.0
    - algorithmName: tpe
      image: ghcr.io/kubeflow/katib/suggestion-hyperopt:v0.19.0
    - algorithmName: grid
      image: ghcr.io/kubeflow/katib/suggestion-optuna:v0.19.0
    - algorithmName: hyperband
      image: ghcr.io/kubeflow/katib/suggestion-hyperband:v0.19.0
    - algorithmName: bayesianoptimization
      image: ghcr.io/kubeflow/katib/suggestion-skopt:v0.19.0
    - algorithmName: cmaes
      image: ghcr.io/kubeflow/katib/suggestion-goptuna:v0.19.0
EOF

# Step 4: Extract webhook certs from cluster
echo "[4/5] Extracting webhook certificates..."
mkdir -p ${CERT_DIR}
kubectl get secret katib-webhook-cert -n ${NAMESPACE} -o jsonpath='{.data.tls\.crt}' | base64 -d > ${CERT_DIR}/tls.crt 2>/dev/null || true
kubectl get secret katib-webhook-cert -n ${NAMESPACE} -o jsonpath='{.data.tls\.key}' | base64 -d > ${CERT_DIR}/tls.key 2>/dev/null || true

if [ ! -s ${CERT_DIR}/tls.crt ]; then
    echo "Generating self-signed certificates..."
    openssl req -x509 -newkey rsa:4096 -keyout ${CERT_DIR}/tls.key -out ${CERT_DIR}/tls.crt -days 365 -nodes -subj "/CN=katib-controller.${NAMESPACE}.svc" 2>/dev/null
fi

# Step 5: Print run instructions
echo "[5/5] Setup complete!"
echo ""
echo "=== Run Controller Locally ==="
echo ""
echo "Option 1: Run with webhooks (full functionality):"
echo ""
echo "  export CERT_DIR=${CERT_DIR}"
echo "  cd $(pwd)"
echo "  go run ./cmd/katib-controller/v1beta1/main.go \\"
echo "    --katib-config=${LOCAL_CONFIG_DIR}/katib-config-local.yaml"
echo ""
echo "Option 2: Quick run (controller-runtime handles certs):"
echo ""
echo "  cd $(pwd)"
echo "  go run ./cmd/katib-controller/v1beta1/main.go"
echo ""
echo "=== Port Forward DB Manager (in another terminal) ==="
echo ""
echo "  kubectl port-forward svc/katib-db-manager -n ${NAMESPACE} 6789:6789"
echo ""
echo "=== Run UI Locally (in another terminal) ==="
echo ""
echo "  cd $(pwd)/pkg/ui/v1beta1/frontend"
echo "  npm install"
echo "  npm start"
echo ""
echo "=== Restore In-Cluster Controller ==="
echo ""
echo "  kubectl scale deployment katib-controller -n ${NAMESPACE} --replicas=1"
echo ""
