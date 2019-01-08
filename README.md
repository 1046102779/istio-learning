# istio源码阅读分析

## mixer

1. [mixer server实例初始化](mixer/arch.md)
2. [校验内置的adapters与templates的HandlBuilder接口关系](mixer/adapters-and-templates.md)
3. [后端存储配置的client初始化](mixer/backend-store.md)
4. [runtime环境的初始化，非常重要](mixer/runtime.md)
5. [mixc调用mixs的处理流程](mixer/envoy-proxy-call-grpcclient.md)
6. [手把手编写template和adapter](mixer/demo)

## pilot

## etcd v3实用命令

```shell
# 列出所有的key, 
#  1. --keys-only=true, 只显示key。不显示对应的value值;
#  2. --limit=100, 列出的key数量限制在1000
ETCDCTL_API=3 etcdctl get / --limit=1000 --prefix --keys-only=true > tmp

# 列出所有与mixer server相关配置文件的keys
ETCDCTL_API=3 etcdctl --prefix=true get /registry/config.istio.io --keys-only=true

# 列出自定义的adapter={myperson}
ETCDCTL_API=3 etcdctl get /registry/config.istio.io/adapters/istio-system/myperson

# 列出自定义的template={person}
ETCDCTL_API=3 etcdctl get /registry/config.istio.io/templates/istio-system/person
```

下面展示自定义template与adapter作为配置文件存储在k8s中的所有相关key

```shell
/registry/config.istio.io/adapters/istio-system/myperson
/registry/config.istio.io/attributemanifests/istio-system/istio-proxy
/registry/config.istio.io/attributemanifests/istio-system/istioproxy
/registry/config.istio.io/attributemanifests/istio-system/kubernetes
/registry/config.istio.io/handlers/istio-system/h1
/registry/config.istio.io/instances/istio-system/i1
/registry/config.istio.io/kubernetesenvs/istio-system/handler
/registry/config.istio.io/kuberneteses/istio-system/attributes
/registry/config.istio.io/logentries/istio-system/accesslog
/registry/config.istio.io/logentries/istio-system/tcpaccesslog
/registry/config.istio.io/metrics/istio-system/requestcount
/registry/config.istio.io/metrics/istio-system/requestduration
/registry/config.istio.io/metrics/istio-system/requestsize
/registry/config.istio.io/metrics/istio-system/responsesize
/registry/config.istio.io/metrics/istio-system/tcpbytereceived
/registry/config.istio.io/metrics/istio-system/tcpbytesent
/registry/config.istio.io/prometheuses/istio-system/handler
/registry/config.istio.io/rules/istio-system/kubeattrgenrulerule
/registry/config.istio.io/rules/istio-system/promhttp
/registry/config.istio.io/rules/istio-system/promtcp
/registry/config.istio.io/rules/istio-system/r1
/registry/config.istio.io/rules/istio-system/stdio
/registry/config.istio.io/rules/istio-system/stdiotcp
/registry/config.istio.io/rules/istio-system/tcpkubeattrgenrulerule
/registry/config.istio.io/stdios/istio-system/handler
/registry/config.istio.io/templates/istio-system/person
```

## 参考文献

[mixer遥测报告](https://segmentfault.com/a/1190000015685943)

## 说明

> 希望与大家一起成长，有任何该服务运行或者代码问题，可以及时找我沟通，喜欢开源，热爱开源, 欢迎多交流
> 联系方式：cdh_cjx@163.com
