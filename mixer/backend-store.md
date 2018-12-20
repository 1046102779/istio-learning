# mixer server后端存储

所有的yaml配置文件都需要存储，包括：Handler、Template和DestinationRule所有相关配置，也就是所有mixer server后端对接的所有适配器。

目前支持三种后端存储协议：

1. 本地文件系统，协议：fs://
2. MCP协议，协议：mcp://, MCP协议资料，详见: [Mesh Config Protocal](https://gewuwei.oss-cn-shanghai.aliyuncs.com/tracelearning/Mesh%20Configuration%20Protocol%20(MCP).pdf)
3. kubernetes协议，协议：k8s://

接下来，我详细介绍下后端配置存储的作用，以及这三种存储协议的支持和功能。

## 通用的后端配置存储

我们看一个配置DEMO, 文件路径：$GOPATH/src/istio.io/istio/mixer/testdata/config/accesslog.yaml

```shell
apiVersion: "config.istio.io/v1alpha2"
kind: handler
metadata:
  name: stdio
  namespace: istio-system
spec:
  compiledAdapter: stdio
  params:
    outputAsJson: true
---
apiVersion: "config.istio.io/v1alpha2"
kind: logentry
metadata:
  name: accesslog
  namespace: istio-system
spec:
  severity: '"Default"'
  timestamp: request.time | timestamp("2017-01-01T00:00:00Z")
  variables:
    sourceIp: source.ip | ip("0.0.0.0")
    sourceApp: source.labels["app"] | ""
    sourcePrincipal: source.principal | ""
---
apiVersion: "config.istio.io/v1alpha2"
kind: rule
metadata:
  name: stdiohttp
  namespace: istio-system
spec:
  match: context.protocol == "http" || context.protocol == "grpc"
  actions:
  - handler: stdio
    instances:
    - accesslog.logentry
```

可以看到无论是Handler、Template或者DestinationRule，配置文件的组成部分：{apiVersion, kind, metadata{name, namespace, labels, ...}, spec}四部分构成，其中，apiVersion是固定的，都为"config.istio.io/v1alpha2".

对于这三类资源，它的唯一性构成：${metadata.name}.${kind}.${metadata.namespace}。

对应的，我们在`$GOPATH/src/istio.io/istio/mixer/pkg/config/store/store.go`文件中，可以看到这三类配置转化为可以存储的struct结构定义，如下所示：

```shell
// ResourceMeta代表一个资源中的metadata部分
type ResourceMeta struct {
    Name        string
    Namespace   string
    Labels      map[string]string
    Annotations map[string]string
    Revision    string
}

// BackEndResource代表一个唯一的资源，可以是Handler、Template或者DestinationRule资源。
// 对于资源中的spec部分，因为无固定，所以做成的interface类型。
// 
// 对于所有资源的存储，可以通过map[key]*BackEndResource方式去存储，key也就是上面所说的资源唯一性：${metadata.name}.${kind}.${metadata.namespace}
type BackEndResource struct {
    Kind     string
    Metadata ResourceMeta
    Spec     map[string]interface{}
}

// key的唯一性结构
// Key represents the key to identify a resource in the store.
type Key struct {
    Kind      string
    Namespace string
    Name      string
}
```

除了解析并存储所有的资源外，后端存储还提供了访问、监控这些资源的store interface。监控是使用probe探针的方式去监听文件的变化或者通过网络协议监听配置的变化，从而重新生成配置，比如，监听配置写入在k8s etcd的api server。

上面我们理解了配置的资源定义构成，和资源的唯一性定义。接下来，我们开始了解后端存储的接口部分。

> 资源类型与protobuffer协议的转换，我们暂时不考虑。也就是先暂时不考虑远程后端存储，比如: k8s。


```shell
// Backend为mixer定义了无类型存储接口.
type Backend interface {
	 // 初始化mixer server能够处理的kind类型，包括：内置的templates所有名称列表，内置的adapters所有适配器名称，以及rules、instance、handler、template和attributemanifest
	 // 
	 // 如果配置文件中定义了其他不支持的kind，则会忽略，且不报错
    Init(kinds []string) error

	 // 停止监听配置文件的变化
    Stop()

    // WaitForSynced blocks and awaits for the caches to be fully populated until timeout.
    WaitForSynced(time.Duration) error

    // Watch方法用于接收配置的资源变化，并创建或者更新内存配置
    Watch() (<-chan BackendEvent, error)

    // Get方法通过资源唯一性，获取指定的资源内容
    Get(key Key) (*BackEndResource, error)

    // List方法用于返回所有的资源配置， 如果后端适配器资源过多，可能也有性能损耗
    List() map[Key]*BackEndResource
}
```

