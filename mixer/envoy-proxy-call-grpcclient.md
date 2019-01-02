# 分析mixer grpc服务调用流程

当mixer server服务初始化完成，已经准备好对外提供grpc server服务后，包括Check与Report服务。

我们看一下, mixc发送report请求，在mixer server端接收到的数据：

客户端请求：
> mixc report --string_attributes destination.service=abc.ns.svc.cluster.local,source.name=myservice,destination.port=8080 --stringmap_attributes "request.headers=clnt:abc;source:abcd,destination.labels=app:ratings,source.labels=version:v2"  --int64_attributes response.duration=2003,response.size=1024 --timestamp_attributes  request.time="2017-07-04T00:01:10Z" --bytes_attributes source.ip=c0:0:0:2

> 服务端接收数据：

```shell
attributes: [
	{
		[
			destination.labels app ratings source.name myservice destination.port 8080 request.time response.size request.headers clnt abc source abcd destination.service abc.ns.svc.cluster.local source.labels version v2 response.duration source.ip
		] 
		map[-4:-5 -6:-7 -15:-16] 
		map[-9:1024 -20:2003] 
		map[] 
		map[] 
		map[-8:2017-07-04 00:01:10 +0000 UTC] 
		map[]
		map[-21:[192 0 0 2]] 
		map[-1:{map[-2:-3]} -10:{map[-11:-12 -13:-14]} -17:{map[-18:-19]}]
	}
]

default words: [
	destination.service source.name destination.port response.duration response.size request.time source.ip request.headers destination.labels source.labels
]

global word count: 0
```

我们对上面的attributes列表数据说明下含义:

通过attributes列表可以看到，istio支持envoy proxy本地缓存并批量构成一个grpc client请求。降低网络传输量,降低负载

对于attributes列表的每个元素, 第一个为发送过来的所有字段与数据构成的列表，其他的变量是对第一个列表的解析key-value；key为第一个列表字段索引的取反，再减1

Words []string 		                [
			destination.labels app ratings source.name myservice destination.port 8080 request.time response.size request.headers clnt abc source abcd destination.service abc.ns.svc.cluster.local source.labels version v2 response.duration source.ip
		] 
Strings map[int32]int32		        map[-4:-5 -6:-7 -15:-16] 
Int64S map[int32]int64		        map[-9:1024 -20:2003] 
Doubles map[int32]float64		    map[] 
Bools map[int32]bool		        map[] 
Timestamps map[int32]time.Time		map[-8:2017-07-04 00:01:10 +0000 UTC] 
Durations map[int32]time.Duration   map[]
Bytes map[int32][]byte		        map[-21:[192 0 0 2]] 
StringMaps map[int32]StringMap		map[-1:{map[-2:-3]} -10:{map[-11:-12 -13:-14]} -17:{map[-18:-19]}]

例如：destination.labels的索引key等于-1, app的索引key等于-2, ratings的索引key等于-3

1. 对于Strings类型的map，实际值：{source.name: "myservice", destination.port:"8080", destination.service:"abc.ns.svc.cluster.local"}
2. 对于Int64S类型的map，实际值：{response.size: 1024, response.duration: 2003}
3. 对于Doubles类型的map，实际值：{}
4. 对于Bools类型的map，实际值：{}
5. 对于Timestamps类型的map，实际值：{request.time: "2017-07-04 00:01:10 +0000 UTC"}
6. 对于Durations类型的map，实际值：{}
7. 对于Bytes类型的map，实际值：{source.ip: 192.0.0.2}
8. 对于StringMaps类型的map，实际值：{destination.labels: {app: ratings}, response.size: {request.headers: clnt, abc:source}, source.labels: {version:v2}}

下面我们先以grpc server中的Report服务为例，介绍envoy proxy发送grpc client请求，被grpc server处理的整体流程。

## Report处理grpc client请求流程

当mixer server接收grpc client report请求后，每个包数据都需要经过两个分发处理。
1. Preprocess处理，被variety为TEMPLATE_VARIETY_ATTRIBUTE_GENERATOR的template处理；
2. Report处理，真正处理上报数据的部分。

对于dispatcher，都是通用的分发处理流程，它需要用到mixer server初始化实例的全局路由表。然后最终分发给后端适配器的grpc client，给到远端服务。

