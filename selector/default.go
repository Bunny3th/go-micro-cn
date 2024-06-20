package selector

import (
	"sync"
	"time"

	"github.com/pkg/errors"

	"go-micro.dev/v5/registry"
	"go-micro.dev/v5/registry/cache"
)

type registrySelector struct {
	so Options      //选项
	rc cache.Cache  //服务缓存
	mu sync.RWMutex
}

//生成本地服务缓存
func (c *registrySelector) newCache() cache.Cache {
	opts := make([]cache.Option, 0, 1)

	//如果Context传入selector_ttl值,则设置缓存过期时间
	if c.so.Context != nil {
		if t, ok := c.so.Context.Value("selector_ttl").(time.Duration); ok {
			opts = append(opts, cache.WithTTL(t))
		}
	}

	return cache.New(c.so.Registry, opts...)
}

//初始化
func (c *registrySelector) Init(opts ...Option) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	//配置选项
	for _, o := range opts {
		o(&c.so)
	}

	//生成本地缓存
	c.rc.Stop()
	c.rc = c.newCache()

	return nil
}

func (c *registrySelector) Options() Options {
	return c.so
}

//*重要方法 对已发现的服务做选择
//1、获得服务列表
//2、应用筛选器
//3、应用策略,返回Next
func (c *registrySelector) Select(service string, opts ...SelectOption) (Next, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	sopts := SelectOptions{
		Strategy: c.so.Strategy,
	}

	for _, opt := range opts {
		opt(&sopts)
	}

	// get the service
	// try the cache first
	// if that fails go directly to the registry
	//todo 存疑,英文注释说从cache读，但看下来是直接调用了 registry.GetService方法
	//注意:这里调用的是cache的GetService()方法,而不是registry接口的GetService()方法
	services, err := c.rc.GetService(service)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			return nil, ErrNotFound
		}

		return nil, err
	}

	// 使用筛选器过滤
	for _, filter := range sopts.Filters {
		services = filter(services)
	}

	// 如果没有返回结果,直接报错
	if len(services) == 0 {
		return nil, ErrNoneAvailable
	}

	return sopts.Strategy(services), nil
}

//mark方法未实现
func (c *registrySelector) Mark(service string, node *registry.Node, err error) {
}

//reset方法未实现
func (c *registrySelector) Reset(service string) {
}

// 停止观察器并销毁缓存
func (c *registrySelector) Close() error {
	c.rc.Stop()

	return nil
}

func (c *registrySelector) String() string {
	return "registry"
}


//默认选择器
func NewSelector(opts ...Option) Selector {
	//默认使用随机策略
	sopts := Options{
		Strategy: Random,
	}

	for _, opt := range opts {
		opt(&sopts)
	}

	//默认使用mdns作为注册器
	if sopts.Registry == nil {
		sopts.Registry = registry.DefaultRegistry
	}

	s := &registrySelector{
		so: sopts,
	}
	//生成服务缓存
	s.rc = s.newCache()

	return s
}