咋一看，下面的Store和上面Backend没什么不同，仔细看，还是有些不同，用于不同场景。当我们需要使用grpc调用时，就需要把上面的BackEndResource转化成能够传输的协议数据格式protobuffer。所以增加了远程服务调用的相关协议接口。含义与上面相同。

```shell
// Store defines the access to the storage for mixer.
type Store interface {
    Init(kinds map[string]proto.Message) error

    Stop()

    // WaitForSynced blocks and awaits for the caches to be fully populated until timeout.
    WaitForSynced(time.Duration) error

    // Watch creates a channel to receive the events. A store can conduct a single
    // watch channel at the same time. Multiple calls lead to an error.
    Watch() (<-chan Event, error)

    // Get returns the resource to the key.
    Get(key Key) (*Resource, error)

    // List returns the whole mapping from key to resource specs in the store.
    List() map[Key]*Resource

    probe.SupportsProbe
}
```


## store结合Store与Backend接口的一个实现

istio mixer比较有意思的一点是，有一个store把Store与Backend联系起来了，并对外提供统一的入口。这个store是Store接口的一个实现，同时Backend作为store的一个参数，也提供了真正的后端配置存储。

> 后面我们再补充相关内容

```shell
// store是Store接口的一个实现，我们可以看到Backend
type store struct {
    kinds   map[string]proto.Message
    backend Backend

    mu    sync.Mutex
    queue *eventQueue
}

// 后端存储backend，需要实现探针，探活后端存储服务是否健康
func (s *store) RegisterProbe(c probe.Controller, name string) {
    if e, ok := s.backend.(probe.SupportsProbe); ok {
        e.RegisterProbe(c, name)
    }
}

// Stop方法，用于关闭事件队列的监听，主要是配置的新增、更新或者删除
func (s *store) Stop() {
	......
}

// Init方法用于整理mixer server目前支持的模板、适配器等kind类型的配置，如果不在支持的范围内，则有些配置默认丢弃。
// 同时注意，后端存储服务rpc调用，是只支持pb协议
func (s *store) Init(kinds map[string]proto.Message) error {
    kindNames := make([]string, 0, len(kinds))
    for k := range kinds {
        kindNames = append(kindNames, k)
    }
    if err := s.backend.Init(kindNames); err != nil {
        return err
    }
    s.kinds = kinds
    return nil
}


// 因为加载，如果是后端存储是独立的server，那么加载可能需要一旦时间才能加载到内存中。所以这里需要等待一段时间。
// 我觉得没有使用通知机制来通知，有点恶心了。
func (s *store) WaitForSynced(timeout time.Duration) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.backend.WaitForSynced(timeout)
}

// Watch方法用于监控后端存储服务的配置变化，并把配置资源新增、更新或者删除事件通过channel传递给mixer server。并更新资源。
// 注意：这个Watch方法并没有启动一个goroutine来监控，而是在Init初始化时就已经开始了监控，只是这里初始化一个channel队列。然后作为接收者。生产者早就在Init时开始了goroutine检测配置的变化
// 
// 还注意一个与实现无关的事情，如果没有消费者，那么生产者获取生产资料，不仅浪费资源，也是做无用功。
// 我觉得合理的策略，应该是生产者发现没有消费者，就什么都不做，这个最好的策略。在v1.0.3这个istio都一直生产，管消费者存在不存在。
// 为此，我提了一个issue：https://github.com/istio/istio/issues/10596
func (s *store) Watch() (<-chan Event, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    if s.queue != nil {
        return nil, ErrWatchAlreadyExists
    }
    ch, err := s.backend.Watch()
    if err != nil {
        return nil, err
    }
    q := newQueue(ch, s.kinds)
    s.queue = q
    return q.chout, nil
}

// Get方法，用于获取指定资源的数据，Resource只包含有效数据部分，也就是我们关注的spec部分的数据。
//
// 因为apiVersion、kind和metadata都是识别资源的数据，而我们关心的是后端适配器需要的数据，也就是spec部分的数据。
// Resource存储的两类数据，其实是一样的，只是一类是我们直接可以处理的数据定义，一类是用于pb协议传输的数据定义
func (s *store) Get(key Key) (*Resource, error) {
    obj, err := s.backend.Get(key)
    if err != nil {
        return nil, err
    }

    pbSpec, err := cloneMessage(key.Kind, s.kinds)
    if err != nil {
        return nil, err
    }
    if err = convert(key, obj.Spec, pbSpec); err != nil {
        return nil, err
    }
    return &Resource{Metadata: obj.Metadata, Spec: pbSpec}, nil
}

// 获取当前mixer server后端存储的所有配置数据，也就是对上文的遍历和存储Resource列表
func (s *store) List() map[Key]*Resource {
    data := s.backend.List()
    result := make(map[Key]*Resource, len(data))
    for k, d := range data {
        pbSpec, err := cloneMessage(k.Kind, s.kinds)
        if err != nil {
            log.Errorf("Failed to clone %s spec: %v", k, err)
            continue
        }
        if err = convert(k, d.Spec, pbSpec); err != nil {
            log.Errorf("Failed to convert %s spec: %v", k, err)
            continue
        }
        result[k] = &Resource{
            Metadata: d.Metadata,
            Spec:     pbSpec,
        }
    }
    return result
}
```