对于dispatcher分发的请求，为了数据包之间的隔离性，在发送数据包给路由表之前，需要创建一个session，这个session使用sync.Pool，增加内存复用。

在接收到grpc client的请求数据包后，在第一次分发给路由表之前，都需要先转发给variety为TEMPLATE_VARIETY_ATTRIBUTE_GENERATOR的路由表先处理下；然后再把处理后的数据包给转发给variety={TEMPLATE_VARIETY_REPORT，TEMPLATE_VARIETY_CHECK, TEMPLATE_VARIETY_QUOTA}的路由表，具体看grpc client的请求服务名。

grpc server接收envoy proxy的grpc client请求的主逻辑，如下所示：

envoy proxy发送的grpc client report请求，首先为variety等于TEMPLATE_VARIETY_ATTRIBUTE_GENERATOR的后端适配器任务处理；然后再variety等于TEMPLATE_VARIETY_REPORT的后端适配器任务处理

```shell
func (s *grpcServer) Report(ctx context.Context, req *mixerpb.ReportRequest) (*mixerpb.ReportResponse, error) {
	......
	// 原始数据包交给variety为TEMPLATE_VARIETY_ATTRIBUTE_GENERATOR的子路由表处理，处理后的数据返回reportBag
	if err := s.dispatcher.Preprocess(newctx, accumBag, reportBag); err != nil {
		......
	}
	// 对处理后的数据，作为variety={TEMPLATE_VARIETY_REPORT，TEMPLATE_VARIETY_CHECK, TEMPLATE_VARIETY_QUOTA}的数据，并进行第二次处理上报
	if err := reporter.Report(reportBag); err != nil {
		......
	}

	return reportResp, nil
}
```

### Preprocess与Report处理

在`$GOPATH/src/istio.io/istio/mixer/pkg/dispatcher/dispatcher.go`文件中，先通过Preprocess方法分发一个variety为TEMPLATE_VARIETY_ATTRIBUTE_GENERATOR的路由表。首先构建一个独立临时可复用的session，然后再通过session dispatch方法分发，主要是通过dispatcher中的全局路由表，形成路由路径。

```shell
func (d *Impl) Preprocess(ctx context.Context, bag attribute.Bag, responseBag *attribute.MutableBag) error {
	s := d.getSession(ctx, tpb.TEMPLATE_VARIETY_ATTRIBUTE_GENERATOR, bag)
	
	// 注意，输入的responseBag，作为输出返回值
	s.responseBag = responseBag
	// session中的dispatch，是全局路由表，所有的数据流都需要通过这张路由表，找到后端适配器的grpc client service，发起调用。
	// 所以这个方法是通用方法。所有的variety，都要经过
	err := s.dispatch()
	d.putSession(s)
	return err
}
```

接下来，我们看看路由表, 核心路由方法

