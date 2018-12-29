后端配置的动态加载和存储

后端存储配置的更新、新增和删除，都需要在mixer server重新构建一个相对稳定的运行时环境。

后端存储配置的重新构建，主要的方法`processNewConfig`, 它涵盖了yaml资源所有数据类型的加载和构建。

## 属性加载

mixer server支持的固定属性非常多，有时候后端适配器想要自定义属性。那么后端支持的属性列表包括两部分：

1. yaml配置文件中kind为`attributemanifest`的资源，需要全部加载到内存`map[string]*config.AttributeManifest_AttributeInfo`中;
2. 还有一个mixer server系统内部的template种类为TEMPLATE_VARIETY_ATTRIBUTE_GENERATOR的类型, 也需要加载到上面的map存储空间；

加载后map的映射关系：属性名->属性类型

## handler加载

mixer server会加载两种handler到`map[string]*HandlerStatic`内存中。目前支持yaml配置的handler资源有两类：

1. yaml配置文件内kind为handler，则需要通过compiledAdapter找到mixer server支持的适配器名称，并修改默认返回值的数据结构定义；
2. yaml配置文件内还有kind直接为适配器名称的资源，它则不需要通过compiledAdapter指定，直接就是kind值作为适配器名。

这两部分的适配器加载，主要是告诉mixer server目前后端适配器的数量以及返回值的数据结构定义。


利用map存储资源对象的key都是资源的唯一标识。

## instance加载

理论上，mixer server也会加载两种handler到`map[string]*InstanceStatic`内存中。但是对于kind为template，这里spec部分采用了数据序列化协议，没有在这里直接存储，这部分是动态生成的；但是会对后端配置资源entries中能够在mixer server支持的templates列表中找到名称，则就可以进行存储映射。表示后端各个适配器需要的template列表。


## 动态template加载

动态template是指非人为创建的yaml文件，主要是由代码生成的template，每个动态生成的template资源spec部分只有一个字段: descriptor, 并对这个descriptor进行反序列化解析，每个模板都会分类variety。如果这个模板种类variety为TEMPLATE_VARIETY_ATTRIBUTE_GENERATOR，表示这个模板会有动态生成的属性列表. 所以还需要对属性列表进行存储。全部存储在`map[string]*Template`。

## 动态adapter加载

动态adapter是指非人为创建的yaml文件，主要是由代码生成的adapter，每个动态生成的adapter资源spec部分有一个字段：config，并对这个config进行反序列化解析，这个动态adapter是使用动态template的数据模型定义。

注意：动态adapter与动态template是对应的，动态adapter不会映射到静态的template。如果动态生成的adapter资源配置文件中在spec部分指定templates名称列表，在上面动态template加载不存在模板名，则不会存储到`map[string]*Adapter`内存中

## 动态handler加载

动态handler是指非人为创建的yaml文件，主要是由代码生成的handler，每个动态生成的adapter资源在spec部分是没有compiledAdapter字段的，然后再进行反序列
化，并存储在`map[string]*HandlerDynamic`内存中。

## 动态instance加载

动态instance也是非人为创建的yaml文件，主要由代码生成的instance。最后存储到`map[string]*InstanceDynamic`内存中。

## rule加载

rule加载肯定是动态的，因为这个是后端适配器或者运维开发所关心的数据。

rule优先做mixer server内置的HandlerStatic匹配，如果匹配不到，再做动态加载配置后的HandlerDynamic匹配。

匹配完成后，再遍历actions中的template列表，并对handler接口校验，校验handler是否实现了template定义的grpc service服务接口。


## 小结
**动态映射动态资源对象，静态映射静态资源对象。**

**动态资源对象是对远端适配器服务而言的，静态资源对象是对本地适配器服务而言的, 本地适配器服务有20个服务, 本地所以不需要提供端口服务**

而对于k8s, mch的远端服务，则是动态资源对象, 需要在yaml配置文件kind为handler，spec部分connection不能为空，在初始化handler Table时，也就是Table中的entry初始化时，对于动态资源对象会直接创建grpc client连接

总结，现在来理解下配置中的template、adapter、handler、instance、attributemanifest和rule六个概念。

配置中的template与adapter是作为新增的资源对象，在mixer server中已经静态地枚举了默认支持的12个template与20个adapter，同时也支持第三方通过配置的方式新增；

handler设置compiledAdapter为adapter名称，也就间接表示了该handler支持的模板列表；

instance设置spec部分的template名称，也就间接表示了该instance进行实例化所需的template；

通过handler和instance则把mixer server作为客户端，通过grpc client调用后端真正服务的服务名和请求参数，都准备好了。
