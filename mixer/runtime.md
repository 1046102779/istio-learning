本小结详细介绍runtime运行时环境初始化，以及接收envoy proxy的grpc client请求，以及监听后端配置存储的配置变化等，涉及的内容非常多，且很复杂。

## runtime初始化

首先，了解下runtime的运行时相关参数，参数表格如下：

| 参数名 | 类型 | 参数描述 |
| --- | --- | --- |
| defaultConfigNamespace | string | 默认值：istio-system , 可通过mixer server启动时指定命令行设置默认空间 |
| ephemeral | `*config.Ephemeral` | 运行时环境需要存储mixer server内置的所有templates和adapters，以及存储所有mixer server服务后端所有适配器的配置，包括：Handler, Template和DestinationRule | 
| snapshot | `*config.Snapshot` | 含义后面再补充，暂时不知 |
| handlers | `*handler.Table` | 路由作用。dispatcher分发grpc client请求，给具体后端适配器处理，这需要路由。|
| dispatcher | `*dispatcher.Impl` | 分发envoy proxy的grpc client请求，并且实现Dispatcher接口的四个方法：Check、GetReport、Qutoa和Preprocess |
| store | store.Store | 这个在backend-store.md中已说明，提供了获取和监听后端存储配置的方法列表 |
| handlerPool | `*pool.GoroutinePool` | 后端各个适配器同时能处理grpc client请求的并发goroutine数量 |
| ... | ... | ... |


