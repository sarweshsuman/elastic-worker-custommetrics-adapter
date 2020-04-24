package provider

import (
    "time"
    "fmt"
    "sync"
    "strconv"
    "strings"

    types "k8s.io/apimachinery/pkg/types"
    "k8s.io/klog"

    apimeta "k8s.io/apimachinery/pkg/api/meta"
    apierr "k8s.io/apimachinery/pkg/api/errors"
    "k8s.io/apimachinery/pkg/api/resource"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/labels"
    "k8s.io/apimachinery/pkg/runtime/schema"
    "k8s.io/client-go/dynamic"
    "k8s.io/metrics/pkg/apis/custom_metrics"
    "k8s.io/apimachinery/pkg/util/wait"
    utilruntime "k8s.io/apimachinery/pkg/util/runtime"
    redis "github.com/go-redis/redis/v7"

    "github.com/kubernetes-incubator/custom-metrics-apiserver/pkg/provider"
    "github.com/kubernetes-incubator/custom-metrics-apiserver/pkg/provider/helpers"
)

var (
  updateInterval = 5 * time.Second
)

// Runnable represents something that can be run until told to stop.
type Runnable interface {
	// Run runs the runnable forever.
	Run()
	// RunUntil runs the runnable until the given channel is closed.
	RunUntil(stopChan <-chan struct{})
}

type ElasticWorkerMetricsProvider struct {
    client dynamic.Interface
    mapper apimeta.RESTMapper
    redisClient *redis.Client

    metrics map[provider.CustomMetricInfo]bool
    registeredMetrics []provider.CustomMetricInfo

    // For updating metrics list.
    mu sync.RWMutex
}

func NewElasticWorkerMetricsProvider(client dynamic.Interface, mapper apimeta.RESTMapper, redisConfig RedisConfigs) (*ElasticWorkerMetricsProvider) {
  redisConnectionAddr := strings.Join([]string{redisConfig.Hostname,redisConfig.Port},":")
  redisClient := redis.NewClient(&redis.Options{
		Addr:     redisConnectionAddr,
		Password: redisConfig.Password,
		DB:       redisConfig.Database,
	})
  _, err := redisClient.Ping().Result()
  if err != nil {
    klog.Fatalf("Unable to connect to redis metric source db: %v",err)
  }

	myprovider := &ElasticWorkerMetricsProvider{
		client:          client,
		mapper:          mapper,
    redisClient:     redisClient,
	}
	return myprovider
}

func (myprovider *ElasticWorkerMetricsProvider) Run() {
	myprovider.RunUntil(wait.NeverStop)
}

func (myprovider *ElasticWorkerMetricsProvider) RunUntil(stopChan <-chan struct{}) {
	go wait.Until(func() {
		if err := myprovider.updateMetrics(); err != nil {
			utilruntime.HandleError(err)
		}
	}, updateInterval, stopChan)
}

func (myprovider *ElasticWorkerMetricsProvider) updateMetrics() error {
  myprovider.mu.Lock()
  defer myprovider.mu.Unlock()

  metricList,err := myprovider.LRange()
  if err != nil {
    klog.Errorf("error in retrieving metric list: %v", err)
    return err
  }
  // reset existing map
  myprovider.metrics = make(map[provider.CustomMetricInfo]bool)
  myprovider.registeredMetrics = []provider.CustomMetricInfo{}

  for idx := range metricList {
    /* TODO: extend support for other type of reseource, currently only pods are supported */
    metric := provider.CustomMetricInfo{
            GroupResource: schema.GroupResource{Group: "", Resource: "pods"},
            Metric:        metricList[idx],
            Namespaced:    true,
        }
    _ , alreadyExists := myprovider.metrics[metric]
    if !alreadyExists {
      myprovider.metrics[metric] = true
      myprovider.registeredMetrics = append(myprovider.registeredMetrics,metric)
    }
  }
  return nil
}

func (myprovider *ElasticWorkerMetricsProvider) ListAllMetrics() []provider.CustomMetricInfo {
    return myprovider.registeredMetrics
}

