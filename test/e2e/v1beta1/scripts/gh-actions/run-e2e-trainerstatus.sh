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
# This runtime configures pods to report progress to the Trainer Progress Server
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
                          import ssl
                          import urllib.request
                          from datetime import datetime, timezone

                          total_steps = int(os.environ.get('TOTAL_STEPS', '10'))
                          lr = float(os.environ.get('LR', '0.01'))
                          
                          # Get progress reporting endpoint from env (injected by Trainer webhook)
                          progress_endpoint = os.environ.get('TRAINER_PROGRESS_ENDPOINT', '')
                          token_path = os.environ.get('TRAINER_PROGRESS_TOKEN_PATH', '/var/run/secrets/kubernetes.io/serviceaccount/token')
                          
                          def report_progress(progress_pct, loss, step, total, remaining_secs=None):
                              """Report progress to Trainer Progress Server."""
                              if not progress_endpoint:
                                  print(f"[DEBUG] No TRAINER_PROGRESS_ENDPOINT, skipping POST")
                                  return
                                  
                              try:
                                  # Read service account token
                                  with open(token_path, 'r') as f:
                                      token = f.read().strip()
                                  
                                  payload = {
                                      "progressPercentage": progress_pct,
                                      "metrics": [
                                          {"name": "loss", "value": f"{loss:.4f}"},
                                          {"name": "current_step", "value": str(step)},
                                          {"name": "total_steps", "value": str(total)},
                                      ]
                                  }
                                  if remaining_secs is not None:
                                      payload["estimatedRemainingSeconds"] = remaining_secs
                                  
                                  data = json.dumps(payload).encode('utf-8')
                                  
                                  req = urllib.request.Request(
                                      progress_endpoint,
                                      data=data,
                                      headers={
                                          'Content-Type': 'application/json',
                                          'Authorization': f'Bearer {token}'
                                      },
                                      method='POST'
                                  )
                                  
                                  # Skip TLS verification for internal cluster communication
                                  ctx = ssl.create_default_context()
                                  ctx.check_hostname = False
                                  ctx.verify_mode = ssl.CERT_NONE
                                  
                                  with urllib.request.urlopen(req, context=ctx, timeout=10) as resp:
                                      print(f"[PROGRESS] Reported {progress_pct}% to Trainer: {resp.status}")
                              except Exception as e:
                                  print(f"[WARN] Failed to report progress: {e}")

                          print(f"Starting training with LR={lr}, total_steps={total_steps}")
                          print(f"Progress endpoint: {progress_endpoint or 'NOT SET'}")

                          for step in range(total_steps):
                              progress = int((step + 1) / total_steps * 100)
                              loss = 1.0 - (step * 0.08) - (lr * 10)
                              remaining = int((total_steps - step - 1) * 2)

                              print(f"Step {step+1}/{total_steps}: loss={loss:.4f}, progress={progress}%")
                              
                              # Report progress to Trainer Progress Server
                              report_progress(progress, loss, step + 1, total_steps, remaining)
                              
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
