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

FROM python:3.11-slim

ENV TARGET_DIR /opt/katib
ENV EARLYSTOPPING_DIR cmd/earlystopping/progressstop/v1beta1

RUN if [ "$(uname -m)" = "ppc64le" ] || [ "$(uname -m)" = "aarch64" ]; then \
    apt-get -y update && \
    apt-get -y install gfortran libopenblas-dev liblapack-dev && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*; \
    fi

ADD ./pkg/ ${TARGET_DIR}/pkg/
ADD ./${EARLYSTOPPING_DIR}/ ${TARGET_DIR}/${EARLYSTOPPING_DIR}/
WORKDIR ${TARGET_DIR}/${EARLYSTOPPING_DIR}

RUN pip install --prefer-binary --no-cache-dir -r requirements.txt
RUN chgrp -R 0 ${TARGET_DIR} \
    && chmod -R g+rwX ${TARGET_DIR}

ENTRYPOINT ["python", "main.py"]
