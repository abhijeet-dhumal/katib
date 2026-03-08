# Copyright 2024 The Kubeflow Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""
Main entrypoint for the ProgressStop early stopping service.

This service provides progress-based early stopping for Katib experiments,
using real-time training metrics from TrainerStatus collector to make
intelligent early stopping decisions.
"""

import logging
from concurrent import futures

import grpc

from pkg.apis.manager.v1beta1.python import api_pb2_grpc
from pkg.earlystopping.v1beta1.progressstop.service import ProgressStopService

logger = logging.getLogger(__name__)
logging.basicConfig(level=logging.INFO)

DEFAULT_PORT = "6789"


def serve():
    """Start the gRPC server for ProgressStop early stopping service."""
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=10),
        options=[
            ("grpc.max_send_message_length", -1),
            ("grpc.max_receive_message_length", -1),
        ],
    )
    
    service = ProgressStopService()
    api_pb2_grpc.add_EarlyStoppingServicer_to_server(service, server)
    
    # Add health check service
    from grpc_health.v1 import health_pb2_grpc
    from grpc_health.v1.health import HealthServicer
    
    health_servicer = HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)
    
    server.add_insecure_port(f"[::]:{DEFAULT_PORT}")
    logger.info(f"ProgressStop early stopping service starting on port {DEFAULT_PORT}")
    server.start()
    
    try:
        server.wait_for_termination()
    except KeyboardInterrupt:
        logger.info("Shutting down ProgressStop service")
        server.stop(0)


if __name__ == "__main__":
    serve()
