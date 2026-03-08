/*
Copyright 2024 The Kubeflow Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

/*
TrainerStatusCollector collects metrics from TrainJob's trainerStatus field.
This collector enables real-time progress tracking for Kubeflow Trainer jobs
that use HuggingFace Transformers with the KubeflowCallback.

The collector watches the TrainJob's status.trainerStatus field and reports
metrics such as loss, progress percentage, and estimated remaining time to
the Katib DB Manager.
*/

package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/klog/v2"

	api "github.com/kubeflow/katib/pkg/apis/manager/v1beta1"
	tscollector "github.com/kubeflow/katib/pkg/metricscollector/v1beta1/trainerstatus-collector"
)

var (
	dbManagerServiceAddr = flag.String("s-db", "", "Katib DB Manager service endpoint")
	trialName            = flag.String("t", "", "Trial Name")
	trainJobName         = flag.String("trainjob", "", "TrainJob Name (defaults to trial name)")
	namespace            = flag.String("namespace", "", "Namespace where TrainJob is running")
	metricNames          = flag.String("m", "", "Metric names (semicolon separated)")
	pollInterval         = flag.Duration("poll-interval", 5*time.Second, "Interval between TrainJob status checks")
	kubernetesAPIHost    = flag.String("k8s-api", "https://kubernetes.default.svc", "Kubernetes API server address")
	bearerTokenPath      = flag.String("token-path", "/var/run/secrets/kubernetes.io/serviceaccount/token", "Path to service account token")
)

func main() {
	flag.Parse()

	klog.Infof("TrainerStatus Collector starting")
	klog.Infof("Trial Name: %s", *trialName)
	klog.Infof("TrainJob Name: %s", getTrainJobName())
	klog.Infof("Namespace: %s", *namespace)
	klog.Infof("Poll Interval: %v", *pollInterval)

	// Read bearer token from file
	bearerToken := ""
	if *bearerTokenPath != "" {
		tokenBytes, err := os.ReadFile(*bearerTokenPath)
		if err != nil {
			klog.Warningf("Failed to read bearer token: %v", err)
		} else {
			bearerToken = strings.TrimSpace(string(tokenBytes))
		}
	}

	// Parse metric names
	var metrics []string
	if *metricNames != "" {
		metrics = strings.Split(*metricNames, ";")
	}

	// Create collector config
	config := tscollector.CollectorConfig{
		TrainJobName:      getTrainJobName(),
		Namespace:         *namespace,
		MetricNames:       metrics,
		PollInterval:      *pollInterval,
		KubernetesAPIHost: *kubernetesAPIHost,
		BearerToken:       bearerToken,
	}

	// Create gRPC connection to DB Manager
	conn, err := grpc.NewClient(*dbManagerServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		klog.Fatalf("Could not connect to DB Manager service: %v", err)
	}
	defer conn.Close()

	dbClient := api.NewDBManagerClient(conn)

	// Create report function
	reportFunc := func(olog *api.ObservationLog) error {
		req := &api.ReportObservationLogRequest{
			TrialName:      *trialName,
			ObservationLog: olog,
		}
		_, err := dbClient.ReportObservationLog(context.Background(), req)
		return err
	}

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		klog.Infof("Received signal %v, shutting down", sig)
		cancel()
	}()

	// Start watching and collecting
	klog.Info("Starting TrainerStatus collection")
	if err := tscollector.WatchAndCollect(ctx, config, reportFunc); err != nil {
		klog.Fatalf("Collection failed: %v", err)
	}

	// Report final metrics
	klog.Info("Collecting final metrics")
	finalLog, err := tscollector.CollectObservationLog(config)
	if err != nil {
		klog.Errorf("Failed to collect final metrics: %v", err)
	} else if len(finalLog.MetricLogs) > 0 {
		if err := reportFunc(finalLog); err != nil {
			klog.Errorf("Failed to report final metrics: %v", err)
		} else {
			klog.Infof("Final metrics reported: %v", finalLog)
		}
	}

	klog.Info("TrainerStatus Collector finished")
}

func getTrainJobName() string {
	if *trainJobName != "" {
		return *trainJobName
	}
	return *trialName
}
