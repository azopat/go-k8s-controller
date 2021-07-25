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

type Controller struct {
	PodNamespace    string
	Client          *kubernetes.Clientset
	Filter          PodFilter
	InformerFactory informers.SharedInformerFactory
	Logger          *zap.Logger
	RebuildSettings *RebuildSettings
	CTX             context.Context
	CreateNew       bool
	CalcCount       int
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
	nestedloop := 0
	sleepnum := 2 // Second
	c.CalcCount = c.RebuildSettings.PodCount
	for {
		c.CreateNew = false
		count := c.podCount("status.phase=Running", "manager=podcontroller")
		count += c.podCount("status.phase=Pending", "manager=podcontroller")
		for i := 0; i < c.RebuildSettings.PodCount-count; i++ {
			c.CreatePod()

		}
		c.CalcCount = count

		c.Logger.Sugar().Infof("createInitialPods count: %v", count)
		c.CreateNew = true

		if nestedloop > 60 {
			nestedloop = 0
			// Create a pod interface for the given namespace

			podInterface := c.Client.CoreV1().Pods(c.PodNamespace)

			// List the pods in the given namespace
			podList, err := podInterface.List(c.CTX, metav1.ListOptions{LabelSelector: "manager=podcontroller"})

			if err != nil {
				c.Logger.Sugar().Warnf("Initia Running error :  %v", err)

			}
			for _, pod := range podList.Items {
				// Calculate the age of the pod
				podCreationTime := pod.GetCreationTimestamp()
				age := time.Since(podCreationTime.Time).Round(time.Second)
				Duration, _ := time.ParseDuration(age.String())
				// Get the status of each of the pods
				podStatus := pod.Status

				if podStatus.Phase == "Running" || podStatus.Phase == "Pending" || podStatus.Phase == "ContainerCreating" {
					if Duration > time.Duration(180)*time.Second {

						if podStatus.Phase == "Pending" || podStatus.Phase == "ContainerCreating" {
							c.Logger.Sugar().Infof(" Duration delete: %v", Duration)
							c.Client.CoreV1().Pods(pod.ObjectMeta.Namespace).Delete(c.CTX, pod.ObjectMeta.Name, metav1.DeleteOptions{})
						}
					}
					continue
				} else {

					c.Client.CoreV1().Pods(pod.ObjectMeta.Namespace).Delete(c.CTX, pod.ObjectMeta.Name, metav1.DeleteOptions{})

				}

			}
		}

		time.Sleep(time.Duration(sleepnum) * time.Second)
		nestedloop = nestedloop + sleepnum

	}

}

func (c *Controller) handleSchedAdd(newObj interface{}) {
	c.Logger.Sugar().Infof("handleSchedAdd is : %v", newObj)
	pod := newObj.(*v1.Pod)

	c.Logger.Sugar().Infof("new pod status : %v", pod.Status.Phase)

}
func (c *Controller) recreatePod(oldObj, newObj interface{}) {
	podold := oldObj.(*v1.Pod)

	pod := newObj.(*v1.Pod)
	c.Logger.Sugar().Infof("recreatePod new pod status : %v", pod.Status.Phase)
	c.Logger.Sugar().Infof("recreatePod old pod status : %v", podold.Status.Phase)
	c.Logger.Sugar().Infof("CalcCount count: %v", c.CalcCount)

	if pod.Status.Phase == "Running" || pod.Status.Phase == "Pending" {
		return
	}

	if c.CreateNew {
		if c.CalcCount < c.RebuildSettings.PodCount {
			c.Logger.Sugar().Infof("CalcCount creat pods count: %v", c.CalcCount)

			c.CreatePod()
		}
	}
	if (pod.ObjectMeta.Labels["manager"] == "podcontroller") && c.isPodUnhealthy(pod) {
		go c.deletePod(pod)
		return
	}
	if (podold.ObjectMeta.Labels["manager"] == "podcontroller") && c.isPodUnhealthy(podold) {
		go c.deletePod(podold)
		return
	}

	if c.okToRecreate(pod) {
		go c.deletePod(pod)
		if c.CreateNew {
			c.CalcCount = c.CalcCount - 1
		}
		return
	}
	if c.okToRecreate(podold) {
		go c.deletePod(podold)
		return
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
	pod := obj.(*v1.Pod)
	c.Logger.Sugar().Warnf("delete pod name is : %v", pod.Name)
	c.Logger.Sugar().Infof("delete pod status : %v", pod.Status.Phase)

}

func (c *Controller) deletePod(pod *v1.Pod) error {
	if pod.ObjectMeta.Labels["manager"] == "podcontroller" {
		c.Logger.Info("Deleting pod", zap.String("podName", pod.ObjectMeta.Name))
		return c.Client.CoreV1().Pods(pod.ObjectMeta.Namespace).Delete(c.CTX, pod.ObjectMeta.Name, metav1.DeleteOptions{})

	} else {
		c.Logger.Sugar().Warnf("can not delete pod name is : %v its not bleong to podcontroller", pod.Name)
		return nil
	}

}
