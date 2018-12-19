这部分主要介绍在创建mixer server实例的过程中，templates与adapters之间的关系，并详细介绍所有adapters中各自指定的SupportedTemplates列表，实现了模板列表中各个模板定义的HandlerBuilder interface

我们这里先看一个实例，在看所涉及的所有相关主逻辑代码。

## 一个DEMO

| 适配器 | 支持的模板列表 |
| --- | --- |
| stdio | logentry, metric |

### stdio适配器

该stdio适配器支持两个模板列表：logentry与metric。那么stdio必须实现logentry定义的Handler与其他相关的接口，比如：HandlerBuilder；还有metric定义的Handler与其他相关的接口，比如：HandlerBuilder。

```shell
# stdio adapter info
{
    Name:        "stdio",
    Impl:        "istio.io/istio/mixer/adapter/stdio",
    Description: "Writes logs and metrics to a standard I/O stream",
    SupportedTemplates: []string{ ## 该stdio适配器支持的template列表，有两个：logentry，metric
        logentry.TemplateName,
        metric.TemplateName,
    },
    DefaultConfig: &config.Params{
        LogStream:                  config.STDOUT,
        MetricLevel:                config.INFO,
        OutputLevel:                config.INFO,
        OutputAsJson:               true,
        MaxDaysBeforeRotation:      30,
        MaxMegabytesBeforeRotation: 100 * 1024 * 1024,
        MaxRotatedFiles:            1000,
        SeverityLevels: map[string]config.Params_Level{
            "INFORMATIONAL": config.INFO,
            "informational": config.INFO,
            "INFO":          config.INFO,
            "info":          config.INFO,
            "WARNING":       config.WARNING,
            "warning":       config.WARNING,
            "WARN":          config.WARNING,
            "warn":          config.WARNING,
            "ERROR":         config.ERROR,
            "error":         config.ERROR,
            "ERR":           config.ERROR,
            "err":           config.ERROR,
            "FATAL":         config.ERROR,
            "fatal":         config.ERROR,
        },
    },

    NewBuilder: func() adapter.HandlerBuilder { return &builder{} }, ## 很重要，在创建mixer Server实例时会通过template校验相关的adapters是否实现了template所定义的HandlerBuilder。
}
```

### 模板logentry

关注template.Info：logentry变量BuilderSupportsTemplate值，该函数值参数传入adapter builder，校验是否实现了logentry的HandlerBuilder接口。

注意：BuilderSupportsTemplate函数体中的logentry.HandlerBuilder是指`istio.io/istio/mixer/template/logentry`中的

```shell
type HandlerBuilder interface {
    adapter.HandlerBuilder

    // SetLogEntryTypes is invoked by Mixer to pass the template-specific Type information for instances that an adapter
    // may receive at runtime. The type information describes the shape of the instance.
    SetLogEntryTypes(map[string]*Type /*Instance name -> Type*/)
}
```

模板logentry值如下：

```shell
logentry.TemplateName: {
    Name:               logentry.TemplateName,
    Impl:               "logentry",
    CtrCfg:             &logentry.InstanceParam{},
    Variety:            istio_adapter_model_v1beta1.TEMPLATE_VARIETY_REPORT,
    BldrInterfaceName:  logentry.TemplateName + "." + "HandlerBuilder",
    HndlrInterfaceName: logentry.TemplateName + "." + "Handler",
    BuilderSupportsTemplate: func(hndlrBuilder adapter.HandlerBuilder) bool {
        _, ok := hndlrBuilder.(logentry.HandlerBuilder)
        return ok
    },
    HandlerSupportsTemplate: func(hndlr adapter.Handler) bool {
        _, ok := hndlr.(logentry.Handler)
        return ok
    },
    InferType: func(cp proto.Message, tEvalFn template.TypeEvalFn) (proto.Message, error) {
    		......
    }
    SetType: func(types map[string]proto.Message, builder adapter.HandlerBuilder) {
    		......
    }
    DispatchReport: func(ctx context.Context, handler adapter.Handler, inst []interface{}) error {
    		......
    }
    CreateInstanceBuilder: func(instanceName string, param proto.Message, expb *compiled.ExpressionBuilder) (template.InstanceBuilderFn, error) {
    		......
    }
```

### 模板metric

关注template.Info：metric变量BuilderSupportsTemplate值，该函数值参数传入adapter builder，校验是否实现了metric的HandlerBuilder接口。

注意：BuilderSupportsTemplate函数体中的metric.HandlerBuilder是指`istio.io/istio/mixer/template/metric`中的

```shell
type HandlerBuilder interface {
    adapter.HandlerBuilder

    // SetMetricTypes is invoked by Mixer to pass the template-specific Type information for instances that an adapter
    // may receive at runtime. The type information describes the shape of the instance.
    SetMetricTypes(map[string]*Type /*Instance name -> Type*/)
}
```