```shell
// dispatch方法，核心有三个for循环，如果我们理解mixer server初始化过程中runtime的建立，这个就很好理解了。
func (s *session) dispatch() error {
	// 获取namespace，具体步骤：
	//  1. 通过固定属性词汇"context.reporter.kind", 获取标签属性值：inbound，outbound
	//  2. 如果为inbound，表示流入，因为是上报，所以是业务处理完成后，也就是在目标服务处理完业务请求后，通过envoy proxy返回时上报，那么这个namespace就是destination.namespace; 
	//  3. 如果为outbound，表示流出，这个是业务客户端的envoy proxy发起的调用，那么这个namespace就是source.namespace；
	// 注意：destination.namespace与source.namespace都是固定属性词汇
	namespace := getIdentityNamespace(s.bag)
	// 首先通过session请求数据包的variety与namespace，获取后端适配器的处理列表
	// 这个首先通过variety获取指定服务类型的所有实例，然后再通过namespace，在得到一个后端适配器处理列表NamespaceTable
	destinations := s.rc.Routes.GetDestinations(s.variety, namespace)
	
	// 遍历每个后端适配器
	// 需要注意的是，这里会对所有的包variety进行统一处理，所以感觉逻辑会有点混乱。
	// variety={TEMPLATE_VARIETY_REPORT, TEMPLATE_VARIETY_ATTRIBUTE_GENERATOR, TEMPLATE_VARIETY_QUOTA}
	// 上面三个variety融合在一起
	for _, destination := range destinations.Entries() {
		var state *dispatchState
		
		// 首先如果variety为TEMPLATE_VARIETY_REPORT, 则初始化state，并通过handler找到dispatchState, 这里面包含了执行任务所需要的全部数据。
		if s.variety == tpb.TEMPLATE_VARIETY_REPORT {
            // We buffer states for report calls and dispatch them later
            state = s.reportStates[destination]
            if state == nil {
                state = s.impl.getDispatchState(s.ctx, destination)
                s.reportStates[destination] = state
            }
       }
       
		for _, group := range destination.InstanceGroups {
			// 校验后端适配器是否能够接收这个数据，这个是通过DestinationRule中spec的match进行条件判定。
			// 如果不匹配，则我们不会处理这个数据包
			groupMatched := group.Matches(s.bag)
			
			for j, input := range group.Builders {
				// 即使match不匹配，如果variety为TEMPLATE_VARIETY_QUOTA, 也需要给一个默认的配额结果
				if s.variety == tpb.TEMPLATE_VARIETY_QUOTA {
                // only dispatch instances with a matching name
                if !strings.EqualFold(input.InstanceShortName, s.quotaArgs.Quota) {
                    continue
                }
                if !groupMatched {
                    // This is a conditional quota and it does not apply to the requester
                    // return what was requested
                    s.quotaResult.Amount = s.quotaArgs.Amount
                    s.quotaResult.ValidDuration = defaultValidDuration
                }
                foundQuota = true
              }
                
				// 如果上个循环不匹配，就会继续循环
				if !groupMatched{
					continue
				}
				
				// 通过构建一个后端适配器handler实例, 这样通过instance作为输入参数就可以调用远端的grpc server服务了
				if instance, err = input.Builder(s.bag); err != nil {
				}
				// 如果variety为TEMPLATE_VARIETY_REPORT， 则添加到dispatchState中，表示对于这个请求，匹配到了多个后端适配器
				if s.variety == tpb.TEMPLATE_VARIETY_REPORT {
					state.instances = append(state.instances, instance)
					continue
				}
				
				// 如果为其他，比如：variety为TEMPLATE_VARIETY_ATTRIBUTE_GENERATOR, 我们则通过进行pb协议的数据转换。
				state = s.impl.getDispatchState(s.ctx, destination)
				state.instances = append(state.instances, instance)
				if s.variety == tpb.TEMPLATE_VARIETY_ATTRIBUTE_GENERATOR {
					state.mapper = group.Mappers[j]
					state.inputBag = s.bag
				}
				......
				// dispatchState进行任务调度，等待任务被执行
				s.dispatchToHandler(state)
			}
		}
	}
	// 等待session中的所有dispatchState任务被调度完成
	s.waitForDispatched()
	
	// variety为TEMPLATE_VARIETY_CHECK的调度
	if s.variety == tpb.TEMPLATE_VARIETY_CHECK && status.IsOK(s.checkResult.Status) {
		......
	}
}
```

### 任务调度处理

mixer server采用了goroutine调度池，限制协诚的使用数量，进行任务调度. 在session的dispatch方法中


