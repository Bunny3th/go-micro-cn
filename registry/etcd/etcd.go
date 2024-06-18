/*
etcd 包提供了使用etcd注册服务的方法
**请注意**
此包原位于 github.com/go-micro/plugins/v5/registry/etcd
为方便演示,特将其放在register包下*/
package etcd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	hash "github.com/mitchellh/hashstructure"
	"go-micro.dev/v5/logger"
	"go-micro.dev/v5/registry"
	"go-micro.dev/v5/util/cmd"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

var (
	prefix = "/micro/registry/"
)

//定义etcd注册器的数据结构,etcdRegistry实现了Registry interface
type etcdRegistry struct {
	client  *clientv3.Client //etcd client
	options registry.Options //配置项

	sync.RWMutex                             //读写锁
	register     map[string]uint64           //注册者  key:微服务+节点ID value:对节点做hash得到的数字
	leases       map[string]clientv3.LeaseID //etcd租约[租赁功能允许为存储在 Etcd 中的键值对设置一个超时时间(TTL)] key:微服务+节点ID value:etcd租赁ID
}

func init() {
	cmd.DefaultRegistries["etcd"] = NewRegistry
}

//生成一个新的etcdRegistry对象，返回其对象指针
func NewRegistry(opts ...registry.Option) registry.Registry {
	//1、初始化一个etcdRegistry指针
	e := &etcdRegistry{
		options:  registry.Options{},
		register: make(map[string]uint64),
		leases:   make(map[string]clientv3.LeaseID),
	}

	//2、读取系统变量ETCD_USERNAME和ETCD_PASSWORD,获取etcd用户名与密码,加入opts配置方法切片
	username, password := os.Getenv("ETCD_USERNAME"), os.Getenv("ETCD_PASSWORD")
	if len(username) > 0 && len(password) > 0 {
		opts = append(opts, Auth(username, password))
	}

	//3、读取系统变量MICRO_REGISTRY_ADDRESS，获取etcd服务地址,,加入opts配置方法切片
	address := os.Getenv("MICRO_REGISTRY_ADDRESS")
	if len(address) > 0 {
		opts = append(opts, registry.Addrs(address))
	}

	//4、通过遍历执行opts数组中配置方法，修改etcdRegistry中options字段
	configure(e, opts...)
	return e
}

//通过遍历执行opts数组中配置方法，修改etcdRegistry中options字段,并对部分参数赋初始值
func configure(e *etcdRegistry, opts ...registry.Option) error {
	//1、etcd连接配置 默认连接地址为 127.0.0.1:2379
	config := clientv3.Config{
		Endpoints: []string{"127.0.0.1:2379"},
	}

	//2、用传入的 func(*Options)方法,修改Options中属性值  PS:对option模式不了解的同学要先学习一下
	for _, o := range opts {
		o(&e.options)
	}

	//3、默认超时值5秒
	if e.options.Timeout == 0 {
		e.options.Timeout = 5 * time.Second
	}

	//4、设置默认logger
	if e.options.Logger == nil {
		e.options.Logger = logger.DefaultLogger
	}

	//5、设置etcd拨号超时时间
	config.DialTimeout = e.options.Timeout

	//6、安全传输层协议(TLS)配置
	if e.options.Secure || e.options.TLSConfig != nil {
		tlsConfig := e.options.TLSConfig
		if tlsConfig == nil {
			tlsConfig = &tls.Config{
				InsecureSkipVerify: true,
			}
		}

		config.TLS = tlsConfig
	}

	//7、查看etcdRegistry.options中context字段是否有传值,若有则进行处理
	if e.options.Context != nil {
		//如果etcdRegistry context字段中有传入kv值Username、Password,则将其赋给etcd连接配置中Username、Password
		u, ok := e.options.Context.Value(authKey{}).(*authCreds)
		if ok {
			config.Username = u.Username
			config.Password = u.Password
		}

		//查看context中是否传入了zap配置，如果有，则将其赋给etcd连接配置的LogConfig
		//Zap是Uber开源的Go高性能日志库，其优势在于实时写结构化日志(Structured Logging)到文件具有很好的性能。
		//结构化日志实际上是指不直接输出日志文本，采用JSON或其它编码方式使日志内容结构化，方便后续分析和查找。
		//比如采用ELK(Elasticsearch Logstatash Kibana)
		cfg, ok := e.options.Context.Value(logConfigKey{}).(*zap.Config)
		if ok && cfg != nil {
			config.LogConfig = cfg
		}
	}

	//8、开始对etcdRegistry.options.Addrs进行处理,赋给etcd连接配置中的Endpoints
	var cAddrs []string

	for _, address := range e.options.Addrs {
		if len(address) == 0 {
			continue
		}
		//将地址字符串分为IP与Port两部分
		addr, port, err := net.SplitHostPort(address)
		//如果没有写Port，则默认其为2379
		if ae, ok := err.(*net.AddrError); ok && ae.Err == "missing port in address" {
			port = "2379"
			addr = address
			cAddrs = append(cAddrs, net.JoinHostPort(addr, port))
		} else if err == nil {
			cAddrs = append(cAddrs, net.JoinHostPort(addr, port))
		}
	}

	//将处理好的地址切片(cAddrs)赋给etcd连接配置中的Endpoints
	if len(cAddrs) > 0 {
		config.Endpoints = cAddrs
	}

	//9、生成etcd Client，赋予etcdRegistry.client
	cli, err := clientv3.New(config)
	if err != nil {
		return err
	}
	e.client = cli
	return nil
}