模板metric值如下：

```shell
metric.TemplateName: {
    Name:               metric.TemplateName,
    Impl:               "metric",
    CtrCfg:             &metric.InstanceParam{},
    Variety:            istio_adapter_model_v1beta1.TEMPLATE_VARIETY_REPORT,
    BldrInterfaceName:  metric.TemplateName + "." + "HandlerBuilder",
    HndlrInterfaceName: metric.TemplateName + "." + "Handler",
    BuilderSupportsTemplate: func(hndlrBuilder adapter.HandlerBuilder) bool {
        _, ok := hndlrBuilder.(metric.HandlerBuilder)
        return ok
    },
    HandlerSupportsTemplate: func(hndlr adapter.Handler) bool {
        _, ok := hndlr.(metric.Handler)
        return ok
    },
    InferType: func(cp proto.Message, tEvalFn template.TypeEvalFn) (proto.Message, error) {
    		......
    }
    SetType: func(types map[string]proto.Message, builder adapter.HandlerBuilder)
    		......
    }
    DispatchReport: func(ctx context.Context, handler adapter.Handler, inst []interface{}) error {
    		......
    }
    CreateInstanceBuilder: func(instanceName string, param proto.Message, expb *compiled.ExpressionBuilder) (template.InstanceBuilderFn, error) {
    		......
    }
```


我们发现模板中的各个template中的HandlerBuilder interface都是一样的，都是使用的template/template_handler.gen.go文件中的HandlerBuilder，**除了k8s**。

### HandlerBuilder

这里我们说下各个template共同使用的HandlerBuilder接口

```shell
HandlerBuilder interface {
    // SetAdapterConfig gives the builder the adapter-level configuration state.
    SetAdapterConfig(Config)

    // Validate is responsible for ensuring that all the configuration state given to the builder is
    // correct. The Build method is only invoked when Validate has returned success.
    Validate() *ConfigErrors

    // Build must return a handler that implements all the template-specific runtime request serving
    // interfaces that the Builder was configured for.
    // This means the Handler returned by the Build method must implement all the runtime interfaces for all the
    // template the Adapter supports.
    // If the returned Handler fails to implement the required interface that builder was registered for, Mixer will
    // report an error and stop serving runtime traffic to the particular Handler.
    Build(context.Context, Env) (Handler, error)
    
    SetLogEntryTypes(map[string]*Type /*Instance name -> Type*/) # 这个是额外添加的，注意
}
```

在stdio适配器中HandlerBuilder的实现API，如下图所示：

