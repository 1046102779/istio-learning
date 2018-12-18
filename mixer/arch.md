# mixer server启动流程

## 本地启动并简单测试
```shell
# 本地启动mixer server instance的命令，如下所示：
mixs server --configStoreURL=fs://$GOPATH/src/istio.io/istio/mixer/testdata/config

grpc server的端口：9091

# 我们使用mix client命令，试图发送几个命令给mix server，命令如下：check、report
mixc check --string_attributes destination.service=abc.ns.svc.cluster.local,source.name=myservice,destination.port=8080 --stringmap_attributes "request.headers=clnt:abcd;source:abcd,destination.labels=app:ratings,source.labels=version:v2"   --timestamp_attributes request.time="2017-07-04T00:01:10Z" --bytes_attributes source.ip=c0:0:0:2

返回结果如下：
Check RPC completed successfully. Check status was OK
  Valid use count: 10000, valid duration: 5m0s
  Referenced Attributes
    context.reporter.kind ABSENCE
    destination.labels::app EXACT
    destination.namespace ABSENCE
    destination.service EXACT
    request.headers::clnt EXACT
    source.labels::version EXACT

mixc report --string_attributes destination.service=abc.ns.svc.cluster.local,source.name=myservice,destination.port=8080 --stringmap_attributes "request.headers=clnt:abc;source:abcd,destination.labels=app:ratings,source.labels=version:v2"  --int64_attributes response.duration=2003,response.size=1024 --timestamp_attributes  request.time="2017-07-04T00:01:10Z" --bytes_attributes source.ip=c0:0:0:2

返回结果如下：
Report RPC returned OK
```


## 分析mixer server启动流程

当使用mixs server命令时，启动流j程大体如下：

1. 获取mixer内置的templates与adapters；
2. 对mixer server相关参数进行默认设置和命令行传入更新；如：grpc提供服务的默认端口或者地址为9091，可支持命令行修改；mixer自身健康状态的监控端口为9093，可支持命令行修改; grpc一次可传输的最大数据量，并发量; mixer server同时最多处理的goroutines数量；adapter能够同时处理的goroutines数量等等参数。主要是用于接收grpc client的请求相关参数、以及处理这些请求的goroutines池、和后端handler的goroutines池。还有一个很重要的参数，就是指定存储adapter配置文件的位置，可以是k8s路径、fs路径等，启动mixer server时会自动加载这些配置到内存中，并通过VirtualService、Template和DestinationRule进行固定属性和数据的匹配、处理并存储；`这些参数作为启动参数传入mixer server中`
3. 然后执行runServer方法，开始初始化grpc server服务、mixer server自身健康状态的监控。 并分别启动两个goroutine，主线程用grpc server的Wait进行阻塞。第二goroutine这是我们通常说的"net/http/pprof"性能监控

对于流程中的第3步骤，过程比较复杂。

### 启动mixs server的详细流程

#### 第一步

对于第1步，文件`$GOPATH/src/istio.io/istio/mixer/template/inventory.yaml`表示内置了12个adapter，分别是`apikey`, `authorization`, `checknothing`, `edge`, `listentry`, `logentry`, `metric`, `quota`, `reportnothing`, `tracespan`, `servicecontrolreport`, `kubernetesenv/template`

在`$GOPATH/src/istio.io/istio/mixer/template`很多文件都是通过`tools/mixgen`、`tools/codegen`等命令生成的，它是adapter各个后端实例对接的抽象API接口，每一个adapter包括了Handler interface、Template后的Instance数据模型以及HandlerBuilder


首先要说明一点，一个adapter可以对应多个template，例如：一个为stdio的适配器，有两个template，分别是：logentry和metric。 

```shell
## logentry template
type Handler interface{
	adapter.Handler
	HandleLogEntry(context.Context, []*Instance) error
}

## metric template
type Handler interface{
	adapter.Handler
	HandlerMetric(context.Context, []*Instance) error
}
```

则对于stdio适配器，对外提供了两个服务，一个是HandleLogEntry，另一个是HandleMetric，协议是GRPC。

内置的adapter包括：`bypass`, `circonus`, `cloudwatch`, `denier`, `dogstatsd`, `fluentd`, `kubernetesenv`, `list`, `memquota`, `noop`, `opa`, `prometheus`, `rbac`, `redisquota`, `servicecontrol`, `signalfx`, `solarwinds`, `stackdriver`, `statsd`和`stdio`共20个adapter。

目前这20个adapter对应的template，由下表构成：