上面介绍了Store接口、Backend接口和store实现并利用了Backend接口，并对外提供服务。

1. 初始化mixer server支持的所有配置kind类型列表，包括：template名列表、adapter适配器名列表、以及instance、rule、handler、template和attributemanifest。
2. Watch监控后端存储服务的配置变化，通过channel队列返回event。
3. WaitForSynced直接超时，阻塞后端存储服务加载所有配置到内存。
4. Get获取指定资源的spec部分的数据模型定义；包括两部分：1. map类型的spec数据定义；2. 需要grpc通过pb传输的数据模型定义。两类存储的数据是一样的；
5. List获取后端存储服务所有的配置数据定义，对第4步完成遍历并返回Resouce列表。
6. Stop用于停止对后端存储服务配置的动态监听。

接下来，我们再一次对store进行封装，进行store的一键初始化操作服务

```shell
// Builder函数类型用于创建一个Backend，或者是k8s、或者是MCP后端存储服务
type Builder func(u *url.URL, gv *schema.GroupVersion, credOptions *creds.Options, ck []string) (Backend, error)

// RegisterFunc，因为mixer server支持多种后端存储服务，所有当用户输入ConfigStoreUrl时，就要对这个scheme进行校验，看是不是系统支持的协议类型。
type RegisterFunc func(map[string]Builder)

// Registory用于返回mixer server支持的所有后端存储服务协议，并根据用户输入校验，并返回初始化后封装Backend的store
type Registry struct {
    builders map[string]Builder
}

// NewRegistry方法用于实例化mixer server支持所有的后端存储配置协议列表实例-Registry。
func NewRegistry(inventory ...RegisterFunc) *Registry {
    b := map[string]Builder{}
    // 这种填充参数的方式，已经是通用方法了。也就是mixer server支持的各个后端存储协议。各个后端存储配置协议都提供了构建Backend实例，并提供的相关服务接口。
    for _, rf := range inventory {
        rf(b)
    }
    return &Registry{builders: b}
}

// NewStore方法根据用户输入的ConfigStoreUrl参数值的scheme，来校验当前mixer server服务要使用的后端存储配置服务。
func (r *Registry) NewStore(
    configURL string,
    groupVersion *schema.GroupVersion,
    credOptions *creds.Options,
    criticalKinds []string) (Store, error) {
    u, err := url.Parse(configURL)

    if err != nil {
        return nil, fmt.Errorf("invalid config URL %s %v", configURL, err)
    }

    var b Backend
    switch u.Scheme {
    case FSUrl:
        b = newFsStore(u.Path)
    default:
    	  // 创建Backend实例
        if builder, ok := r.builders[u.Scheme]; ok {
            b, err = builder(u, groupVersion, credOptions, criticalKinds)
            if err != nil {
                return nil, err
            }
        }
    }
    if b != nil {
        return &store{backend: b}, nil
    }
    return nil, fmt.Errorf("unknown config URL %s %v", configURL, u)
}
```

下面我们介绍下，mixer server实例初始化时，是如何初始化后端存储配置服务的客户端实例的

下面这段代码就是在创建mixer server实例时，对后端存储配置服务的client实例初始化代码。

