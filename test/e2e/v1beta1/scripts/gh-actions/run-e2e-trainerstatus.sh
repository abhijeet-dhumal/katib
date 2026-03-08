#!/usr/bin/env bash

# E2E test script for TrainerStatus metrics collector
# 
# Prerequisites:
# - Kubernetes cluster with Katib installed
# - Kubeflow Trainer installed with TrainJob CRD
# - TrainerStatus collector image available
# - torch-progress-test ClusterTrainingRuntime created

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TESTDATA_DIR="${SCRIPT_DIR}/../../testdata/trainerstatus"
NAMESPACE="${NAMESPACE:-kubeflow}"

echo "========================================"
echo "TrainerStatus Collector E2E Test"
echo "========================================"
echo "Namespace: ${NAMESPACE}"
echo "Test data: ${TESTDATA_DIR}"
echo ""

# Check prerequisites
echo "Checking prerequisites..."

# Check if Katib is installed
if ! kubectl get crd experiments.kubeflow.org &>/dev/null; then
    echo "ERROR: Katib CRDs not found. Please install Katib first."
    exit 1
fi

# Check if Trainer is installed
if ! kubectl get crd trainjobs.trainer.kubeflow.org &>/dev/null; then
    echo "ERROR: Kubeflow Trainer CRDs not found. Please install Trainer first."
    exit 1
fi

# Check if namespace exists
if ! kubectl get namespace "${NAMESPACE}" &>/dev/null; then
    echo "Creating namespace ${NAMESPACE}..."
    kubectl create namespace "${NAMESPACE}"
fi

# Enable metrics collector injection
echo "Enabling metrics collector injection for namespace ${NAMESPACE}..."
kubectl label namespace "${NAMESPACE}" \
    katib.kubeflow.org/metrics-collector-injection=enabled \
    --overwrite

# Create ClusterTrainingRuntime for the test
echo "Creating ClusterTrainingRuntime for test..."
cat <<EOF | kubectl apply -f -
apiVersion: trainer.kubeflow.org/v1alpha1
kind: ClusterTrainingRuntime
metadata:
  name: torch-progress-test
spec:
  mlPolicy:
    numNodes: 1
  template:
    spec:
      replicatedJobs:
        - name: node
          template:
            spec:
              template:
                spec:
                  containers:
                    - name: node
                      image: python:3.11-slim
                      command:
                        - python3
                        - -c
                        - |
                          import os
                          import time
                          import json
                          from datetime import datetime, timezone

                          total_steps = int(os.environ.get('TOTAL_STEPS', '10'))
                          lr = float(os.environ.get('LR', '0.01'))

                          print(f"Starting training with LR={lr}, total_steps={total_steps}")

                          for step in range(total_steps):
                              progress = int((step + 1) / total_steps * 100)
                              loss = 1.0 - (step * 0.08) - (lr * 10)
                              remaining = int((total_steps - step - 1) * 2)

                              print(f"Step {step+1}/{total_steps}: loss={loss:.4f}, progress={progress}%")
                              time.sleep(2)

                          print("Training complete!")
                      env:
                        - name: TOTAL_STEPS
                          value: "10"
                      resources:
                        requests:
                          cpu: "100m"
                          memory: "128Mi"
                        limits:
                          cpu: "500m"
                          memory: "512Mi"
EOF

echo ""
echo "Running E2E test..."
echo ""

# Run the Python test
python3 "${SCRIPT_DIR}/run-e2e-trainerstatus.py" \
    --experiment-path "${TESTDATA_DIR}/trainerstatus-experiment.yaml" \
    --namespace "${NAMESPACE}" \
    --verbose

echo ""
echo "========================================"
echo "E2E Test Complete"
echo "========================================"