```shell
// 当使用session的dispatch分发时，最终会把任务放置到channel队列上, 以function方法和param参数的形式打包成任务
func (s *session) dispatchToHandler(ds *dispatchState) {
    s.activeDispatches++
    ds.session = s
    s.impl.gp.ScheduleWork(ds.invokeHandler, nil)
}

// 当goroutine池有空闲的协诚时，会取出任务并执行任务。最终goroutine执行这个处理函数
func (ds *dispatchState) invokeHandler(interface{}) {
	 ......
	 // 下面都是针对dispatchState数据，分发处理。
	 // 比如：variety为TEMPLATE_VARIETY_REPORT，则通过destination中的template找到对应的处理方法，比如：DispatchReport。
	 // 这个函数里面会发起真正到后端适配器服务的grpc client调用。
	 switch ds.destination.Template.Variety {
    case tpb.TEMPLATE_VARIETY_ATTRIBUTE_GENERATOR:
        ds.outputBag, ds.err = ds.destination.Template.DispatchGenAttrs(
            ctx, ds.destination.Handler, ds.instances[0], ds.inputBag, ds.mapper)

    case tpb.TEMPLATE_VARIETY_CHECK, tpb.TEMPLATE_VARIETY_CHECK_WITH_OUTPUT:
        // allocate a bag to store check output results
        // this bag is released in session waitForDispatched
        ds.outputBag = attribute.GetMutableBag(nil)

        ds.checkResult, ds.err = ds.destination.Template.DispatchCheck(
            ctx, ds.destination.Handler, ds.instances[0], ds.outputBag, ds.outputPrefix)

    case tpb.TEMPLATE_VARIETY_REPORT:
        ds.err = ds.destination.Template.DispatchReport(
            ctx, ds.destination.Handler, ds.instances)

    case tpb.TEMPLATE_VARIETY_QUOTA:
        ds.quotaResult, ds.err = ds.destination.Template.DispatchQuota(
            ctx, ds.destination.Handler, ds.instances[0], ds.quotaArgs)

    default:
        panic(fmt.Sprintf("unknown variety type: '%v'", ds.destination.Template.Variety))
    }
    ......
}

// 举个例子, 再初始化mixer server时，内置的模板列表中会存在DispatchReport方法， 比如：模板为servicecontrolreport
// 下面的处理方法，则在处理所需要的服务函数和参数后，发起grpc client请求调用。也就是断言判断
DispatchReport: func(ctx context.Context, handler adapter.Handler, inst []interface{}) error {

                // Convert the instances from the generic []interface{}, to their specialized type.
                instances := make([]*servicecontrolreport.Instance, len(inst))
                for i, instance := range inst {
                    instances[i] = instance.(*servicecontrolreport.Instance)
                }

                // Invoke the handler.
                if err := handler.(servicecontrolreport.Handler).HandleServicecontrolReport(ctx, instances); err != nil {
                    return fmt.Errorf("failed to report all values: %v", err)
                }
                return nil
            },
```

## 总结

经过这个例子，我们可以明白从envoy proxy发起grpc client调用，到后端适配器真正处理的完整流程了。


## 再阅读源码理解

本小节主要讲解mixer server接收envoy proxy的grpc client请求的完整处理过程。

mixer server处理client发送过来的请求包括两个：Check和Report，也即mixc的两个命令。

这部分先讲解Report请求，因为Check请求包括了mixer server的缓存设计，这个可以阅读[敖小剑的技术博客](https://skyao.io/post/201804-istio-mixer-cache-concepts/)连续4篇，写得比较详细。

Report请求处理流程： 

在mixer server接收到grpc client请求后，就开始进行请求数据的封装，并获取dispatcher存储的reporter对象。

mixc可以进行本地缓存批量一次性请求grpc server服务，所以会存在请求Attributes列表, 遍历列表做以下步骤处理：

因为在dispatch分发时，会根据不同的variety服务类型，会做不同的调用流程，主要是动态生成标签的variety，会直接处理。因为不涉及到远端服务。而report可能会涉及到远端服务调用，所以尽量批量调度处理。

1. 数据包经过Preprocess处理。封装一个variety为TEMPLATE_VARIETY_ATTRIBUTE_GENERATOR的临时session数据包，获取请求数据属性`context.reporter.kind`和根据前面这个属性值，获取`destination.namespace`或者`source.namespace`的属性值。从而找到NamespaceTable的路由;
2. 遍历NamespaceTable中的destinations列表，校验每个rule是否匹配请求数据中的属性值。如果匹配, 则构建template的instance数据实例，生成待调度的dispatchState任务放入到队列中。在任务被goroutine调度并执行时，会通过variety进行服务分类处理，落到template的DispatchGenAttrs， DispatchCheck，DispatchReport或者DispatchQuota, 因为adapter实现了该template的相关服务API，所以最后调用到了静态的本地服务或者动态的远端服务。


对于variety为TEMPLATE_VARIETY_REPORT的服务类型，首先把所有适配到的destinations构建成一个类似map[destination][]instance的任务，并不是每处理一个destination,就进行任务调度，而是一次性把grpc server接收到的请求数据，进行缓存并最后通过reporter.Flush方法进行一次性的任务调度。

mixer server在后期版本中把mixc Quota命令放在了mixc Check子命令中。

Reporter的处理逻辑还是比较好理解的