//registry.Service转json
func encode(s *registry.Service) string {
	b, _ := json.Marshal(s)
	return string(b)
}

//json转registry.Service
func decode(ds []byte) *registry.Service {
	var s *registry.Service
	json.Unmarshal(ds, &s)
	return s
}

//将service、node中"/"符号替换为"-"，返回"/micro/registry/service/node"形式的字符
func nodePath(s, id string) string {
	service := strings.Replace(s, "/", "-", -1)
	node := strings.Replace(id, "/", "-", -1)
	return path.Join(prefix, service, node)
}

//将service中"/"符号替换为"-" ,并加上prefix(默认"/micro/registry/")前缀
func servicePath(s string) string {
	return path.Join(prefix, strings.Replace(s, "/", "-", -1))
}

//这里的Init方法与NewRegistry方法功能差不多，只是不尝试读取系统变量
func (e *etcdRegistry) Init(opts ...registry.Option) error {
	return configure(e, opts...)
}

//返回etcdRegistry实例中的配置
func (e *etcdRegistry) Options() registry.Options {
	return e.options
}

//*重要方法，这里开始实现服务节点注册
func (e *etcdRegistry) registerNode(s *registry.Service, node *registry.Node, opts ...registry.RegisterOption) error {
	if len(s.Nodes) == 0 {
		return errors.New("Require at least one node")
	}

	//1、检查本地缓存中(etcdRegistry.leases)是否存在Key值为"服务名+节点ID"对应的leaseID
	e.RLock()
	/*
		备注:LeaseID 是一个用于表示 Etcd 中租赁（Lease）的唯一标识符.可以使用LeaseID来与 Etcd 中的键值对进行关联
		Etcd 的租赁功能允许为存储在 Etcd 中的键值对设置一个超时时间（TTL, Time-To-Live）,
		当超时时间到达后，与该租赁相关联的键值对将被自动删除.
		可以看出来,这里的LeaseID关联的Key是"服务名+节点ID"组合
	*/
	leaseID, ok := e.leases[s.Name+node.Id]
	e.RUnlock()

	log := e.options.Logger

	//2、如果没有在etcdRegistry.leases map中找到，则需要到etcd中查找是否存在
	//并记录到etcdRegistry对象leases、register两个字段中
	if !ok {
		//定义了一个超时上下文.如果在时间范围内没有从etcd找到leaseID，则取消
		ctx, cancel := context.WithTimeout(context.Background(), e.options.Timeout)
		defer cancel()

		//开始从etcd中寻找Key值为"服务名+节点ID"的所有键值对，这些键值对的Value是registry.Service序列化的JSON字符
		rsp, err := e.client.Get(ctx, nodePath(s.Name, node.Id), clientv3.WithSerializable())
		if err != nil {
			return err
		}

		/*对于etcd中找到的键值对做处理,并记录到etcdRegistry对象leases、register两个字段中
		这里解释一下,一般情况下，一个key只会有一条kv值，但此处resp.Kvs 是一个切片，主要是为了支持以下情况：
		1、历史版本数据的获取。通过设置 WithRev 或 WithPrefixRev 等选项），etcd 客户端库会返回与给定键相关联的所有历史版本的键值对。
		   这些键值对会按照版本从旧到新排序，并且存储在 resp.Kvs 切片中
		2、范围查询，通过设置WithRange选项，可能返回多个键的键值对*/
		for _, kv := range rsp.Kvs {
			if kv.Lease > 0 {
				//用etcd中的leaseID为leaseID变量赋值
				leaseID = clientv3.LeaseID(kv.Lease)

				//kv对中的value转为registry.Service对象
				srv := decode(kv.Value)
				if srv == nil || len(srv.Nodes) == 0 {
					continue
				}

				//对节点切片中第一个元素做hash运算。 （因为K值是"服务名+节点ID",则value中必然只记录一个节点）
				h, err := hash.Hash(srv.Nodes[0], nil)
				if err != nil {
					continue
				}

				//将etcd中键值对记录到etcdRegistry的leases、register两个字段中
				e.Lock()
				e.leases[s.Name+node.Id] = leaseID
				e.register[s.Name+node.Id] = h
				e.Unlock()

				break
			}
		}
	}

	//此变量判断lease是否在etcd中存在
	var leaseNotFound bool

	/*3、对leaseID做续租处理(如果其存在),并从续租结果来判断etcd中是否已经找不到对应的Lease
	为什么要说"如果其存在"?因为leaseID有两种来源：
	1、etcdRegistry.leases map中获得
	2、在etcdRegistry的leases map中没有找到，所以要从etcd中获得
	如果是直接从etcdRegistry.leases 中获得，那么必须考虑一种情况：本地map中存在，但etcd中对应的leases可能已经失效
	*/
	if leaseID > 0 {
		//续租
		//这里一个疑惑点是，为什么不用KeepAlive而用KeepAliveOnce?
		//1、查看KeepAliveOnce源码,此方法循环执行私有keepAliveOnce方法，通过将保活请求从客户端流式传输到服务器，
		//并将保活响应从服务器流式传输给客户端，来保持租约的有效性
		//2、结合项目中web包看，在调用WebService的Run()方法时,会循环执行(默认30秒一次)调用register方法
		//所以，这里没有用KeepAlive
		log.Logf(logger.TraceLevel, "Renewing existing lease for %s %d", s.Name, leaseID)
		if _, err := e.client.KeepAliveOnce(context.TODO(), leaseID); err != nil {
			if err != rpctypes.ErrLeaseNotFound {
				return err
			}
			//一旦返回错误是LeaseNotFound,说明在etcd中找不到对应的Lease
			log.Logf(logger.TraceLevel, "Lease not found for %s %d", s.Name, leaseID)
			//leaseNotFound变量设为true
			leaseNotFound = true
		}
	}

	//4、步骤3续租可能失败，所以接下去,判断是否需要在etcd中注册服务节点

	//对节点取hash值
	h, err := hash.Hash(node, nil)
	if err != nil {
		return err
	}

	//获得etcdRegistry.register 中 key=服务名+节点ID 对应的value
	e.Lock()
	v, ok := e.register[s.Name+node.Id]
	e.Unlock()

	//如果能在etcdRegistry.register获得 && 步骤3续租成功,则不需要重复注册
	if ok && v == h && !leaseNotFound {
		log.Logf(logger.TraceLevel, "Service %s node %s unchanged skipping registration", s.Name, node.Id)
		return nil
	}

	//上面的代码是避免重复注册,接下去开始把节点数据注册进etcd
	service := &registry.Service{
		Name:      s.Name,
		Version:   s.Version,
		Metadata:  s.Metadata,
		Endpoints: s.Endpoints,
		Nodes:     []*registry.Node{node},
	}

	//用option模式设置连接参数
	var options registry.RegisterOptions
	for _, o := range opts {
		o(&options)
	}

	//新建一个超时context对象
	ctx, cancel := context.WithTimeout(context.Background(), e.options.Timeout)
	defer cancel()

	//在etcd中新建一个租赁(lease)
	var lgr *clientv3.LeaseGrantResponse
	if options.TTL.Seconds() > 0 {
		// get a lease used to expire keys since we have a ttl
		lgr, err = e.client.Grant(ctx, int64(options.TTL.Seconds()))
		if err != nil {
			return err
		}
	}

	log.Logf(logger.TraceLevel, "Registering %s id %s with lease %v and leaseID %v and ttl %v", service.Name, node.Id, lgr, lgr.ID, options.TTL)
	// create an entry for the node
	//将KV值put入etcd
	//key:服务名+节点ID
	//value:registry.Service转为json
	if lgr != nil {
		_, err = e.client.Put(ctx, nodePath(service.Name, node.Id), encode(service), clientv3.WithLease(lgr.ID))
	} else {
		//如果上一步获取lease失败，则也做put处理，但是没有设置ttl
		//那么如何保证服务死亡的情况下将这条put数据删除?
		//结合web包中Run方法看,Run方法监听到系统退出信号后,会执行服务注销步骤,将etcd中对应数据删除
		//todo 疑问:在突然停电的时候,会不会导致来不及监听到停止信号,导致服务数据残留在etcd中?
		_, err = e.client.Put(ctx, nodePath(service.Name, node.Id), encode(service))
	}
	if err != nil {
		return err
	}

	//更新etcdRegistry register、leases 字段数据
	e.Lock()
	// save our hash of the service
	e.register[s.Name+node.Id] = h
	// save our leaseID of the service
	if lgr != nil {
		e.leases[s.Name+node.Id] = lgr.ID
	}
	e.Unlock()

	return nil
}

