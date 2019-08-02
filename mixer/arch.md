# mixer server框架

![mixer数据处理总体流程](https://gewuwei.oss-cn-shanghai.aliyuncs.com/tracelearning/mixer%E6%95%B0%E6%8D%AE%E5%A4%84%E7%90%86%E6%80%BB%E4%BD%93%E6%B5%81%E7%A8%8B.png)

![envoy proxy调用mixer server调用逻辑](https://www.baidu.com)

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
2. 对mixer server相关参数进行默认设置和命令行传入更新；如：grpc提供服务的默认端口或者地址为9091，可支持命令行修改；mixer自身健康状态的监控端口为9093，可支持命令行修改; grpc一次可传输的最大数据量，并发量; mixer server同时最多处理的goroutines数量；adapter能够同时处理的goroutines数量等等参数。主要是用于接收grpc client的请求相关参数、以及处理这些请求的goroutines池、和后端handler的goroutines池。还有一个很重要的参数，就是指定存储adapter配置文件的位置，可以是k8s路径、fs路径等，启动mixer server时会自动加载这些配置到内存中，并通过Handler、Template和DestinationRule进行固定属性和数据的匹配、处理并存储；`这些参数作为启动参数传入mixer server中`
3. 然后执行runServer方法，开始初始化grpc server服务、mixer server自身健康状态的监控。 并分别启动两个goroutine，主线程用grpc server的Wait进行阻塞。第二goroutine这是我们通常说的"net/http/pprof"性能监控

对于流程中的第3步骤，过程比较复杂。

### 启动mixs server的详细流程

#### 第一步

对于第1步，文件`$GOPATH/src/istio.io/istio/mixer/template/inventory.yaml`表示内置了12个template，分别是`apikey`, `authorization`, `checknothing`, `edge`, `listentry`, `logentry`, `metric`, `quota`, `reportnothing`, `tracespan`, `servicecontrolreport`, `kubernetesenv/template`

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

| 函数名 | 函数原型 | 函数含义及默认值 |
| ---| --- | --- |
| newRuntime | `func(store.Store, map[string]*template.Info, map[string]*adpater.Info, string, *pool.GoroutinePool..., bool) *runtime.Runtime`| Runtime是Mixer server运行时的主入口。 它监听配置，实例化handler instance，创建dispatch分发状态机并处理grpc client的请求。 |
| configTracing | `func(serviceName string, options *tracing.Options) (io.Closer, error)` | 配置并初始化全局的GlobalTracer，目前内置支持Jaeger与Zipkin，因为Jaeger本身就支持Zipkin |
| startMonitor | `func(port uint16, enableProfiling bool, lf listenFunc) (*monitor, error)` | 启动mixer server profiling性能监控，使用的net/http/profiling包 |
| listen | `listenFunc` | 默认使用net.Listen监听grpc server |
| configLog | `func(options *log.Options) error` | 同configTracing，配置全局的log文件写入对象 |
| runtimeListen| `func(runtime *runtime.Runtime) error` | 默认使用runtime.StartMonitor方法进行配置监控，当配置变化时，重新配置runtime运行时的环境。配置包括：Handler、Template和DestinationRule |
| remove | `func(name string) error ` | 默认os.Remove方法，移除fs的指定文件 |
| newOpenCensusExporter | `func() (view.Exporter, error)` | Prometheus metrics导出到OpenCensus|
| registerOpenCensusViews | `func(...*view.View) error` | 注册OpenCensus， 我觉得使用Jaeger就够了，为什么要这么多监控系统呢？Jaeger、Zipkin和OpenCensus|



### 第三步

启动mixer server，执行runServer函数。这个流程又分为N步骤：

第一步：初始化patchTable，并创建mixer Server实例;
第二步：启动mixer server服务，监听grpc服务端口；
第三步：主线程goroutine等待mixer server服务终止信号，并关闭grpc服务.

对于第二步，主要是监听来自envoy proxy的grpc client请求，注册的服务有Check与Report。其中Qutoa包含在Check中

对于第三步，mixer server主要用于grpc server对envoy proxy提供Check、Qutoa和Report服务，当mixer server终止时，则主进程也需要做退出后的处理，并Close掉。包括以下步骤

1. grpc server的优雅关闭。Graceful Stop。
2. controlz服务退出，也就是mixer server自身状态UI监控，非性能监控；
3. checkCache服务退出，是mixer server的第二级缓存；
4. grpc server服务关闭退出；
5. tracer client关闭退出
6. monitor server服务退出，这个是profiling mixer server自身性能采样；
7. 两个goroutine池关闭，包括接收grpc client请求的并发goroutine池和后端adapter处理的并发goroutine池；
8. probe探活关闭
9. log sync内存中的日志buff落入磁盘；

#### mixer server实例初始化

这里重点讲解第一步mixer server实例的整个创建过程，比较复杂。

patchTable故障注入可替换的默认值，已经在上面的表格中说明了。

先初始化Server实例，根据上面说的Server各个参数定义以及含义，设置grpc server并发处理的goroutine数量，以及后端adapter
并发处理的goroutine数量。这里比较有意思的一点，自己对此有点看法(APIWorkerPool, AdapterWorkerPool)：

```shell
# 对于goroutine池，并发处理grpc client请求。采用了channel与goroutine pool池，其实官方处理得过于简单了。
# 所有的goroutine作为worker，从channel队列上抢占每个发送过来的grpc client请求，并进行任务处理。
# 官方给出的思路：当channel队列打满时，保证每个队列中的每个任务，都能被一个goroutine服务。也就是说channel队列长度与pool中的goroutine数量是相同的
# 但是我认为这个是不太合适的。因为channel队列的长度不打满的话，则多个goroutine一定会出现idle状态，应该是要给出算法动态调整goroutine数量的，或者给出两个参数：
# 1. channel队列的长度；2. goroutine并发数量
```

对于mixer server内置的12个templates和20个adapter, 先是对这两个进行一些处理， 代码如下所示：


```shell
## 对内置的templates和adapters，进行一些提取和关联, 这个部分涉及的概念比较多，我们逐一改写
tmplRepo := template.NewRepository(a.Templates) 
adapterMap := config.AdapterInfoMap(a.Adapters, tmplRepo.SupportsTemplate)
```

虽然短短的两行代码，但是涉及的概念和内容非常多，我们下面详细介绍下：

首先对于NewRepository方法，创建一个Repository interface实例，这个接口实现了两个方法

```shell
// Repository defines all the helper functions to access the generated template specific types and fields.
Repository interface {
    GetTemplateInfo(template string) (Info, bool)
    SupportsTemplate(hndlrBuilder adapter.HandlerBuilder, tmpl string) (bool, string)
}
```

对于GetTemplateInfo方法，也就是对mixer server内置的12个templates，即map[string]template.Info的SupportedTmplInfo。通过模板名，获取template.Info，也就是模板相关信息。

在介绍第二个方法SupportsTemplate之前，我们先挖掘几个template和adapter小概念。这里有个[issue1](https://github.com/1046102779/istio-learning/issues/1), 可以看看了解下。

> 总体思路：该方法把templates与adapters联系起来了，并且校验每个adapter是否实现了自己指定的SupportedTemplates列表，校验方法：adapter的Builder是否实现了template定义的HandlerBuilder, 如果没有存在没有实现的adapter，则直接panic。

具体见：[templates与adapters的关系校验](adapters-and-templates.md)

清楚adapters与templates的关系后，接下来就出现了下面一行代码：

> s.Probe = probe.NewProbe()

上面一行代码初始化探针probe，用于探活和就绪。在继续往下初始化mixer server实例之前，我们先了解一个命令

> mixs probe --probe-path="xxxx" --interval="10s"

这个命令的含义是：在10s内，如果这个probe-path指定的文件最后修改时间t1, 当前时间为t2, 如果t2-t1大于10s，则表示探活失败；否则探活成功。

也就是说，如果在规定时间内，文件没有发生过修改，则表示不健康。

在mixer server启动参数中，也需要指明Readiness probe和Liveness probe两个探针的配置，包括文件绝对路径和探测间隔时间。需要说明的一点是，这个探测间隔时间，官方写的是输入interval时间的一半，他们认为进行检测也需要消耗大量时间。

这两个探针的意义也在上面已有说明，来自于k8s的概念


接下来，就是grpc server相关参数设置。包括grpc server最大并发流量限制、接收的最大请求包限制、是否开启trace、以及grpc server启动需要的IP和端口

参数设置完后，创建一个监听。默认使用的是net.Listen, 监听存储在Server下的listener参数值中。

然后再通过启动参数ConfigStoreUrl，设置要开启的后端适配器列表，以及对应Template和DestinationRule配置文件，这个url可以是k8s的访问路径，[MCP网格配置协议](https://blog.fleeto.us/post/battle-testing-istio-1.0/), 也可以是fs的本地路径，也可以是其他路径。

[mixer server后端配置存储服务](backend-store.md)

接下来，我们就要讨论比较关键的一步了，mixer server runtime模块，这个是监听后端存储配置的变化，以及接收envoy proxy的grpc client请求并分发给后端适配器处理。这个过程非常复杂，我们先来看Runtime的初始化，在mixer server实例初始化有关这个runtime的初始化，由下面几行代码构成：

```shell
log.Info("Starting runtime config watch...")
rt = p.newRuntime(st, templateMap, adapterMap, a.ConfigDefaultNamespace,
    s.gp, s.adapterGP, a.TracingOptions.TracingEnabled())

if err = p.runtimeListen(rt); err != nil {
    return nil, fmt.Errorf("unable to listen: %v", err)
}

s.dispatcher = rt.Dispatcher()
```

在看上面的代码之前，你可以阅读[runtime初始化运行时](runtime.md)

## 分析mixer grpc服务调用流程
