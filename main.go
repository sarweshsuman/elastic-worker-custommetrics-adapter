package main

import (
    "flag"
    "os"

    "k8s.io/klog"
    "k8s.io/apimachinery/pkg/util/wait"
    "k8s.io/component-base/logs"

    basecmd "github.com/kubernetes-incubator/custom-metrics-apiserver/pkg/cmd"
    "github.com/kubernetes-incubator/custom-metrics-apiserver/pkg/provider"
    myprovider "github.com/sarweshsuman/elastic-worker-custommetrics-adapter/pkg/provider"
)

type ElasticWorkerMetricsAdapter struct {
    basecmd.AdapterBase
    myprovider.RedisConfigs
    Message string
}

func main() {
    logs.InitLogs()
    defer logs.FlushLogs()

    cmd := &ElasticWorkerMetricsAdapter{}
    cmd.Flags().StringVar(&cmd.Message, "msg", "starting ElasticWorkerMetrics adapter...", "startup message")
    cmd.Flags().StringVar(&cmd.Hostname, "redishost", "localhost", "redis hostname")
    cmd.Flags().StringVar(&cmd.Port, "redisport", "6379", "redis port")
    cmd.Flags().IntVar(&cmd.Database, "redisdb", 0, "Redis database")
    cmd.Flags().StringVar(&cmd.Password, "redispassword", "", "Redis password")
    cmd.Flags().AddGoFlagSet(flag.CommandLine) // make sure you get the klog flags
    cmd.Flags().Parse(os.Args)

    providerInstance := cmd.makeProviderOrDie()
    cmd.WithCustomMetrics(providerInstance)

    klog.Infof(cmd.Message)
    if err := cmd.Run(wait.NeverStop); err != nil {
        klog.Fatalf("unable to run custom metrics adapter: %v", err)
    }
}

func (adapter *ElasticWorkerMetricsAdapter) makeProviderOrDie() provider.CustomMetricsProvider {
    client, err := adapter.DynamicClient()
    if err != nil {
        klog.Fatalf("unable to construct dynamic client: %v", err)
    }

    mapper, err := adapter.RESTMapper()
    if err != nil {
        klog.Fatalf("unable to construct discovery REST mapper: %v", err)
    }

    providerInstance := myprovider.NewElasticWorkerMetricsProvider(client, mapper, adapter.RedisConfigs)
    providerInstance.Run()

    return providerInstance
}
