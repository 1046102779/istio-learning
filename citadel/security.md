## 安全

微服务的安全需求：

1. 为了抵御中间人攻击，需要流量加密；
2. 为了提供灵活的服务访问控制，需要双向TLS和细粒度的访问策略；
3. 要审核谁在什么时候做了什么，需要审计工具。

针对以上微服务的安全需求，istio提供了安全目标：

1. **默认安全**：应用程序代码和基础结构无需更改；
2. **深度防御**: 与现有安全系统集成，提供多层防御；
3. **零信任**：在不受信任的网络上构建安全解决方案。

Istio的Citadel用加载Secret卷的方式在Kubernetes container中完成证书和秘钥的分发。

Istio中的安全性涉及的组件：

1. **Citadel**用于秘钥和证书管理；
2. **Sidecar和周边代理**实现客户端和服务器之间的安全通信；
3. **Pilot**将授权策略和安全命名信息分发给代理；
4. **Mixer**管理授权和审计

由上可以知道安全是与istio和sidecar的所有组件都有关联。

istio身份

在客户端，根据安全命名信息检查服务器的标识，以查看它是否是该服务的授权运行程序。在服务器端，服务器可以根据授权策略确定客户端可以访问哪些信息，审核谁在什么时间访问了什么，根据服务向客户收费他们使用并拒绝任何未能支付账单的客户访问服务。

这里面说了三个概念：

1. 服务绑定一个身份A，如果客户端带有的身份不是A，则不允许访问该服务；
2. 提供RBAC策略，根据身份访问相关资源；并记录访问的时间和资源日志；
3. 根据身份和服务资源进行计费服务；

在istio身份模型中，Istio使用一流的服务标识来确定服务的身份。这为表示用户，单个服务或一组服务提供了极大的灵活性和粒度。

对于kubenetes的istio采用的服务标识：**service account**

SPIFFE提供了一个框架规范，该框架能够跨异构环境引导和向服务发布身份。

istio和SPIFFE共享相同的身份文件：SVID（SPIFFE可验证身份证件）。例如，k8s中，X.509证书中的URI字段格式为`spiffe://<domain>/ns/<namespace>/sa/<serviceaccount>`。这使istio服务能够建立和接收与其他SPIFFE兼容系统的连接。


Istio PKI建立在Istio Citadel之上，后者用于秘钥和证书的管理。前者可为每个工作负载安全地提供强大的工作负载标识。PKI还可以大规模自动化秘钥和证书轮换。


针对以上提供的异构环境的证书协同，istio在k8s上提供了一套证书秘钥配置机制：

1. Citadel监视k8s apiserver， 为每个现有和新的服务账户创建SPIFFE证书和秘钥对。Citadel将证书和秘钥对存储为K8s secret。
2. 创建pod时，k8s会根据其服务账户通过k8s secret volume将证书和秘钥对挂载到pod中；
3. citadel监视每个证书的生命周期，并通过重写k8s秘密自动轮换证书；
4. Pilot生成安全命名信息，该信息定义了哪些Service Account可以运行哪些服务。Pilot然后将安全命名信息传递给envoy sidecar。

上述说得非常清楚，首先Citadel为每个service account创建SPIFFE证书和秘钥对，并以secret形式挂在到对应的pod上（然后citadel会监视并轮换即将过期证书），最后Pilot生成安全命名信息，配置pod与service account，并将安全命名信息传递给sidecar，至此，通过安全命名信息，我们就可以找到访问服务的身份是否可以访问pod。

## 认证

Istio提供两种类型的身份验证：

1. 传输身份验证，也成为服务到服务身份验证。Istio提供双向TLS的完整解决方案。
      (1). 为每个服务提供强大的身份，表示其角色，已实现跨集群和云的互操作性；
      (2). 保护服务到服务通信和最终用户到服务通信；
      (3). 提供秘钥管理系统，以自动执行秘钥和证书生成，分发和轮换；

2. 来源身份认证，也称为最终用户身份验证，比如：mixer后端check类型的adapter。进行权限认证；

在这两种情况下，Istio都通过自定义K8s API将身份认证策略存储在Istio配置存储中。Pilot会在适当的时候为每个代理保持最新状态和秘钥。

### 双向TLS认证

对客户端调用服务端认证遵循的步骤：

1. Istio将客户端的出站流量重新路由到本地的sidecar；
2. 客户端sidecar和服务端sidecar机那里一个双向TLS连接，Istio将流量从sidecar转发到服务器sidecar；
3. 授权后，服务器端sidecar通过本地TCP连接将流量转发到本地的service中；

### 安全命名

安全命名上面说过安全命名和k8s中的service account进行绑定的，我们通过后者就可以获取对应的角色ClusterRole/Role以及角色所绑定的资源：ClusterRoleBinding/RoleBinding (resources)


其他还包括：认证策略、策略存储范围和目标选择器，以及传输认证、来源身份认证、主认证绑定和更新认证策略

## 授权和鉴权

Istio的授权功能也成为基于角色的访问控制RBAC。为Istio的服务提供命名空间级别、服务级别和方法级别的访问控制，它的特点是：

1. 基于角色的语义，简单易用；
2. 服务间和最终用户对服务的授权；
3. 通过自定义属性支持的灵活性；
4. 高性能，在sidecar本地执行；


对于授权模式，其实就是基于安全命名和service account，以及role和rolebinding几个k8s的资源对象进行RBAC管理资源的。

授权策略分为两种，分别是全局授权配置和个别策略配置。

以下示例，在全局配置级别上设置了 Istio 授权许可模式。