//服务节点注销
func (e *etcdRegistry) Deregister(s *registry.Service, opts ...registry.DeregisterOption) error {
	if len(s.Nodes) == 0 {
		return errors.New("Require at least one node")
	}

	for _, node := range s.Nodes {
		e.Lock()
		// delete our hash of the service
		delete(e.register, s.Name+node.Id)
		// delete our lease of the service
		delete(e.leases, s.Name+node.Id)
		e.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), e.options.Timeout)
		defer cancel()

		e.options.Logger.Logf(logger.TraceLevel, "Deregistering %s id %s", s.Name, node.Id)
		_, err := e.client.Delete(ctx, nodePath(s.Name, node.Id))
		if err != nil {
			return err
		}
	}

	return nil
}

//对每一个服务节点调用registerNode方法，从而实现服务注册
func (e *etcdRegistry) Register(s *registry.Service, opts ...registry.RegisterOption) error {
	if len(s.Nodes) == 0 {
		return errors.New("Require at least one node")
	}

	var gerr error

	// register each node individually
	for _, node := range s.Nodes {
		err := e.registerNode(s, node, opts...)
		if err != nil {
			gerr = err
		}
	}

	return gerr
}

//*重要方法:根据服务名返回在etcd中注册的对应服务数据，序列化成[]*registry.Service
//此方法用于服务发现
func (e *etcdRegistry) GetService(name string, opts ...registry.GetOption) ([]*registry.Service, error) {
	ctx, cancel := context.WithTimeout(context.Background(), e.options.Timeout)
	defer cancel()

	//以服务名为前缀,获取KV对
	rsp, err := e.client.Get(ctx, servicePath(name)+"/", clientv3.WithPrefix(), clientv3.WithSerializable())
	if err != nil {
		return nil, err
	}

	if len(rsp.Kvs) == 0 {
		return nil, registry.ErrNotFound
	}

	serviceMap := map[string]*registry.Service{}

	for _, n := range rsp.Kvs {
		if sn := decode(n.Value); sn != nil {
			s, ok := serviceMap[sn.Version]
			if !ok {
				s = &registry.Service{
					Name:      sn.Name,
					Version:   sn.Version,
					Metadata:  sn.Metadata,
					Endpoints: sn.Endpoints,
				}
				serviceMap[s.Version] = s
			}

			s.Nodes = append(s.Nodes, sn.Nodes...)
		}
	}

	services := make([]*registry.Service, 0, len(serviceMap))
	for _, service := range serviceMap {
		services = append(services, service)
	}

	return services, nil
}

