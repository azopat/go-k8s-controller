package controller

import (
	"fmt"
	"log"
	"time"

	"github.com/matryer/try"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	guuid "github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *Controller) podCount(selector string, labelSelector string) int {
	pods, err := c.Client.CoreV1().Pods(c.PodNamespace).List(metav1.ListOptions{
		LabelSelector: labelSelector,
		FieldSelector: selector,
	})
	if err != nil {
		return 0
	}

	count := len(pods.Items)

	return count
}

func (c *Controller) CreatePod() error {
	podSpec := c.GetPodObject()

	var pod *core.Pod = nil

	err := try.Do(func(attempt int) (bool, error) {
		var err error

		pod, err = c.Client.CoreV1().Pods(c.PodNamespace).Create(podSpec)

		if err != nil && attempt < 5 {
			time.Sleep(5 * time.Second) // 5 second wait
		}

		return attempt < 5, err // try 5 times
	})

	if err != nil {
		return err
	}

	if err == nil && pod == nil {
		err = fmt.Errorf("Failed to create pod and no error returned")
		return err
	}

	if pod != nil {
		log.Printf("Successfully created Pod")
	}

	return nil
}

func (c *Controller) GetPodObject() *core.Pod {
	return &core.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rebuild-pod-" + guuid.New().String(),
			Labels:    map[string]string{"manager": "podcontroller"},
			Namespace: c.PodNamespace,
		},
		Spec: core.PodSpec{
			ImagePullSecrets: []core.LocalObjectReference{{Name: "regcred"}},
			RestartPolicy:    core.RestartPolicyNever,
			Volumes: []core.Volume{
				{
					Name: "sourcedir",
					VolumeSource: core.VolumeSource{
						PersistentVolumeClaim: &core.PersistentVolumeClaimVolumeSource{
							ClaimName: "glasswallsource-pvc",
						},
					},
				},
				{
					Name: "targetdir",
					VolumeSource: core.VolumeSource{
						PersistentVolumeClaim: &core.PersistentVolumeClaimVolumeSource{
							ClaimName: "glasswalltarget-pvc",
						},
					},
				},
			},
			Containers: []core.Container{
				{
					Name:            "rebuild",
					Image:           "azopat/adaptation",
					ImagePullPolicy: core.PullIfNotPresent,
					Env: []core.EnvVar{
						{Name: "AMQP_URL", Value: "amqp://guest:guest@rabbitmq-service:5672/"},
						{Name: "INPUT_MOUNT", Value: "/var/source"},
						{Name: "OUTPUT_MOUNT", Value: "/var/target"},
						{Name: "REQUEST_PROCESSING_IMAGE", Value: "glasswallsolutions/icap-request-processing:develop-77b6369"},
						{Name: "REQUEST_PROCESSING_TIMEOUT", Value: "00:01:00"},
						{Name: "ADAPTATION_REQUEST_QUEUE_HOSTNAME", Value: "rabbitmq-service"},
						{Name: "ADAPTATION_REQUEST_QUEUE_PORT", Value: "5672"},
						{Name: "ARCHIVE_ADAPTATION_QUEUE_REQUEST_HOSTNAME", Value: "rabbitmq-service"},
						{Name: "ARCHIVE_ADAPTATION_REQUEST_QUEUE_PORT", Value: "5672"},
						{Name: "TRANSACTION_EVENT_QUEUE_HOSTNAME", Value: "rabbitmq-service"},
						{Name: "TRANSACTION_EVENT_QUEUE_PORT", Value: "5672"},
						{Name: "CPU_LIMIT", Value: "1"},
						{Name: "CPU_REQUEST", Value: "0.25"},
						{Name: "MEMORY_LIMIT", Value: "10000Mi"},
						{Name: "MEMORY_REQUEST", Value: "250Mi"},
					},
					VolumeMounts: []core.VolumeMount{
						{Name: "sourcedir", MountPath: "/var/source"},
						{Name: "targetdir", MountPath: "/var/target"},
					},
					Resources: core.ResourceRequirements{
						Limits: core.ResourceList{
							core.ResourceCPU:    resource.MustParse("1"),
							core.ResourceMemory: resource.MustParse("500Mi"),
						},
						Requests: core.ResourceList{
							core.ResourceCPU:    resource.MustParse("25m"),
							core.ResourceMemory: resource.MustParse("100Mi"),
						},
					},
				},
			},
		},
	}
}
