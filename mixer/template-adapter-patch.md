本节我们是对mixer server的后端存储配置加载，以及对各个类型的配置资源种类构造成可以处理grpc client请求的运行时状态。

## SupportedTmplInfo

模板：模板用于数据模型的定义，为后端适配器提供数据模型。
职责：模板在handler、instance与rule中，充当instance角色，并把rule匹配的数据流，填充到template实例化后的instance列表中，并最后通过grpc发送给后端适配器处理。

所以template实例化后的instances，是作为后端适配器grpc server定义的接口参数。数据模型实例化。

该变量在文件`$GOPATH/src/istio.io/istio/mixer/template/template.pb.go`中，以map的形式呈现，长度为12。

它表示istio支持的所有模板，除了这些模板，【**TODO**】我目前还不确定是不是可以动态加载后端存储配置，然后动态地新增第三方开发者自定义的模板支持。

先介绍模板的数据定义:

```shell
type Info struct {
    Name                    string  // 模板名
    Impl                    string  // unknown ::TODO
    Variety                 adptTmpl.TemplateVariety // 模板种类：目前一共有以下5种。它标识这个模板的路由路径, 比如：模板用于定义check数据模型的，那种类为TEMPLATE_VARIETY_CHECK；当模板用于定义report类型模型时的，那种类为TEMPLATE_VARIETY_REPORT; 当模板用于定义动态生成一些固定属性时的数据模型定义，则种类为TEMPLATE_VARIETY_ATTRIBUTE_GENERATOR
    // 也就是说，模板针对不同的请求服务做了服务类型分类；
	/*
    TEMPLATE_VARIETY_CHECK TemplateVariety = 0
    TEMPLATE_VARIETY_REPORT TemplateVariety = 1
    TEMPLATE_VARIETY_QUOTA TemplateVariety = 2
    TEMPLATE_VARIETY_ATTRIBUTE_GENERATOR TemplateVariety = 3
    TEMPLATE_VARIETY_CHECK_WITH_OUTPUT TemplateVariety = 4
   */
    BldrInterfaceName       string // 是HandlerBuilder interface：用于构建一个后端适配器的handler。这个handler用于发送grpc client请求给后端适配器处理请求。变量值：${模板名}.HandlerBuilder. 表示是哪个模板名的HandlerBuilder
    HndlrInterfaceName      string // handler后端适配器接口名，变量值: ${模板名}.Handler， 表示是哪个模板名的Handler实例
    CtrCfg                  proto.Message // 这个是最后模板实例化后，并作为grpc client请求后端适配器服务的参数instances, 也就是各个模板定义的proto协议动态生成的xxx.gen.go文件
    InferType               InferTypeFn // 对模板中的数据模型定义支持的固定属性列表AttributeManifests进行数据类型校验
    // 比如: 上传的属性名称source.uid，如果类型不是string类型，则需要报错。
    // 同时另一个需要说明:
    //  我们描述一个场景，对于istio的固定属性列表，比如：source.ip这个表示源IP，但是具体是指什么IP呢？对应后端适配器的是什么IP呢？这样就存在一个映射
    //	比如在k8s集群中，souce.ip对应后端适配器的IP规范名:adapter_template_kubernetes.output.source_pod_ip
    // 所以就会存在source.ip与这个source_pod_ip的映射关系以及类型相同校验，这些都是InferType的事情，并返回校验成功后的模板定义中的Type, 这个Type是关于authorization模板的实例， 具体不知道，::TODO
    SetType                 SetTypeFn  // 很多模板都没有这个参数，不知道其含义
    BuilderSupportsTemplate BuilderSupportsTemplateFn  // 用于校验adapter是否实现了这个模板定义的HandlerBuilder接口。后者核心任务构成建一个adapter的Handler。调用grpc server
    HandlerSupportsTemplate HandlerSupportsTemplateFn // 用于校验adapter的Handler是否实现了模板定义的Handler接口。与上面的BuilderSupportsTemplate作为承接关系

    AttributeManifests []*pb.AttributeManifest // 表示这个模板支持的固定属性列表。
    /*
    		属性名：adapter_template_kubernetes.output.source_pod_uid， 则该变量的类型为string类型
    */

    DispatchReport   DispatchReportFn // 当模板种类为TEMPLATE_VARIETY_REPORT，该方法作为grpc server服务注册的方法，mixer server作为客户端通过这个方法，发送grpc client请求到真正的后端适配服务处理report请求。
    DispatchCheck    DispatchCheckFn // 当模板种类为TEMPLATE_VARIETY_CHECK时，该方法作为grpc server服务注册的方法，mixer server作为客户端通过这个方法，发送grpc client请求到真正的后端适配器服务处理check请求。
    DispatchQuota    DispatchQuotaFn // 当模板种类为TEMPLATE_VARIETY_QUOTA时，该方法作为grpc server服务注册的方法，mixer server作为客户端通过这个方法，发送grpc client请求到真正的后端适配器服务处理quota请求
    DispatchGenAttrs DispatchGenerateAttributesFn // 当模板种类为TEMPLATE_VARIETY_ATTRIBUTE_GENERATOR，则是通过该方法把模板的实例化发送到后端适配器handler生产属性列表

    CreateInstanceBuilder   CreateInstanceBuilderFn // 通过模板创建Instance所需要的InstanceBuilder, 与HandlerBuilder功能类似。
    CreateOutputExpressions CreateOutputExpressionsFn // 为模板支持的固定属性列表，建立属性与抽象语法树的映射关系表达式，构建成map数据结构
}
```

通过以上对template.Info的介绍，明白template作为mixer server请求分发的核心。它不仅对后端适配器服务接口进行校验，还对属性标签进行数据类型校验和名称的映射，同时对数据表达式进行抽象语法树的匹配校验。

## adapters

适配器：为所有后端配置存储提供可以选择的适配器，进行代码结构化。体现在yaml资源配置中，则是kind为handler，且spec部分的compiledAdapter字段锁定适配器数据模型定义。
职责：适配器在handler、instance与rule中，充当handler角色的spec部分的compiledAdapter字段值。标识yaml文件kind类型为handler对接到mixer server支持的适配器名称。通过这个进行关联映射，最后进行程序化处理过程。

mixer server支持后端存储配置指定的适配器名列表共有20个，都定义在`$GOPATH/src/istio.io/istio/mixer/adapter/inventory.gen.go`文件中。

先介绍适配器的数据定义：

```shell
type Info struct {
	Name string // 适配器名称
	Impl string // 实现适配器的包
	Description string //适配器描述
	NewBuilder NewBuilderFn // 这个用于构建并实例化一个handler。
	// 注意: 这个handler是mixer server本地的handler，所以它是对远端后端适配器的grpc server的客户端封装。同时该handler是对上面所说的template的DispatchReport，DispatchCheck和DispatchQuato等的接口实现。template只是定义服务协议列表。
	SupportedTemplates []string // 该适配器支持的template列表。也就是说一个远端的后端适配器可以对外提供多个服务
	DefaultConfig proto.Message // 表示mixer server提供的适配器数据定义的默认配置，这个可以通过后端配置存储进行动态修改
}
```


由上面的template.Info和adapter.Info两个类型，我们大体知道，template提供服务接口定义、数据模型定义。最后通过adapter来发送grpc client请求到远端的后端适配器服务。