| adapter name| templates|
|---|---|
|bypass | checknothing, reportnothing, metric, quota|
|circonus | metric |
|cloudwatch | metric | 
|denier | checknothing, listentry, quota |
|dogstatsd | metric |
|fluentd| logentry |
| kubernetesenv | ktmpl |
| list | listentry|
| memquota| quota |
| noop | authorization, checknothing, reportnothing, listentry, logentry, metric, quota, tracespan|
| opa | authorization|
|prometheus|metric|
|rbac | authorization|
| redisquota| quota|
|servicecontrol| apikey, servicecontrolreport, quota |
| signalfx| metric, tracespan |
| solarwinds| metric, logentry|
|stackdriver|edgepb, logentry, metric, tracespan|
|statsd| metric|
|stdio | metric, logentry|

#### 第二步

启动mixs server，执行runServer函数。

首先搞清楚Server实例的所有参数含义：

|参数名称 | 参数类型 | 参数含义 |
| --- | --- | --- |
| shutdown | chan error | 接收外部信号中断server|
| server | `*grpc.Server` | 接收envoy proxy的grpc服务请求，包括check、quota和report |
| gp | `*pool.GoroutinePool` | 当grpc server处理来自envoy proxy的请求时，最大并发量控制，也就是goroutine池 |
| adapterGP| `*pool.GoroutinePool` | 一个后端adapter最多同时能够处理的请求数量，也就是goroutine池 |
| listener | net.Listener | 用于监听envoy proxy发送过来的grpc client请求，与上面的参数server一同使用 |
| monitor | `*monitor` | mixer server自身健康状态监控，也包括内存、CPU等占用， 使用的是net/http/pprof包 |
| tracer | io.Closer | 这个稍后再看看... |
| checkCache |  `*checkcache.Cache` | mixer server二级缓存 ，第一级缓存在envoy sidecar中|
| dispatcher | dispatcher.Dispatcher | mixer server接收grpc client请求后的分发，也即把这个请求给到合适的后端adapter处理，不过这里面非常复杂, 后面重点讲解 |
| controlZ | `*ctrlz.Server` | mixer server控制台UI，包括：Logging Scopes、Memory Usage、Environment Variables、ProcessInfo、Command-Lin Arguments、Version Info和Metrics相关信息的控制和服务自身参数和内存使用量监控情况。如果本地启动一个mixer server，则通过**127.0.0.1:9876**可以访问到该服务 |
|livenessProbe | probe.Controller | 探针, 探活, 这后面三个参数后面再看... |
| readinessProbe | probe.Controller | 
| `*probe.Probe` | - |  

下面展示了controlz通过UI查看当前mix server的状态:

![controlz](https://gewuwei.oss-cn-shanghai.aliyuncs.com/tracelearning/mixs-controlz.jpeg)


另一个方面patchTable参数, 用于故障注入的可替换功能集。当我们要对集群中的某些服务进行异常注入测试，看看业务的反馈状态做出业务调整。可替换的函数如下表所示：

| 函数名 | 函数原型 | 函数含义 |
| ---| --- | --- |
| newRuntime | `func(store.Store, map[string]*template.Info, map[string]*adpater.Info, string, *pool.GoroutinePool..., bool) *runtime.Runtime`| Runtime是Mixer server运行时的主入口。 它监听配置，实例化handler instance，创建dispatch分发状态机并处理grpc client的请求。 |
| configTracing | `func(serviceName string, options *tracing.Options) (io.Closer, error)` | 配置并初始化全局的GlobalTracer，目前内置支持Jaeger与Zipkin，因为Jaeger本身就支持Zipkin |
| startMonitor | `func(port uint16, enableProfiling bool, lf listenFunc) (*monitor, error)` | 启动mixer server profiling性能监控，使用的net/http/profiling包 |
| listen | `listenFunc` | 默认使用net.Listen监听grpc server |
| configLog | `func(options *log.Options) error` | 同configTracing，配置全局的log文件写入对象 |
| runtimeListen| `func(runtime *runtime.Runtime) error` | 默认使用runtime.StartMonitor方法进行配置监控，当配置变化时，重新配置runtime运行时的环境。配置包括：VirtualService、Template和DestinationRule |
| remove | `func(name string) error ` | 默认os.Remove方法，移除fs的指定文件 |
| newOpenCensusExporter | `func() (view.Exporter, error)` | Prometheus metrics导出到OpenCensus|
| registerOpenCensusViews | `func(...*view.View) error` | 注册OpenCensus， 我觉得使用Jaeger就够了，为什么要这么多监控系统呢？Jaeger、Zipkin和OpenCensus|



### 第三步

启动mixer server，执行runServer函数。这个流程又分为N步骤：

第一步：

## 分析mixer grpc服务调用流程