```shell
// 初始化runtime实例，所有的运行时环境所需要的参数都是从mixer server中获取的
func New(
    s store.Store,
    templates map[string]*template.Info,
    adapters map[string]*adapter.Info,
    defaultConfigNamespace string,
    executorPool *pool.GoroutinePool,
    handlerPool *pool.GoroutinePool,
    enableTracing bool) *Runtime {

	 // ephemeral用于创建runtime运行时环境的templates和adapters，以及初始化配置存储数据模型
	 //
	 // 还有一个mixer server支持的操作符和运算符typeChecker
    e := config.NewEphemeral(templates, adapters)
    // 这个snapshot后面再看,handlers初始化路由表为空
    // 实例化dispatcher，
    rt := &Runtime{
        defaultConfigNamespace: defaultConfigNamespace,
        ephemeral:              e,
        snapshot:               config.Empty(),
        handlers:               handler.Empty(),
        dispatcher:             dispatcher.New(executorPool, enableTracing), // 从接收grpc client请求，到后端适配器真正处理的整个路由表
        handlerPool:            handlerPool,
        Probe:                  probe.NewProbe(),
        store:                  s,
    }
        
    // 这个很复杂，我看得也懵逼，咋们娓娓道来
    rt.processNewConfig()

    rt.Probe.SetAvailable(errNotListening)

    return rt
}

// 初始化runtime实例时，或者当后端存储服务配置发生变化时，会重构handlers和snapshot。
func (c *Runtime) processNewConfig() {
	// 这个方法那就是相当复杂了。因为有静态的templates、adapters配置处理，也就是Handler、Template、DestinationRule
	// 还有本地动态生成的adapters和templates，比如：$GOPATH/src/istio.io/istio/mixer/template/xxx/template.proto, 这都是由python生成后，然后再由codegen生成*.yaml文件。
	// $GOPATH/src/istio.io/istio/mixer/adapter/xx/config/config.proto, 并由codegen生成*.yaml文件
	// 这些都是动态的，也需要加载到一个稳定的环境中存储。
	// 再就是后端配置存储服务，比如：k8s, mcp或者fs存储的相关配置，也需要加载
	//
	// 这个也就是一个相对稳定的，从rule到instance，再到handler的路由环境。路由的重建是非常耗时的，这可能对业务带来很大的影响。
	// 具体见下面方法的分析
    newSnapshot, _ := c.ephemeral.BuildSnapshot()

	 // 下面两行代码，用于创建一个新的Table, 并对静态和动态的templates与adapters进行BuildHandler接口校验。并生成一个map[string]entry, 用于通过适配器名称就可以获得一个grpc client，并调用远程rpc服务。类似于路由
    oldHandlers := c.handlers
    newHandlers := handler.NewTable(oldHandlers, newSnapshot, c.handlerPool)

	// ExpresssionBuilder定义了mixer server支持的所有固定属性，以及提供的内置处理标签属性数据的方法列表，用于把一些类型转化为真正的go类型，比如：
	// ip(config.IP_ADDRESS) config.STRING。
	// timestamp(config.TIMESTAMP) config.STRING
    builder := compiled.NewBuilder(newSnapshot.Attributes)
    
    // BuildTable方法是runtime运行时中最重要的一个方法了。它提供了Handler、Template和DestinationRule数据流向完整的流程，是一张完整的路由表。从mixer server接收到envoy proxy发送grpc client请求开始，到发送给适配器服务真正的处理遥测数据链路。
    // 
    // 这里涉及到DestinationRule资源中spec下的match语法解析器
    // 还涉及到template的实例化，遍历所有的rules，然后生成一张完整的整个路由表
    // 我详细介绍下这个方法
    newRoutes := routing.BuildTable(
        newHandlers, newSnapshot, builder, c.defaultConfigNamespace, log.DebugEnabled())

	// 将构建成的新全局路由存放到dispatcher中，用于分发处理envoy proxy发送过来的grpc client请求。
    oldContext := c.dispatcher.ChangeRoute(newRoutes)

	 // 保存后端适配器的处理流程和snapshot配置的稳定环境
    c.handlers = newHandlers
    c.snapshot = newSnapshot

    log.Debugf("New routes in effect:\n%s", newRoutes)

	// 清除除snapshot的其他环境，减少内存使用。
    cleanupHandlers(oldContext, oldHandlers, newHandlers, maxCleanupDuration)
}

// BuildSnapshot方法用于构建一个稳定的，完全可以解析的配置快照视图
// 这个方法是相当的重要，对mixer server所有资源默认的spec部分的固定属性标签，以及后端适配器自己关注的spec属性标签进行修改更新。
// 
// 也就是说通过Snapshot，runtime运行时环境就可以拿到所有的所有最新配置数据
func (e *Ephemeral) BuildSnapshot() (*Snapshot, error) {
	......
	// 首先获取mixer server能够处理的所有属性标签集合，比如：source.uid，source.ip，source.labels etc.
	// 在mixer server的属性标签包括两部分构成：
	// 1. mixer server自带SupportedTemplates中variety为TEMPLATE_VARIETY_ATTRIBUTE_GENERATOR，则表示是动态生成的属性标签；比如:模板名为kubernetes
	// 2. 后端配置存储的yaml文件集合中，kind=attributemanifest的资源类型，也是新增的可以被mixer server处理的属性标签
	// 那么这个方法就是告诉mixer server支持的所有属性集合，非常重要。把SupportedTemplates列表k8s定义的属性标签列表与后端存储中存在资源kind为attributemanifest的属性标签列表进行存储，并存储到map[string]*config.AttributeManifest_AttributeInfo
	// 这个存储的类型定义：map中的key表示属性名，value表示属性名的类型。比如：adapter_template_kubernetes.output.source_pod_uid属性类型为：istio_policy_v1beta1.STRING
	attributes := e.processAttributeManifests(monitoringCtx)

	 // 这个方法目的：因为mixer server已经内置了adapters列表，但是每个adapter默认连接rpc的默认配置，比如：服务端口和其他配置等，如果远端服务不是默认的配置，则需要通过后端配置存储指定配置连接
	 // 
	 // 通过在后端配置存储中指定要修改默认配置的适配器名称，并指定kind为handler。适配器的名称指定通过spec的compileAdapter变量指定。配置由spec下的params指定。
	 /*
		apiVersion: "config.istio.io/v1alpha2"
		kind: handler
		metadata:
		  name: statsd
		  namespace: istio-system
		spec:
		  compiledAdapter: statsd
		  params:
		    Address: "10.1.20.47"
		    Prefix: "clever"
	 */
	 // 比如：
	 /*
	 	 // 适配器名称：statsd
        DefaultConfig: &config.Params{
            Address:       "localhost:8125",
            Prefix:        "",
            FlushDuration: 300 * time.Millisecond,
            FlushBytes:    512,
            SamplingRate:  1.0,
        },
	 */
	 // 通过上面mixer server statsd的默认配置，和后端配置存储需要修改连接statsd服务的配置，进行覆盖，则statsd服务就能够真正接收到grpc client的请求。
	 // 返回值为：map[string]*HandlerStatic, key为资源唯一标识，value为adapter，注意：这个value既保留了默认配置，也有后端配置存储传过来的配置
    shandlers := e.processStaticAdapterHandlerConfigs(monitoringCtx)

	 // 对mixer server支持所有的属性词汇列表，进行封装并提供一些查询方法
	 // 比如：GetAttribute方法，通过属性名获取属性类型和描述等。以及字符串化
    af := ast.NewFinder(attributes)
    // 两个作用：
    // 1. 这个是对mixer server中内置的templates进行修改封装，因为内置的templates中spec部分属性太全了，而后端适配器只关注自己想要的属性标签数据。
    // 2. 同时, 需要对关注的属性标签类型进行校验等。
    // 返回map[string]*InstanceStatic, key为资源唯一性标识，value为template实例化的instance，包含了后端配置存储的关注的属性标签列表
    // 这个和handler类似，都是关注自己的标签属性列表
    instances := e.processInstanceConfigs(monitoringCtx, af, errs)

    // 下面都是些动态生成的配置，通过python生成.proto文件，再生成.yaml文件，后者中的descriptor比较特殊，需要解析
    //
    // 该方法用于对后端配置存储中kind为template的资源进行spec部分的属性标签存储，这个是在针对mixer server中静态的templates列表新增的。
    dTemplates := e.processDynamicTemplateConfigs(monitoringCtx, errs)
    // 该方法用于对后端配置存储中kind为adapter的资源进行存储，这个是在针对mixer server中静态的adapters列表新增的
    dAdapters := e.processDynamicAdapterConfigs(monitoringCtx, dTemplates, errs)
    // 该方法用于对后端配置存储中kind为handler的资源进行存储，动态映射的handler所对应的adapter
    dhandlers := e.processDynamicHandlerConfigs(monitoringCtx, dAdapters, errs)
    // 这个也一样，后续我们在详细看这四个动态Template、Handler、Adapter和Instance。配对规则：(Handler封装了Adapter)，(Instance封装了Template)
    dInstances := e.processDynamicInstanceConfigs(monitoringCtx, dTemplates, af, errs)

	 // 把动态生成的handlers、templates，和静态的handlers和templates融合后，对后端配置存储kind为rule，进行rule到handler和instance的映射，方便后续直接处理请求
    rules := e.processRuleConfigs(monitoringCtx, shandlers, instances, dhandlers, dInstances, af, errs)
    // 构建Snapshot实例
    s := &Snapshot{
        ID:                id,
        Templates:         e.templates,
        Adapters:          e.adapters,
        TemplateMetadatas: dTemplates,
        AdapterMetadatas:  dAdapters,
        Attributes:        ast.NewFinder(attributes),
        HandlersStatic:    shandlers,
        InstancesStatic:   instances,
        Rules:             rules,

        HandlersDynamic:  dhandlers,
        InstancesDynamic: dInstances,

        MonitoringContext: monitoringCtx,
    }
    return s, errs.ErrorOrNil()
}

// 通过构建Snapshot实例的过程，我们可以看到，
// 1. snapshot融合了能够处理enovy proxy发送grpc client请求。而snapshot主要是用于对后端配置存储与mixer server默认的templates和adapters进行动态和静态的处理，
// 2. 把配置转化为可以处理请求的数据模型定义和属性标签校验;
// 3. 后端配置存储也可以修改mixer server默认的adapter和template的spec属性标签，让后端适配器设置自己所关心的属性标签列表；
// 4. rules规则下的spec部分定义了match, actions[{handler, instance[]}]，这样通过上面的动静态handlers和instances，来进行rule到具体的template和adapter列表路由。
// 通过以上四点，我们就可以完整地处理从dispatcher到后端适配器的整个数据流了。

// 再注意一点，当后端配置存储发生变化时，mixer server监听到事件后，则会重新构建snapshot。这个过程是非常耗时的。

// NewTable方法用于
func NewTable(old *Table, snapshot *config.Snapshot, gp *pool.GoroutinePool) *Table {
    // 因为rules列表中的每一条DestinationRule路由规则，都包含对应着多个{handler, []instance},
    // 同时分为mixer servers内置的和后端动态配置的
    // 下面两个方法的目的都是形成handler到instaces的映射, 也就是每个handler可以处理多个instances的数据模型。
    instancesByHandler := config.GetInstancesGroupedByHandlers(snapshot)
    instancesByHandlerDynamic := config.GetInstancesGroupedByHandlersDynamic(snapshot)

	 ......
	 
	 // 那么上面生成的合并成下面的整体handler对应instances，便是这样的：map[handler][]instance
    t := &Table{
        entries:       make(map[string]Entry, len(instancesByHandler)+len(instancesByHandlerDynamic)),
        monitoringCtx: ctx,
    }

	// 下面这两段代码，主要是config.BuildHandler与dynamic.BuildHander两个方法
	//
	// 首先我们理解一个概念：一对多，也就是一个适配器可以同时处理多个模板，用于遥测不同维度的指标，同时针对不同服务遥测不同指标
	//
	// 那么这里主要是当我找到合适的适配器后，通过Builder构建Handler。再通过具体的模板数据模型来进行grpc server服务调用；比如：
	// 
	// stdio适配器支持两种模板数据上传, 提供了pb的两种服务：HandleLogEntry和HandleMetric
	// 
	// 这里不明白的一点是：我们在arch.md和adapters.-and-templates.md小节中，介绍过两种关系的校验，因为所有的适配器都需要实现templates定义的HandlerBuilder。
	// 后端配置存储要做templates与adapters的校验我能理解，但是静态地我们在前面已经做过校验了，为何还要做一次校验呢？
    for handler, instances := range instancesByHandler {
        createEntry(old, t, handler, instances, snapshot.ID,
            func(handler hndlr, instances interface{}) (h adapter.Handler, e env, err error) {
                e = NewEnv(snapshot.ID, handler.GetName(), gp).(env)
                h, err = config.BuildHandler(handler.(*config.HandlerStatic), instances.([]*config.InstanceStatic),
                    e, snapshot.Templates)
                return h, e, err
            })
    }

    for handler, instances := range instancesByHandlerDynamic {
        createEntry(old, t, handler, instances, snapshot.ID,
            func(_ hndlr, _ interface{}) (h adapter.Handler, e env, err error) {
                e = NewEnv(snapshot.ID, handler.GetName(), gp).(env)
                tmplCfg := make([]*dynamic.TemplateConfig, 0, len(instances))
                for _, inst := range instances {
                    tmplCfg = append(tmplCfg, &dynamic.TemplateConfig{
                        Name:         inst.Name,
                        TemplateName: inst.Template.Name,
                        FileDescSet:  inst.Template.FileDescSet,
                        Variety:      inst.Template.Variety,
                    })
                }
                h, err = dynamic.BuildHandler(handler.GetName(), handler.Connection,
                    handler.Adapter.SessionBased, handler.AdapterConfig, tmplCfg)
                return h, e, err
            })
    }
    return t
}

// BuildTable方法非常复杂，我们可以看到builder类型很复杂，而且只返回builder中的table。其他都是辅助完成table整张路由表的构建，设计的参数变量非常多。
//
// 
func BuildTable(
    handlers *handler.Table,
    config *config.Snapshot,
    expb *compiled.ExpressionBuilder,
    defaultConfigNamespace string,
    debugInfo bool) *Table {

	 // 创建builder实例
    b := &builder{
        table: &Table{
            id:      config.ID,
            entries: make(map[tpb.TemplateVariety]*varietyTable, 4),
        },

        // nolint: goimports
        handlers: handlers,
        expb:     expb,
        defaultConfigNamespace: defaultConfigNamespace,
        nextIDCounter:          1,

        matchesByID:       make(map[uint32]string, len(config.Rules)),
        instanceNamesByID: make(map[uint32][]string, len(config.InstancesStatic)),

        builders:    make(map[string]template.InstanceBuilderFn, len(config.InstancesStatic)),
        mappers:     make(map[string]template.OutputMapperFn, len(config.InstancesStatic)),
        expressions: make(map[string]compiled.Expression, len(config.Rules)),
    }

	 // 构建路由表， 详见下面的build方法
    b.build(config)

	 ......
	 
	 return b.table
}


// 构建mixer server接收grpc client请求的全流程路由表
func (b *builder) build(snapshot *config.Snapshot) {
	// 遍历所有的DestinationRule规则，并形成路由表
	// 做的事情包括：
	// 1. rule中的match字符串抽象语法树的解析，形成逻辑表达式
	// 2. 
	for _, rule := range snapshot.Rules {
		condition, err := b.getConditionExpression(rule)
		......
		// 静态的DestinationRule部分actions
		for i, action := range rule.ActionsStatic {
			 // 通过handler名称，获取entry。
			 handlerName := action.Handler.Name
           entry, found := b.handlers.Get(handlerName)
           // 遍历这个规则下的所有配置实例列表
           for _, instance := range action.Instances {
           	// 并构建template成实例需要的InstanceBuilder函数。该函数可以直接把grpc client传送过来的pb协议数据，直接转化为template实例的数据模型的实例化数据
           	builder, mapper, err := b.getBuilderAndMapper(snapshot.Attributes, instance)
           	// 在builder的tables中增加一条全局数据路由。
           	// 形成一条TemplateVariety四种类型----->namespace------>后端handlers处理流程, 至于instanceGroups参数设置都是构建template实例instance，并进行数据填充到实例中的函数列表
           	// 注意InstanceGroups是对同一个handler不同的表达式列表进行组织。
   	          b.add(rule.Namespace, buildTemplateInfo(instance.Template), entry, condition, builder, mapper,
         		   entry.Name, instance.Name, rule.Match, action.Name)
           }
		}
		
		// 动态的DestinationRule部分actions, 作用同上
		for i, action := range rule.ActionsDynamic {
			handlerName := action.Handler.Name
          entry, found := b.handlers.Get(handlerName)
          for _, instance := range action.Instances {
          	builder, mapper := b.getBuilderAndMapperDynamic(snapshot.Attributes, instance)

             b.add(rule.Namespace, b.templateInfo(instance.Template), entry, condition, builder, mapper,
                    entry.Name, instance.Name, rule.Match, action.Name)
          }
		}
	}
	
	// 校验是否存在一个默认空间的路由。并添加到路由表中
	for _, vTable := range b.table.entries {
		defaultSet, found := vTable.entries[b.defaultConfigNamespace]
		vTable.defaultSet = defaultSet
		
		if defaultSet.Count() != 0 {
            // Prefix all namespace destinations with the destinations from the default namespace.
            for namespace, set := range vTable.entries {
                if namespace == b.defaultConfigNamespace {
                    // Skip the default namespace itself
                    continue
                }

                set.entries = append(defaultSet.entries, set.entries...)
            }
        }
	}
}
```
