package provider

import (
  "fmt"
  "k8s.io/klog"
)

type RedisConfigs struct {
  Hostname string
  Port string
  Database int
  Password string
}

var (
  metricRegistryListName = "elasticclustermetric_registered_metric"
  metricKeyFormat = "elasticclustermetric_%s_%s_%s"
)

func (myprovider *ElasticWorkerMetricsProvider) LRange() ([]string, error) {
  list, err := myprovider.redisClient.LRange(metricRegistryListName,0,-1).Result()
  if err != nil {
    klog.Errorf("error in retreiving registerd metric list from redis: %v",err)
    return []string{}, err
  }
  return list, nil
}

/* Not Needed, TODO: remove
func (myprovider *ElasticWorkerMetricsProvider) keys() ([]string, error) {
  keys, err := myprovider.redisClient.Keys(defaultMetricKeyPrefix).Result()
  if err != nil {
    klog.Errorf("error in retreiving metrics list from redis: %v",err)
    return []string{}, err
  }
  return keys, nil
}
*/

func (myprovider *ElasticWorkerMetricsProvider) get(key string) (string, error) {
  val, err := myprovider.redisClient.Get(key).Result()
  if err != nil {
    klog.Errorf("error in retreiving metric: %v from redis: %v",key, err)
    return "",err
  }
  return val, nil
}

func buildMetricKey(metricName string, namespace string, resourceName string) string {
  return fmt.Sprintf(metricKeyFormat,namespace,resourceName,metricName)
}
