istio所有的配置定义总体结构：
```shell
apiVersion: 
kind:
metadata:
spec:
```

## VirtualService 

VirtualService定义了控制在 Istio 服务网格中如何路由服务请求的规则.

对于pilot模块的VirtualService对象资源，其spec部分的http定义包括：

```shell
route:
	- destination:
		host: 
		subset: 
retries:
	attempts: 3
	perTryTimeout: 2s
timeout: 10s
fault:
	-delay:
		percent:
		fixedDelay: 5s
	-abort:
		percent: 
		httpStatus: 400
match:
	-sourceLabels:
		app: 
		version:
```

VirtualService对象资源的条件规则：

1. 使用工作负载 label 限制特定客户端工作负载;
2. 根据 HTTP Header 选择规则;
3. 根据请求URI选择规则.

```shell
## 对于第一种label限制，用于匹配pod中的label标签"app=reviews"的service
spec:
	http:
	- match:
		- headers:
			souceLabels:
				app: reviews

## 对于第二种header限制，用于匹配header中"end-user=jason"的service
spec:
	http:
	- match:
		- headers:
			end-user:
				exact: jason
				
## 对于第三种uri限制，用于匹配URI以"/api/v1"开头的service
spec:
	http:
	- match:
		- uri:
			prefix: /api/v1
```

## DestinationRule

在请求被 VirtualService 路由之后，DestinationRule 配置的一系列策略就生效了。这些策略由服务属主编写，包含断路器、负载均衡以及 TLS 等的配置内容。

也就是说，针对每个版本的服务，制定提供服务的策略，包括：熔断器、负载均衡和TLS配置等。或者流控

评估规则：

1. 被选中的 subset 如果定义了策略，就会开始是否生效的评估；


```shell
## 下面这个DestinationRule对象资源，定义了三个reviews的服务版本，全局采用随机算法，但是v2版本采用轮训策略
apiVersion: networking.istio.io/v1alpha3
kind: DestinationRule
metadata:
	name: reviews
spec:
	host: reviews
	trafficPolicy:
		loadBalancer:
			simple: RANDOM
	subsets:
	- name: v1
	  labels:
	  		version: v1
	- name: v2
	  labels:
	  		version: v2
	  	trafficPolicy:
	  		loadBalancer:
	  			simple: RANDOM_ROBIN
	- name: v3
	  labels:
	  		version: v3
```

对于某个版本的服务增加熔断器配置, 对于v1版本，限制并发连接100个

```shell
apiVersion: networking.istio.io/v1alpha3
kind: DestinationRule
metadata:
	name: reviews
spec:
	host: reviews:
	subsets:
	- name: v1
	  labels:
	  		version: v1
	  	trafficPolicy:
	  		connectionPool:
	  			tcp:
	  				maxConnections: 100
```

**a tip**：如果没有定义VirtualService路由规则，只定义了DestinationRule目标规则，那么subsets下的策略是不生效的，因为没有路由规则，则到目标规则，则使用全局的策略，没有的话，则使用底层的缺省路由。

举个例子：

```shell
## 如果这个reviews没有VirtualService路由规则，则v1版本的最大并发数100个不会生效。它会使用底层缺省路由，直接v1
apiVersion: networking.istio.io/v1alpha3
kind: DestinationRule
metadata:
	name: reviews
spec:
	host: reviews
	subsets:
	-  name: v1
		labels:
			version: v1
		trafficPolicy:
			connectionPool:
				tcp:
					maxConnections: 100
```

要生效的方法有两个：

1. 定义v1版本的策略为全局。缺点：如果存在多个版本，则不合适；
2. 定义VirtualService路由规则，最好的解决方案

具体如下所示：

```shell
apiVersion: networking.istio.io/v1alpha3
kind: DestinationRule
metadata:
  name: reviews
spec:
  host: reviews
  trafficPolicy:
    connectionPool:
      tcp:
        maxConnections: 100
  subsets:
  - name: v1
    labels:
      version: v1
```

```shell
apiVersion: networking.istio.io/v1alpha3
kind: VirtualService
metadata:
  name: reviews
spec:
  hosts:
  - reviews
  http:
  - route:
    - destination:
        host: reviews
        subset: v1
```

**从一开始就给每个服务设置缺省规则，是 Istio 世界里推荐的最佳实践。**


## ServiceEntry

首先我们介绍一下，因为所有自动或者手动注入的sidecar，都会在底层通过iptables进行流量拦截，包括：出口和入口。这样service想访问pod或者container外面的流量，则会被sidecar拦截掉，导致无法访问外网被拒绝。这个时候就需要一个白名单。这个就是ServiceEntry的由来。

官方解释ServiceEntry：

```shell
Istio 内部会维护一个服务注册表，可以用 ServiceEntry 向其中加入额外的条目。通常这个对象用来启用对 Istio 服务网格之外的服务发出请求。
```

```shell
# 例如下面的 ServiceEntry 可以用来允许外部对 *.foo.com 域名上的服务主机的调用。
apiVersion: networking.istio.io/v1alpha3
kind: ServiceEntry
metadata:
  name: foo-ext-svc
spec:
  hosts:
  - *.foo.com
  ports:
  - number: 80
    name: http
    protocol: HTTP
  - number: 443
    name: https
    protocol: HTTPS
```

ServiceEntry定义的外网服务，VirtualService路由规则和DestinationRule目标规则同样适用于它。只是外网服务没有版本概念，所以不能使用权重路由策略。

## Gateway

Gateway对象资源用于定义service mesh中某个服务的流量入口, 比如在流量要进入到内部gateway服务，那需要一个Gateway资源对象把流量引入到这个内部的gateway服务中来。

官方解释Gateway：

```shell
Gateway 为 HTTP/TCP 流量配置了一个负载均衡，多数情况下在网格边缘进行操作，用于启用一个服务的入口（ingress）流量。

与k8s Ingress不同，Istio gateway只配置到四层到六层的功能（例如：开放端口或者TLS配置）。绑定一个VirtualService到Gateway上，用户就可以使用标准的Istio规则来控制进入的HTTP和TCP流量
```

下面我们看一个Gateway与内部gateway的互相绑定。

```shell
# 定义Gateway对象资源，并定义host为bookinfo.com，发送到该Gateway的请求，都会转发给绑定到该Gateway的所有内部service。如果Gateway与内部的gateway一一绑定，则直接绑定到该内部绑定的gateway。
apiVersion: networking.istio.io/v1alpha3
kind: Gateway
metadata:
	name: bookinfo-gateway
spec:
	servers:
	-  port:
		number: 443
		name: https:
		protocol: HTTPS
	hosts:
	- bookinfo.com
	tls:
		mode: SIMPLE
		serverCertificate: /tmp/tls.crt
		privateKey: /tmp/tls.key
```

```shell
# 定义内部的gateway服务(bookinfo)。并绑定到bookinfo-gateway这个网关，当路由的uri为/reviews时，进行route路由规则
apiVersion: networking.istio.io/v1alpha3
kind: VirtualService
metadata:
	name: bookinfo
spec:
	hosts:
	-  bookinfo.com
	gateways:
	- bookinfo-gateway # <---- 绑定到Gateway
	http:
	- match
	  - uri:
	  	  	prefix: /reviews
	  route:
	  ...
```

上面这个例子的互相绑定，说明只要流入Gateway外部网关的流量，全部会留到bookinfo这个内部的gateway服务。