![stdio HandlerBuilder接口实现](https://gewuwei.oss-cn-shanghai.aliyuncs.com/tracelearning/stdio.jpeg)

说明上面的stdio实现了logentry和metric的HandlerBuilder。


接下来，我们看templates与adapters校验的主逻辑

## templates校验adapters的主逻辑

校验主逻辑就是在newServer函数中的这两行代码：

```shell
    tmplRepo := template.NewRepository(a.Templates) 
    adapterMap := config.AdapterInfoMap(a.Adapters, tmplRepo.SupportsTemplate)
```

### Repository接口

>  tmplRepo := template.NewRepository(a.Templates) 

Repository接口用于校验某个adapter是否实现了template定义的HandlerBuilder接口。

```shell
// Repository defines all the helper functions to access the generated template specific types and fields.
Repository interface {
    GetTemplateInfo(template string) (Info, bool)
    SupportsTemplate(hndlrBuilder adapter.HandlerBuilder, tmpl string) (bool, string)
}
```

### templateRepo实现Repository接口

```shell
// templateRepo implements Repository
repo struct {
    info map[string]Info

    allSupportedTmpls  []string
    tmplToBuilderNames map[string]string
}

// 首先通过NewRepository方法创建一个Repository实例
func NewRepository(templateInfos map[string]Info) Repository {
	 ......

    allSupportedTmpls := make([]string, len(templateInfos))
    tmplToBuilderNames := make(map[string]string)

    for t, v := range templateInfos {
    	  // templates中的map key是模板名，是templates与adapters联系的唯一方式。
    	  // 把所有的模板名称全部添加到allSupportedTmpls列表中
        allSupportedTmpls = append(allSupportedTmpls, t)
        // 同时把模板名与HandlerBuilder接口名称映射起来，作用后面再看。
        // 其实HandlerBuilder接口名称就是在模板名称后面，添加".HandlerBuilder"，就变成了HandlerBuilder接口名
        tmplToBuilderNames[t] = v.BldrInterfaceName
    }
    // 然后再把传入的参数templateInfos保存起来，共templates与adapters校验使用
    return repo{info: templateInfos, tmplToBuilderNames: tmplToBuilderNames, allSupportedTmpls: allSupportedTmpls}
}

// 通过模板名获取template.Info信息，然后再通过BuilderSupportsTemplate方法，就可以校验adapter是否实现了template中的HandlerBuilder接口了。
func (t repo) GetTemplateInfo(template string) (Info, bool) {
    if v, ok := t.info[template]; ok {
        return v, true
    }
    return Info{}, false
}

// 该SupportsTemplate方法结合了上面的GetTemplateInfo方法，校验adapters是否实现了template中的HandlerBuilder接口。
func (t repo) SupportsTemplate(hndlrBuilder adapter.HandlerBuilder, tmpl string) (bool, string) {
	 // 获取指定模板名的template.Info信息
    i, ok := t.GetTemplateInfo(tmpl)
    if !ok {
        return false, fmt.Sprintf("Supported template %v is not one of the allowed supported templates %v", tmpl, t.allSupportedTmpls)
    }

	 // 然后再通过获取的template.Info调用其中的BuilderSupportsTemplate方法，校验传入的adapter HandlerBuilder是否实现了template的HandlerBuilder接口。
    if b := i.BuilderSupportsTemplate(hndlrBuilder); !b {
        return false, fmt.Sprintf("HandlerBuilder does not implement interface %s. "+
            "Therefore, it cannot support template %v", t.tmplToBuilderNames[tmpl], tmpl)
    }

    return true, ""
}
```

### 对adapters进行HandlerBuilder校验

通过Repository接口的实现者templateRepo，我们就可以验证每一个adapter中自己声明实现了SupportTemplates列表，到底实现了这些对应的template没。接下来，我们再看看第二行代码

>  adapterMap := config.AdapterInfoMap(a.Adapters, tmplRepo.SupportsTemplate)

该方法传入所有adapters，以及校验每个adapters是否满足template HandlerBuilder接口的函数值。这里需要对大家的闭包有所理解

该函数返回一个map[string]*adapter.Info, 它表示每个adapter名称对应的adapter.Info

```shell
// 校验adapter的入口，并返回每个适配器对应的适配器相关信息
func AdapterInfoMap(handlerRegFns []adapter.InfoFn,
    hndlrBldrValidator handlerBuilderValidator) map[string]*adapter.Info {
    return newRegistry(handlerRegFns, hndlrBldrValidator).adapterInfosByName
}

func newRegistry(infos []adapter.InfoFn, hndlrBldrValidator handlerBuilderValidator) *adapterInfoRegistry {
    r := &adapterInfoRegistry{make(map[string]*adapter.Info)}
    for idx, info := range infos {
        log.Debugf("registering [%d] %#v", idx, info)
        // 通过info方法，返回adapter.Info
        adptInfo := info()
        // 如果传入的adapters中存在相同的适配器名称，则重复，直接程序panic, 不允许同时对接两个相同的适配器
        if a, ok := r.adapterInfosByName[adptInfo.Name]; ok {
        	  ......
        } else {
            ......
            // 对于每一个adapter，通过Repository接口实例templateRepo，验证是否都实现了template中对应的HandlerBuilder接口。
            // 注意：每一个adapter可以对应多个template，所以doesBuilderSupportsTemplates存在遍历
            if ok, errMsg := doesBuilderSupportsTemplates(adptInfo, hndlrBldrValidator); !ok {
                // panic if an Adapter's HandlerBuilder does not implement interfaces that it says it wants to support.
                msg := fmt.Sprintf("HandlerBuilder from adapter %s does not implement the required interfaces"+
                    " for the templates it supports: %s", adptInfo.Name, errMsg)
                log.Error(msg)
                panic(msg)
            }

			  // 适配器添加到map中。一起返回
            r.adapterInfosByName[adptInfo.Name] = &adptInfo
        }
    }
    return r
}

// 校验指定的适配器，是否满足自己指定的SupportedTemplates列表
func doesBuilderSupportsTemplates(info adapter.Info, hndlrBldrValidator handlerBuilderValidator) (bool, string) {
	 // 适配器中的NewBuilder方法，用于构建HandlerBuilder实例，作为template.Info中BuilderSupportsTemplate的传入参数
    handlerBuilder := info.NewBuilder()
    resultMsgs := make([]string, 0)
    for _, t := range info.SupportedTemplates {
    	  // 遍历适配器中的支持模板列表，并通过模板中的BuilderSupportsTemplate方法进行HandlerBuilder接口校验，如果通过，则表示adapter确实支持该template
        if ok, errMsg := hndlrBldrValidator(handlerBuilder, t); !ok {
            resultMsgs = append(resultMsgs, errMsg)
        }
    }
    if len(resultMsgs) != 0 {
        return false, strings.Join(resultMsgs, "\n")
    }
    return true, ""
}
```

## 总结

由此，我们可以完整地看到每一个适配器自己声明并支持的模板列表，到底是不是真的适配template。同时通过这个适配器stdio与支持的两个模板logentry和metric，也大概知道了整个适配过程。