// TODO: current implementation skips metricSelector use.
// CustomMetricInfo will contain registered MetricName like load, total_cluster_load
// In redis, metrics are stored with name elasticclustermetric_<namespace>_<resourcename>_<metricname>
// and this name will be created when request is received.
func (myprovider *ElasticWorkerMetricsProvider) GetMetricByName(name types.NamespacedName, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValue, error) {
    klog.Infof("Attempt to retrieve metric: %v for resource: %v with metricSelector: %v",info, name, metricSelector)
    value, err := myprovider.valueFor(info, name.Namespace, name.Name)
    if err != nil {
      return nil, err
    }
    return myprovider.metricFor(value, name, info)
}

// TODO: current implementation skips metricSelector use.
func (myprovider *ElasticWorkerMetricsProvider) GetMetricBySelector(namespace string, selector labels.Selector, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValueList, error) {
    klog.Infof("Attempt to retrieve metric: %v for resources in namespace: %v and resource selector: %v", info, namespace, selector)

    resourceNames, err := helpers.ListObjectNames(myprovider.mapper, myprovider.client, namespace, selector, info)
  	if err != nil {
  		klog.Errorf("unable to list matching resource names: %v", err)
  		return nil, apierr.NewInternalError(fmt.Errorf("unable to list matching resources"))
  	}

    metrics := []custom_metrics.MetricValue{}
    for idx := range resourceNames {
        value, err := myprovider.valueFor(info, namespace, resourceNames[idx])
        if err != nil {
          klog.Errorf("unable to retrieve metrics: %v", err)
          return nil, err
        }
        metricValue, err := myprovider.metricFor(value, types.NamespacedName{Namespace: namespace, Name: resourceNames[idx]} , info)
        if err != nil {
      		klog.Errorf("unable to convert raw metric: %v to metricValue: %v", value, err)
      		return nil, apierr.NewInternalError(fmt.Errorf("unable to convert raw metric: %v to metricValue: %v", value, err))
      	}
        metrics = append(metrics, *metricValue)
    }

    return &custom_metrics.MetricValueList{
        Items: metrics,
    }, nil
}

func (myprovider *ElasticWorkerMetricsProvider) valueFor(info provider.CustomMetricInfo, namespace string, resourceName string) (string, error) {
    // normalize the value so that you treat plural resources and singular
    // resources the same (e.g. pods vs pod)
    info, _, err := info.Normalized(myprovider.mapper)
    if err != nil {
        klog.Errorf("error in normalization of the metric: %v error:%v", info, err)
        return "", err
    }

    _, infoFound := myprovider.metrics[info]
    if !infoFound {
      klog.Errorf("metric %v not found in registered metric list", info)
      return "", provider.NewMetricNotFoundError(info.GroupResource, info.Metric)
    }

    redisMetricName := buildMetricKey(info.Metric, namespace, resourceName)
    klog.Infof("redisMetricName:%v for metric:%v resource:%v namespace: %v", redisMetricName, info, resourceName, namespace)

    val, err := myprovider.get(redisMetricName)
    if err != nil {
      klog.Errorf("unable to fetch redisMetricName: %v from redis: %v", redisMetricName, err)
      return "", apierr.NewInternalError(fmt.Errorf("unable to fetch metrics"))
    }

    return val, nil
}

// metricFor constructs a result for a single metric value.
func (myprovider *ElasticWorkerMetricsProvider) metricFor(value string, name types.NamespacedName, info provider.CustomMetricInfo) (*custom_metrics.MetricValue, error) {
    // construct a reference referring to the described object
    objRef, err := helpers.ReferenceFor(myprovider.mapper, name, info)
    if err != nil {
        klog.Errorf("error in building reference object: %v", err)
        return nil, err
    }

    currentValue, err := strconv.Atoi(value)
    if err != nil {
      klog.Errorf("error in converting string:%v to int for metric value error: %v", value, err)
      return nil, err
    }

    return &custom_metrics.MetricValue{
        DescribedObject: objRef,
        // skipping selector info fill
        Metric:  	      custom_metrics.MetricIdentifier{Name:info.Metric},
        // TODO: fetch timestamp from redis, currently using a dummy value
        Timestamp:       metav1.Time{time.Now()},
        // TODO: support different types of metric value, currently only load percentage(0-100) is supported
        Value:           *resource.NewQuantity(int64(currentValue), "PercentageLoad"),
    }, nil
}
