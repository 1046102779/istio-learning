该demo主要是讲解自己写一个template和一个adapter，并在本地跑起这个demo来。

template主要用于定契约，描述三个事实, 其实第三点可以归纳到第一点：
1. template该模板提供的服务类型，且每个模板只能提供一种服务，服务种类有：CHECK、QUOTA、REPORT和ATTRIBUTE_GENERATOR四类；
2. 提供数据模型，也就是这个模板主要是针对哪些数据进行处理，比如：指标类型数据，日志类型数据，监控类型数据、鉴权类型数据、配额类型数据等等；
3. 其他的则是一些针对(1)的grpc service接口定义。其目的就是为了让支持该模板的adapter实现这些service接口定义；

## demo演示

```shell
# terminal01, 启动mix server服务，指定配置文件地址
mixs server --configStoreURL=fs://$(pwd)/testdata

# terminal02, 启动适配器服务grpc server
./l04
print person request data: xhj, 31, clever01

# termial03, 通过mix客户端命令发送一个check请求给mixer server。然后就出现了terminal02打印的日志信息
mixc check --string_attributes destination.owner=xhj,destination.container.name=clever01 --int64_attributes destination.port=31
```

## 遇到的问题

在编写自定义的template时，遇到了一些问题：

1. template编写template.proto协议文件时，数据模型定义的变量名不能使用name，该名已经被template自身占用。
2. template编写template.proto协议文件，还需要注意一点，需要相关写一些注释，和yaml注释文件，否则在生成文件时，会报一些"no comment"的信息。虽然不影响业务逻辑，但是最好是写上;


在编写自定义的adapter时，同样也遇到了一些问题：


其他问题是在发起mixc check客户端调用或者mixs server启动时出现的，比如：

1. `* rpc error: code = Internal desc = grpc: error unmarshalling request: unexpected EOF`
2. `config does not conform to schema of template 'person': unable to encode fieldEncoder email_address: destination.container.name | "cdh_cjx@163.com". unable to build primitve encoder for:email_address destination.container.name | "cdh_cjx@163.com". unknown attribute destination.container.name`
3. `error creating instance: destination='person.template.istio-system:h1.handler.istio-system
(myperson.adapter.istio-system)', error='fieldEncoder: age - lookup failed: 'destination.port''`
4. `field 'age' not found in message 'Params'`
5. `field 'age' is of type 'string' instead of expected type 'int'`
6. `panic: Unknown map type string`

针对`unknown attribute xxx`标签的错误，一般都是在标签属性文件中没有定义这类标签，需要添加attributes.yaml文件中

针对"expected type int"的错误，这里重点介绍下：

```shell
我们知道对于每一个template定义，都有固定的服务种类支持，比如：check, quota, report, generate_attributes四类，那么对于本demo实例，template是定义的check服务类型，那么对应实现的adapter，则是对envoy proxy发过来器的grpc client请求进行check数据校验，主要是指鉴权类的校验.

那么运维在写kind为handler的这类对针对check服务类型的配置时，其中的spec部分的params参数，则主要是鉴权类型的数据值，比如ACL、token等数据。

注意：是具体的数据，因为这会对grpc client发送过来的数据处理后，并与adapter指定的数据(kind: handler对象资源下的spec下的params数据)进行比对。

因为mixer server是动态watch配置的，所以配置是可以动态修改的。这个就解决了check类型数据校验的动态变化。
```

所以针对上面错误的解决方案，就是我的`operator_cfg.yaml`配置kind：handler的params应该是具体数据，而不是什么`age: destination.port | 31`.

针对`panic: Unknown map type string`错误，一般都是mixc客户端的命令写得有错误。

其他问题有待考究。

## 编写template

我们创建一个person模板，用于处理所有与人相关的基本信息, 包括用户名、年龄和email

> cd $GOPATH/src/istio.io/istio/mixer/template

> mkdir person && cd person
> cat template.proto

```shell
syntax = "proto3";

// Example config:
//
//```shell
//apiVersion: "config.istio.io/v1alpha2"
//kind: person
//metadata:
//   name: person
//   namespace: istio-system
//spec:
//   owner: destination.owner | "guest"
//   age: destination.port | "24"
//   email_address: destination.labels["email_address"] | "cdh_cjx@163.com"
//```
package person;

//import "policy/v1beta1/type.proto";
import "mixer/adapter/model/v1beta1/extensions.proto";

option (istio.mixer.adapter.model.v1beta1.template_variety) = TEMPLATE_VARIETY_CHECK;

