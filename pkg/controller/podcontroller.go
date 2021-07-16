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
	deletedPods = make(map[string]string)
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
		AddFunc:    c.handleSchedAdd,
		UpdateFunc: c.recreatePod,
		DeleteFunc: c.handleSchedDelete,
	})

	informer.Run(c.CTX.Done())

	<-c.CTX.Done()
}

/*
func podLastTransitionTime(podObj *v1.Pod) time.Time {
	for _, pc := range podObj.Status.Conditions {
		if pc.Type == v1.PodScheduled && pc.Status == v1.ConditionFalse {
			return pc.LastTransitionTime.Time
		}
	}
	return time.Time{}
}*/
func (c *Controller) createInitialPods() {

	for {

		count := c.podCount("status.phase=Running", "manager=podcontroller")
		count += c.podCount("status.phase=Pending", "manager=podcontroller")
		c.Logger.Info("Initia Running pods count ", zap.Int("count", count))
		c.Logger.Info("Initia Pending pods count ", zap.Int("count", count))

		for i := 0; i < c.RebuildSettings.PodCount-count; i++ {
			c.CreatePod()

		}
		time.Sleep(30 * time.Second)

	}

}

func (c *Controller) handleSchedAdd(newObj interface{}) {
	c.Logger.Sugar().Warnf("handleSchedAdd is : %v", newObj)
	pod := newObj.(*v1.Pod)

	c.Logger.Sugar().Infof("new pod status : %v", pod.Status.Phase)

	/*pod, ok := newObj.(*v1.Pod)
	if !ok {
		c.Logger.Info("failed to cast object")
		return
	}

	if c.okToRecreate(pod) {
		c.Logger.Info("We have a pod")
		c.Logger.Sugar().Warnf("New pod is : %v", newObj)
		c.CreatePod()

	}*/
}
func (c *Controller) recreatePod(oldObj, newObj interface{}) {
	c.Logger.Info("update event We have a pod")
	c.Logger.Sugar().Infof("New old is : %v", oldObj)

	c.Logger.Sugar().Infof("New pod is : %v", newObj)

	podold := oldObj.(*v1.Pod)

	pod := newObj.(*v1.Pod)
	c.Logger.Sugar().Infof("new pod status : %v", pod.Status.Phase)
	c.Logger.Sugar().Infof("old pod status : %v", podold.Status.Phase)

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
func (c *Controller) handleSchedDelete(obj interface{}) {
	c.Logger.Sugar().Warnf("delete pod is : %v", obj)

}

func (c *Controller) deletePod(pod *v1.Pod) error {
	time.Sleep(30 * time.Second)
	c.Logger.Info("Deleting pod", zap.String("podName", pod.ObjectMeta.Name))
	return c.Client.CoreV1().Pods(pod.ObjectMeta.Namespace).Delete(c.CTX, pod.ObjectMeta.Name, metav1.DeleteOptions{})
}
