package controller

import (
	"context"
	"time"

	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

var (
	deletedPods         = make(map[string]string)
	toUnBurstCheckCount = 0
	toBurstCheckCount   = 0
	applyPodCount       = 0
	burstCount          = 0
)

type Controller struct {
	PodNamespace    string
	Client          *kubernetes.Clientset
	Filter          PodFilter
	InformerFactory informers.SharedInformerFactory
	Logger          *zap.Logger
	RebuildSettings *RebuildSettings
	CTX             context.Context
}

type PodFilter struct {
	annotation string
	namespace  string
}

type RebuildSettings struct {
	PodCount                int
	MinioUser               string
	MinioPassword           string
	ProcessImage            string
	MinioEndpoint           string
	JaegerHost              string
	JaegerPort              string
	JaegerOn                string
	ProcessPodCpuRequest    string
	ProcessPodCpuLimit      string
	ProcessPodMemoryRequest string
	ProcessPodMemoryLimit   string
	MessageBrokerUser       string
	MessageBrokerPassword   string
}

func NewPodController(logger *zap.Logger, podNamespace string, rs *RebuildSettings) (*Controller, error) {

	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	filter := PodFilter{
		annotation: "rebuild-pod",
		namespace:  podNamespace,
	}

	informerFactory := informers.NewSharedInformerFactoryWithOptions(client, 0, informers.WithNamespace(podNamespace))
	if err != nil {
		return nil, err
	}

	controller := &Controller{
		PodNamespace:    podNamespace,
		Client:          client,
		Filter:          filter,
		InformerFactory: informerFactory,
		Logger:          logger,
		RebuildSettings: rs,
	}

	return controller, nil
}

func (c *Controller) Run() {

	c.Logger.Info("Starting the controller")
	go c.createInitialPods()

	informer := c.InformerFactory.Core().V1().Pods().Informer()

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: c.recreatePod,
	})
	informer.Run(c.CTX.Done())

	<-c.CTX.Done()
}

func (c *Controller) createInitialPods() {

	applyPodCount = c.RebuildSettings.PodCount

	for {
		runningPodCount := c.podCount("status.phase=Running", "manager=podcontroller")
		pendingPodsCount := c.podCount("status.phase=Pending", "manager=podcontroller")
		totalCount := runningPodCount + pendingPodsCount
		c.Logger.Info("Running pods count ", zap.Int("count", runningPodCount))
		c.Logger.Info("Pending pods count ", zap.Int("count", pendingPodsCount))

		// We increase the amount of pods if we have too many pending pods
		if pendingPodsCount > runningPodCount {
			toBurstCheckCount++
			if toBurstCheckCount == 3 {
				toBurstCheckCount = 0
				applyPodCount = applyPodCount + 5
				burstCount++
			}
		} else {
			toBurstCheckCount = 0
			if burstCount > 0 {
				burstCount--
				applyPodCount = applyPodCount - 5
			}
		}

		if applyPodCount < 110 && applyPodCount > totalCount {
			for i := 0; i < applyPodCount-totalCount; i++ {
				c.CreatePod()
			}
		}

		time.Sleep(60 * time.Second)
	}

}

func (c *Controller) recreatePod(oldObj, newObj interface{}) {
	pod := newObj.(*v1.Pod)
	if c.okToRecreate(pod) {
		c.Logger.Info("We have a pod")
		c.Logger.Sugar().Warnf("New pod is : %v", newObj)

		// we do this just in order to run the pod creation/deletion only once. becauase sometimes we receive same event 2 times for a pod. Needs to investigate why
		_, exist := deletedPods[pod.ObjectMeta.Name]
		if !exist {
			deletedPods[pod.ObjectMeta.Name] = "yes"
			go c.deletePod(pod)
			c.CreatePod()
		}
		// And delete the previous one
		/*
			err := c.deletePod(pod)
			if err != nil {
				c.Logger.Error("Failed to delete pod: ", zap.Error(err))
			} else {
				// We create a new pod. TODO : This needs to be passed to a worker queue
				c.CreatePod() // TODO : need a better way of doing this
			}
		*/
	}
}

func (c *Controller) okToRecreate(pod *v1.Pod) bool {
	return (pod.ObjectMeta.Labels["manager"] == "podcontroller") && // we need to filter out just rebuild pods
		(c.isPodUnhealthy(pod) || // which are either unhealthy
			(pod.Status.Phase == "Succeeded" || pod.Status.Phase == "Failed" || pod.Status.Phase == "Unknown")) // Or completed
}

func (c *Controller) isPodUnhealthy(pod *v1.Pod) bool {
	// Check if any of Containers is in CrashLoop
	statuses := append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...)
	for _, containerStatus := range statuses {
		if containerStatus.RestartCount >= 5 {
			if containerStatus.State.Waiting != nil && containerStatus.State.Waiting.Reason == "CrashLoopBackOff" {
				return true
			}
		}
	}
	return false
}

func (c *Controller) deletePod(pod *v1.Pod) error {
	time.Sleep(30 * time.Second)
	c.Logger.Info("Deleting pod", zap.String("podName", pod.ObjectMeta.Name))
	return c.Client.CoreV1().Pods(pod.ObjectMeta.Namespace).Delete(c.CTX, pod.ObjectMeta.Name, metav1.DeleteOptions{})
}