```shell
// 这个一般都是nil，因为Args是从命令行获取的参数列表，store.Store是不可能获取的。它用于测试, 测试单元直接给定一个初始化的Store。
st := a.ConfigStore 
if st == nil {
	// 如果不指定scheme，则默认为k8s后端存储配置服务
    configStoreURL := a.ConfigStoreURL
    if configStoreURL == "" {
        configStoreURL = "k8s://"
    }

	// config.StoreInventory方法用于获取mixer server支持的所有后端存储服务
	// 注意：fs存储配置服务是本地的实例，它直接已经写入到store.go文件中了，可以直接使用
	//
	// NewRegistry方法用于注册所有支持的后端存储配置服务，用来实例化Backend。
    reg := store.NewRegistry(config.StoreInventory()...)
    groupVersion := &schema.GroupVersion{Group: crd.ConfigAPIGroup, Version: crd.ConfigAPIVersion}
    // NewStore方法，通过参数configStoreUrl的scheme，可以获取到当前mixer server设置使用的后端存储配置服务。并返回一个后端存储配置服务的client实例，并提供一些方法，获取资源配置spec部分的数据模型定义，两部分数据模型：1. mixer server直接使用的metadata，2. grpc传输的pb协议数据模型。
    if st, err = reg.NewStore(configStoreURL, groupVersion, a.CredentialOptions, runtimeconfig.CriticalKinds()); err != nil {
        return nil, fmt.Errorf("unable to connect to the configuration server: %v", err)
    }
}
var rt *runtime.Runtime
templateMap := make(map[string]*template.Info, len(a.Templates))
for k, v := range a.Templates {
    t := v // Make a local copy, otherwise we end up capturing the location of the last entry
    templateMap[k] = &t
}

var kinds map[string]proto.Message
if a.UseAdapterCRDs {
    kinds = runtimeconfig.KindMap(adapterMap, templateMap)
} else {
    kinds = runtimeconfig.KindMap(map[string]*adapter.Info{}, templateMap)
}
// 上面获取kinds这部分代码，目的是获取当前mixer server支持的所有templates名称列表、所有适配器名称列表、template、instance、rule、handler和attributemanifest
//
// Init方法用于初始化加载所有配置并监听后端存储配置服务配置的变化
if err := st.Init(kinds); err != nil {
    return nil, fmt.Errorf("unable to initialize config store: %v", err)
}

// block wait for the config store to sync
log.Info("Awaiting for config store sync...")
// mixer server主动等待30s，用于获取远端后端存储配置服务的有所配置数据
// 
// 你会发现用本地文件系统时，好像Server立马就可以提供服务了，因为在fsStore中这个方法是空操作。
if err := st.WaitForSynced(30 * time.Second); err != nil {
    return nil, err
}
```

## 本地文件系统

> $GOPATH/src/istio.io/istio/mixer/pkg/config/store/fsstore.go

本地文件系统的监听，我们简要说明下，因为它不提供端口服务，只是本地检测配置文件的变化，并实现Backend接口。

说明的几点：
1. 所有的配置文件都是yaml格式，所以支持的文件后缀两种：yml和yaml。
2. 文件内容对比是通过bytes的sha哈希比较
3. 配置文件yaml内容解析，使用过yaml第三方包进行解析

关注三个方法checkAndUpdate、readFiles和parseFile。

1. parseFile方法用于解析绝对路径yaml配置文件内容，因为每个yaml配置文件内容一般都有三个资源，构成一个后端适配器可以处理的数据流，分别是Handler、Template和DestinationRule。所以需要通过`\n---\n`分割资源。并通过yaml第三方包进行资源bytes的解析，并存储到BackEndResource中；
2. readFiles方法，因为我们提供了ConfigStoreUrl参数值，所以可以对这个本地目录路径进行遍历，并借助上面的parseFile方法存储到map[Key]*resource, 上面我们说过Key是资源的唯一标识；
3. checkAndUpdate方法用于比较内存中的map[Key]*resource与通过readFiles进行sha哈希比较，如果不存在，则有新增事件到channel队列中；更新或者删除；
4. checkAndUpdate方法是在mixer server的Init初始化加载所有的配置时，并开始定时去拿去所有的配置并进行比较。然后把新增、更新或者删除事件给到Watch监听的事件中。

```shell
// Init方法用于获取并校验配置数据的变化，形成event发送到channel队列中
func (s *fsStore) Init(kinds []string) error {
    for _, k := range kinds {
        s.kinds[k] = true
    }
    s.checkAndUpdate()
    go func() {
        tick := time.NewTicker(s.checkDuration)
        for {
            select {
            case <-s.donec:
                tick.Stop()
                return
            case <-tick.C:
                s.checkAndUpdate()
            }
        }
    }()
    return nil
}
```

## kubernetes存储

## MCP存储

## 总结

对于k8s和mcp两者，我们后面有时间再看。我们现在更重要的是关注mixer server实例的初始化和数据流向

通过上面的了解，我们可以知道mixer server加载配置的流程，并提供了一些获取配置存储资源的方法，包括Watch等等。
