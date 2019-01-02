 本小节主要梳理handler Table的构建过程, 在初始化或者监听到后端存储配置的事件后，就会重新构建runtime的环境，那么handler Table也是重构必不可少的一个环节。它构建过程包含几个步骤：
 
>  前置说明：在上节谈过了rule配置资源对象的动态加载，那么这小节就是以rule配置资源spec部分的actions作为入口，构建完整的handler Table

对于handler包括静态的handler和动态的handler。
1. 组织一个handler映射多个instance的动态和静态map。
2. 初始化handler Table：entry，并对(1)进行template与adapter的接口实现校验，包括两步：一. handler中的adapter.Info的HandlerBuilder实现了template定义的HandlerBuilder构建接口；二. handler中的adapter.Info中构建Handler实现了template定义的Handler grpc service服务定义接口。这都是作为client而言的接口实现。
3. 校验完成后，添加handler.Name与Entry{Handler, Name, AdapterName}的映射，这样通过handler name我们就可以发起grpc client调用后端真正服务。
4. 对于静态对象资源，那就是本地服务；对于动态资源对象，则需要创建一个grpc.Client连接conn，并保存到Handler。

tips: 
1. 在第2步中，会替换掉Template定义的proto.Message，采用配置文件所关心的数据模型定义。
2. 对于动态资源对象handler，需要创建一个grpc连接。


## 本小节介绍整个路由的构建过程

要说整个路由的构建过程，那就应该以kind为rule的资源对象说起，因为它是条件匹配入口。

构建整个路由的入口由`$GOPATH/src/istio.io/istio/mixer/pkg/runtime/routing/builder.go`文件的BuildTable方法开始。

注意，在mixer server的整个过程中，我发现所有对象都是通过`builder`对象生产的。所以mixer server的整个Table也是由builder构建，则首先创建一个builder。然后再通过rules列表进行逐一路由记录的建立。

构建过程的具体步骤：
1. 初始化构建路由的builder对象；
2. 遍历rules列表，并对每个rule规则的actions列表进行遍历，获取每个handler和对应的instances列表，形成`variety->namespace->func handler(instances)`的路由, 这里面包括动态资源对象和静态资源对象。
3. 对于(2)，其中需要获取构建instance的InstanceBuilder, 这个是用来进行proto.Message与template中定义的各个Check、Report、Qutoa等服务参数的转化；还有一点注意，InstanceGroups列表的含义，对于同一个handler， 如果rule的match相同，则多个instance构成一组。
4. 最后一点是有关设置默认namespace的路由过程，每一条路由记录，都需要发送给默认的路由处理。如果默认路由缺省，则不需要处理；


```shell
// 构建整个路由表
func BuildTable(handlers *handler.Table, config *config.Snapshot, expb *compiled.ExpressionBuilder, defaultConfigNamespace string, debugInfo bool) *Table{
	b:=&builder{
		table: &Table{
			id: config.ID,
			entries: make(map[tpb.TemplateVriety]*vrietyTable, 4),
		},
		
		handlers: handlers,
		...
		expressions: make(map[string]compiled.Expression, len(config.Rules)),
	}
	// 构建路由表
	b.build(config)
	return b.table
}

// 构建路由表
func (b *builder) build(snapshot *config.Snapshot) {
	// 对所有的rule列表进行遍历，建立路由表
	for _, rule := range snapshot.Rules {
		condition, err := b.getConditionExpression(rule)
		...
		// 首先对静态的actions建立路由
		for i, action := range rule.ActionsStatic {
			handlerName := action.Handler.Name
			entry, found := b.handlers.Get(handlerName)
			// 对每个handler映射多个template，建立起一个handler对应多个InstanceGroups的映射,每一个InstanceGroup都是对应相同的条件匹配规则。
			for _, instance := range action.Instances {
				builder, mapper, err := b.getBuilderAndMapper(snapshot.Attributes, instance)
				b.add(rule.Namespace, buildTemplateInfo(instance.Template), entry, condition, builder, mapper,
				     entry.Name, instance.Name, rule.Match, action.Name)
			}
		}
		
					
		// 再对动态的actions建立路由，同上
		for i, action := range rule.ActionsDynamic {
			handlerName := action.Handler.Name
			entry, found := b.handlers.Get(handlerName)
			for _, instance := range action.Instances {
				builder, mapper := b.getBuilderAndMapperDynamic(snapshot.Attributes, instance)
				b.add(rule.Namespace, b.templateInfo(instance.Template), entry, condition, builder, mapper,
					entry.Name, instance.Name, rule.Match, action.Name)
		}
	}

	// 对于设置的默认namespace，都需要数据流都需要首先经过默认这条路由处理。
	for _, vTable := range b.table.entries {
		defaultSet, found := vTable.entries[b.defaultConfigNamespace]
		vTable.defaultSet = defaultSet
		if defaultSet.Count() != 0 {
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
}
```

## 总结

我们可以看到mixer server初始化runtime时，整个路由表的完整建立过程。每个envoy proxy的grpc client请求或者命令，再加上动态生成属性请求，共分为四类服务，分别是Check, Report, Qutoa和GenAttrs。然后再对每个rule设置的处理namespace进行分类，最后真正路由到handler的entries进行处理，并调用tempalte中定义的契约接口，进行静态资源对象的本地调用，或者动态资源对象的远程调用。