//此方法与GetService不同点在于列出所有已注册的服务信息
func (e *etcdRegistry) ListServices(opts ...registry.ListOption) ([]*registry.Service, error) {
	versions := make(map[string]*registry.Service)

	ctx, cancel := context.WithTimeout(context.Background(), e.options.Timeout)
	defer cancel()

	rsp, err := e.client.Get(ctx, prefix, clientv3.WithPrefix(), clientv3.WithSerializable())
	if err != nil {
		return nil, err
	}

	if len(rsp.Kvs) == 0 {
		return []*registry.Service{}, nil
	}

	for _, n := range rsp.Kvs {
		sn := decode(n.Value)
		if sn == nil {
			continue
		}
		v, ok := versions[sn.Name+sn.Version]
		if !ok {
			versions[sn.Name+sn.Version] = sn
			continue
		}
		// append to service:version nodes
		v.Nodes = append(v.Nodes, sn.Nodes...)
	}

	services := make([]*registry.Service, 0, len(versions))
	for _, service := range versions {
		services = append(services, service)
	}

	// sort the services
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })

	return services, nil
}

//注册项监听
func (e *etcdRegistry) Watch(opts ...registry.WatchOption) (registry.Watcher, error) {
	return newEtcdWatcher(e, e.options.Timeout, opts...)
}

func (e *etcdRegistry) String() string {
	return "etcd"
}