```shell
apiVersion: "rbac.istio.io/v1alpha1"
kind: RbacConfig
metadata:
  name: default
spec:
  mode: 'ON_WITH_INCLUSION'
  inclusion:
    namespaces: ["default"]
  enforcement_mode: PERMISSIVE
```

以下示例，在策略级别上设置了 Istio 授权许可模式。

```shell
apiVersion: "rbac.istio.io/v1alpha1"
kind: ServiceRoleBinding
metadata:
  name: bind-details-reviews
  namespace: default
spec:
  subjects:
    - user: "cluster.local/ns/default/sa/bookinfo-productpage"
  roleRef:
    kind: ServiceRole
    name: "details-reviews-viewer"
  mode: PERMISSIVE
```

上面两个全局配置和策略级别配置分别对应着启用授权和授权策略，前者是istio内部唯一的一个对象资源，后者是针对RBAC进行资源配置；

### 启用授权

我们可以使用RbacConfig对象启用Istio Authorization。RbacConfig对象在Istio内部是一个单例，其名称固定位default。我们只能在Istio中使用一个RbacConfig实例

与其他Istio配置对象一样，RbacConfig被定义为Kubernetes CustomResourceDefinition(CRD)对象。

在RbacConfig对象中，运算符可以指定mode值，它可以是：

1. **OFF**: 禁用istio授权；
2. **ON**: 为网格中的所有服务启用了Istio授权；
3. **ON_WITH_INCLUSION**：仅对**包含**字段中指定的服务和命名空间启用Istio授权；
4. **ON_WITH_EXCLUSION**: 除了**排除**字段中指定的服务和命名空间外，网格中的所有服务都启用了Istio授权；

对于前面设置的全局配置，为default命名空间启用了Istio授权。

### 授权策略

要配置授权策略，请指定ServiceRole和ServiceRoleBinding资源对象。与其他Istio配置一样，它们被定义为K8s CustomResourceDefinition（CRD）对象。

1. ServiceRole定义了一组访问服务的角色；
2. ServiceRoleBinding向特定subjects授予ServiceRole，例如用户、组或服务。

ServiceRole与ServiceRoleBinding组合规定：允许**谁在哪些条件下做什么**。明确地说：

1. **谁**指的是ServiceRoleBinding中的subjects部分；
2. **做什么**指的是ServiceRole中的permissions部分；
3. **哪些条件**指的是你可以在ServiceRole或者ServiceRoleBinding中使用Istio属性指定的conditions部分；

#### ServiceRole

ServiceRole规范包括规则、权限列表。每条规则都有以下标准字段：

1. services： 服务名称列表。我们可以将值设置为"*",表示指定命名空间下的所有服务；
2. methods：HTTP方法名称列表，对于gRPC请求的权限，HTTP请求的动词始终是POST。我们可以将值设置为"*", 表示所有方法；
3. paths：HTTP路径或者gRPC方法。gRPC方法必须采用`/packageName.serviceName/methodName`的形式，并且区分大小写；(备注：这个我们在mix server的grpc调用时发现过，例如：)

```shell
## 我们在demo中template生成的client，paths路径就是/person.HandlePersonService/HandlePerson
func (c *handlePersonServiceClient) HandlePerson(ctx context.Context, in *HandlePersonRequest, opts ...grpc.CallOption) (*istio_mixer_adapter_model_v1beta11.CheckResult, error) {
    out := new(istio_mixer_adapter_model_v1beta11.CheckResult)
    err := grpc.Invoke(ctx, "/person.HandlePersonService/HandlePerson", in, out, c.cc, opts...)
    if err != nil {
        return nil, err
    }
    return out, nil
}
```

ServiceRole中指定的规则、权限列表，仅仅适用于metadata中指定的命名空间。下面显示了一个可以匹配任何规则的ServiceRole, 角色名：service-admin，其实就是没啥用

```shell
apiVersion: rbac.istio.io/v1alpha1
kind: ServiceRole
metadata:
	name: service-admin
	namespace: default
spec:
	rules:
	- services: ["*"]
	  methods: ["*"]
```	

下面这个例子显示了角色名：products-viewer，并对default命名空间下的products.default.svc.cluster.local服务具有HEAD和GET权限的ServiceRole。

```shell
apiVersion: "rbac.istio.io/v1alpha1"
kind: ServiceRole
metadata:
  name: products-viewer
  namespace: default
spec:
  rules:
  - services: ["products.default.svc.cluster.local"]
    methods: ["GET", "HEAD"]
```

我们支持规则中所有字段的前缀匹配和后缀匹配, 比如：`*-viewers, viewers-*`

在ServiceRole资源对象中，namespace+services+methods+paths定义了如何访问服务

除了上面定义的services、methods和paths之外，还定义了一个约束constraints条件, headers的key=version，value=v1或者v2。

```shell
piVersion: "rbac.istio.io/v1alpha1"
kind: ServiceRole
metadata:
  name: products-viewer-version
  namespace: default
spec:
  rules:
  - services: ["products.default.svc.cluster.local"]
    methods: ["GET", "HEAD"]
    constraints:
    - key: request.headers[version]
      values: ["v1", "v2"]
```

#### ServiceRoleBinding

ServiceRoleBinding规范包括两部分：

1. roleRef指的是同一命名空间中的ServiceRole资源；
2. subjects分配给角色的列表。

其实用户角色绑定。

注意每个subject都有user、properties，其properties使用固定属性，做key-value列表赋值给ServiceRole


虽然我们强烈建议使用 Istio 授权机制，但 Istio 足够灵活，允许您通过 Mixer 组件插入自己的身份验证和授权机制。 要在 Mixer 中使用和配置插件，请访问我们的策略和遥测适配器文档。
