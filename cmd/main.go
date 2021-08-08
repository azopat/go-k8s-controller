package main

import (
	"context"
	"os"
	"strconv"
	"strings"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/util/runtime"

	podcontroller "github.com/k8-proxy/go-k8s-controller/pkg/controller"
)

func main() {

	ctx, _ := context.WithCancel(context.Background())
	loggerConfig := zap.NewProductionConfig()

	// general logger
	logger, err := loggerConfig.Build()
	runtime.Must(err)

	podNamespace := os.Getenv("PROCESS_POD_NAMESPACE")

	podCountStr := os.Getenv("POD_COUNT")
	minioUser := os.Getenv("MINIO_USER")
	minioPassword := os.Getenv("MINIO_PASSWORD")
	processImage := os.Getenv("PROCESS_IMAGE")
	minioEndpoint := os.Getenv("MINIO_ENDPOINT")
	messageBrokerUser := os.Getenv("MESSAGE_BROKER_USER")
	messageBrokerPassword := os.Getenv("MESSAGE_BROKER_PASSWORD")
	podCount, err := strconv.Atoi(podCountStr)

	processPodCpuRequest := os.Getenv("PROCESS_POD_CPU_REQUEST")
	if strings.TrimSpace(processPodCpuRequest) == "" {
		processPodCpuRequest = "25m"
	}

	processPodCpuLimit := os.Getenv("PROCESS_POD_CPU_LIMIT")
	if strings.TrimSpace(processPodCpuLimit) == "" {
		processPodCpuLimit = "1"
	}

	processPodMemoryRequest := os.Getenv("PROCESS_POD_MEMORY_REQUEST")
	if strings.TrimSpace(processPodMemoryRequest) == "" {
		processPodMemoryRequest = "200Mi"
	}

	processPodMemoryLimit := os.Getenv("PROCESS_POD_MEMORY_LIMIT")
	if strings.TrimSpace(processPodMemoryLimit) == "" {
		processPodMemoryLimit = "500Mi"
	}

	jaegerHost := os.Getenv("JAEGER_AGENT_HOST")
	jaegerPort := os.Getenv("JAEGER_AGENT_PORT")
	jaegerOn := os.Getenv("JAEGER_AGENT_ON")
	if err != nil {
		podCount = 10 // default value
	}

	rs := &podcontroller.RebuildSettings{
		PodCount:                podCount,
		MinioUser:               minioUser,
		MinioPassword:           minioPassword,
		ProcessImage:            processImage,
		MinioEndpoint:           minioEndpoint,
		JaegerHost:              jaegerHost,
		JaegerPort:              jaegerPort,
		JaegerOn:                jaegerOn,
		ProcessPodCpuRequest:    processPodCpuRequest,
		ProcessPodCpuLimit:      processPodCpuLimit,
		ProcessPodMemoryRequest: processPodMemoryRequest,
		ProcessPodMemoryLimit:   processPodMemoryLimit,
		MessageBrokerUser:       messageBrokerUser,
		MessageBrokerPassword:   messageBrokerPassword,
	}

	ctrl, err := podcontroller.NewPodController(logger, podNamespace, rs)
	if err != nil {
		logger.Panic("Failed to initialise the controller", zap.Error(err))
	}

	ctrl.CTX = ctx
	ctrl.Run()

	<-ctx.Done()
}