// The `person` template represents person info key, used to authorize API calls.
message Template {
    // The owner being called (destination.owner).
    string  owner = 1;
    // The age being called (destination.port).
    int64 age = 2;
    // The email_address being called (destination.labels["email_address"]).
    string email_address = 3;
}
```

> $GOPATH/src/istio.io/istio/bin/mixer_codegen.sh -t template.proto

> ls 

```shell
person.pb.html                          template_handler.gen.go                 template_handler_service.proto
template.proto                          template_handler_service.descriptor_set template_proto.descriptor_set
template.yaml                           template_handler_service.pb.go
```

## 编写adapter



> cd ../../adapter
> mkdir myperson && cd myperson
>  ## 下面myperson.go用于提供grpc server服务，对于该check服务类型，则主要是用户基本信息校验，不通过返回给mixc客户端相应的错误码和错误信息
>  ## 而config目录则主要用于认证校验grpc client发送过来的数据。也就是说config中存储的是目标数据模型
> mkdir config && touch myperson.go
> cat myperson.go

```shell
package myperson

import (
        "context"
        "fmt"
        "net"

        google_rpc "github.com/gogo/googleapis/google/rpc"
        "google.golang.org/grpc"
        istio_mixer_adapter_model_v1beta11 "istio.io/api/mixer/adapter/model/v1beta1"
        "istio.io/istio/mixer/adapter/myperson/config"
        "istio.io/istio/mixer/template/person"
)

type (
        Server interface {
                Addr() string
                Close() error
                Run(shutdown chan error)
        }

        MyPerson struct {
                listener net.Listener
                server   *grpc.Server
        }
)
var _ person.HandlePersonServiceServer = &MyPerson{}

func (m *MyPerson) Addr() string {
        return m.listener.Addr().String()
}

func (m *MyPerson) Close() error {
        if m.server != nil {
                m.server.GracefulStop()
        }
        if m.listener != nil {
                m.listener.Close()
        }
        return nil
}

func (m *MyPerson) Run(shutdown chan error) {
        shutdown <- m.server.Serve(m.listener)
}

func (m *MyPerson) HandlePerson(ctx context.Context, req *person.HandlePersonRequest) (
        *istio_mixer_adapter_model_v1beta11.CheckResult, error) {
        fmt.Printf("print person request data: %s, %d, %s\n",
                req.Instance.Owner,
                req.Instance.Age,
                req.Instance.EmailAddress,                                                                                
        )
        fmt.Println("print adapter info.....")
        cfg := &config.Params{}
        if err := cfg.Unmarshal(req.AdapterConfig.Value); err != nil {
                panic(err.Error())
        }
        fmt.Printf("print person adapter data: %s, %d, %s\n",
                cfg.Owner,
                cfg.Age,
                cfg.EmailAddress,
        )
        if req.Instance.Owner == cfg.Owner &&
                req.Instance.Age == cfg.Age &&
                req.Instance.EmailAddress == cfg.EmailAddress {
                return &istio_mixer_adapter_model_v1beta11.CheckResult{}, nil
        }
        return &istio_mixer_adapter_model_v1beta11.CheckResult{
                Status: google_rpc.Status{
                        Code:    40001,
                        Message: "基本信息不匹配",
                },
        }, nil
}


func NewMyPerson(addr string) (Server, error) {
        if addr == "" {
                addr = "127.0.0.1:4001"
        }
        listener, err := net.Listen("tcp", fmt.Sprintf("%s", addr))
        if err != nil {
                return nil, err
        }
        s := &MyPerson{
                listener: listener,
        }
        fmt.Printf("grpc://%s\n", addr)
        s.server = grpc.NewServer()
        person.RegisterHandlePersonServiceServer(s.server, s)
        return s, nil
}
```

> cd config && touch config.proto

> cat config.proto

```shell
syntax="proto3";

// config for myperson
package adapter.myperson.config;

import "gogoproto/gogo.proto";

option go_package="config";

// config for myperson
message Params {
    // Path of the file to save the information about runtime requests.
    string owner = 1;
    // age for person
    int64 age = 2;
    // email_address for person
    string email_address = 3;
}
```

> $GOPATH/src/istio.io/istio/bin/mixer_codegen.sh -a config.proto -x "-s=false -n myperson -t person"

> ls

```shell
adapter.myperson.config.pb.html config.proto                    myperson.yaml
config.pb.go                    config.proto_descriptor
```

**注意事项：**

1. config目录下的其他文件动态生成，是需要myperson.go支持的，因为adpater动态生成的配置文件myperson.yaml中kind: adapter在spec部分是需要指定templates的。
2. 按照(1), 在编写myperson.go文件时，首先注释掉HandlePerson方法中的函数体，因为myperson.go编译需要能通过。然后等config目录下的文件生成后，在补充实现grpc server的service方法

## 测试集实施

> cd $GOPATH/src/istio.io/istio/mixer/adapter/myperson
> 
> mkdir testdata && touch operator_cfg.yaml
> 
> cp config/myperson.yaml testdata/
> 
> cat operator_cfg.yaml

```shell
# handler for adapter myperson
apiVersion: "config.istio.io/v1alpha2"
kind: handler
metadata:
 name: h1
 namespace: istio-system
spec:
 adapter: myperson
 connection:
     address: "127.0.0.1:4001" #replaces at runtime by the test
 params:
   owner: "donghai"
   age: 30
   email_address: "cdh_cjx@163.com"
---

# instance for template metric
apiVersion: "config.istio.io/v1alpha2"
kind: instance
metadata:
 name: i1
 namespace: istio-system
spec:
 template: person
 params:
   owner: destination.owner | "donghai"
   age: destination.port | 31
   email_address: destination.labels["email_address"] | "cdh_cjx@163.com"
---

# rule to dispatch to handler h1
apiVersion: "config.istio.io/v1alpha2"
kind: rule
metadata:
 name: r1
 namespace: istio-system
spec:
 actions:
 - handler: h1.istio-system
   instances:
   - i1
---
```

> cp operator_cfg.yaml testdata/
>
> cp ../../template/person/template.yaml testdata/
>
> cp ../../testdata/config/attributes.yaml testdata/
> 
>  ## 注意需要在attributes.yaml文件中追加几个属性词汇

```shell
      destination.port:
        valueType: INT64
```

如果发现在启动mixer server服务时，报其他属性词汇不存在，则在该文件中继续添加。

**运行结果：**

当mixc check传输的属性标签名与值不等于设定的目标值时，返回值：

mixc check --string_attributes destination.owner=donghai --stringmap_attributes "destination.labels=email_address:cdh_cjx@163.com" --int64_attributes destination.port=31

```shell
Check RPC completed successfully. Check status was Code 40001 (h1.handler.istio-system:基本信息不匹配)
```

表明认证不通过

> mixc check --string_attributes destination.owner=donghai --stringmap_attributes "destination.labels=email_address:cdh_cjx@163.com" --int64_attributes destination.port=30

当相同时，返回值：

```shell
Check RPC completed successfully. Check status was OK
```


## 总结

这个只是本地编写adapter与template，并在本地测试验证。如果要上k8s的话，这里有一个完整的[demo](http://www.servicemesher.com/blog/set-sail-a-production-ready-istio-adapter/)

## 参考文献

1. [教程|构建生产就绪的Istio Adapter](http://www.servicemesher.com/blog/set-sail-a-production-ready-istio-adapter/)
2. [Mixer Out Of Process Adapter Dev Guide](https://github.com/istio/istio/wiki/Mixer-Out-Of-Process-Adapter-Dev-Guide)
3. [Mixer Out of Process Adapter Walkthrough](https://github.com/istio/istio/wiki/Mixer-Out-Of-Process-Adapter-Walkthrough)
4. [k8s权限认证rbac基础问题分析和解决思路记录](http://blog.51cto.com/goome/2170196)

## 花絮

我们在使用[教程|构建生产就绪的Istio Adapter](http://www.servicemesher.com/blog/set-sail-a-production-ready-istio-adapter/)在k8s上部署adapter时，可能会遇到一个问题：

```shell
Error from server (Forbidden): Forbidden (user=kubernetes, verb=get, resource=nodes, subresource=proxy) ( pods/log myperson-bf7ffdd7c-tvqvh)
```

这个主要是user=kubernetes的subjects没有RBAC权限，需要添加创建角色和绑定角色操作。步骤如下：

[k8s@kube-node2 ~]$ cat config/roles.yaml

```shell
# 创建kube-apiserver角色, 并可以通过[get, create]动作，访问nodes/proxy和nodes/metrics两类资源
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kube-apiserver
  namespace: kube-system
rules:
- apiGroups:
  - ""
  resources:
  - nodes/proxy
  - nodes/metrics
  verbs:
  - get
  - create
```

# 把kubernetes用户绑定到kube-apiserver角色上，即可
[k8s@kube-node2 ~]$ cat config/clusterrolebindings.yaml

```shell
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kube-apiserver
  namespace: kube-system
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kube-apiserver
subjects:
- apiGroup: rbac.authorization.k8s.io
  kind: User
  name: kubernetes
```
