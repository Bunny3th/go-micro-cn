// 服务注册器缓存
package cache

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	log "go-micro.dev/v5/logger"
	"go-micro.dev/v5/registry"
	util "go-micro.dev/v5/util/registry"
)

//服务注册器接口定义
type Cache interface {
	// 嵌入注册器接口
	registry.Registry
	// stop the cache watcher
	Stop()
}

type Options struct {
	Logger log.Logger
	// TTL is the cache TTL
	TTL time.Duration
}

type Option func(o *Options)

type cache struct {
	opts Options

	//注册器
	registry.Registry
	// status of the registry
	// used to hold onto the cache
	// in failure state
	//用于在故障状态下保留缓存的注册器的状态
	//todo 什么意思呢？假设缓存中已经有服务信息,但是缓存中的服务信息TTL有效期已过,
	//todo 那么就需要从Registry那里重新获取最新的数据,但获取出错
	//todo 那么这时候,只能把缓存中的数据给用户,同时在status中记一下错误,意思就是虽然数据给你了,但是不保证正确
	status error

	//SingleFlight是Go的一个扩展包。作用是当有多个goroutine同时调用同一个
	//函数的时候，只允许一个goroutine去调用这个函数，等到这个调用的goroutine返回结果的时候，
	//再把结果返回给这几个同时调用的程序，这样可以减少并发调用的数量
	//这个包一般用于防止缓存击穿
	sg      singleflight.Group
	cache   map[string][]*registry.Service
	ttls    map[string]time.Time
	watched map[string]bool

	// used to stop the cache
	//停止服务信息观察
	//之后缓存信息不再更新
	exit chan bool

	// indicate whether its running
	watchedRunning map[string]bool

	// registry cache
	sync.RWMutex
}

var (
	DefaultTTL = time.Minute
)

func backoff(attempts int) time.Duration {
	if attempts == 0 {
		return time.Duration(0)
	}
	return time.Duration(math.Pow(10, float64(attempts))) * time.Millisecond
}

//获取注册器状态
func (c *cache) getStatus() error {
	c.RLock()
	defer c.RUnlock()
	return c.status
}

//设置注册器状态
func (c *cache) setStatus(err error) {
	c.Lock()
	c.status = err
	c.Unlock()
}

//检查服务缓存数据是否有效
//1、服务列表必须有数据
//2、TTL时间不能为零时刻(即0年 1月1日00:00:00 UTC)
//3、TTL未过期
func (c *cache) isValid(services []*registry.Service, ttl time.Time) bool {
	//1、服务列表必须有数据
	if len(services) == 0 {
		return false
	}

	// 2、TTL时间不能为零时刻(即0年 1月1日00:00:00 UTC)
	if ttl.IsZero() {
		return false
	}

	// 3、TTL未过期
	if time.Since(ttl) > 0 {
		return false
	}

	// ok
	return true
}

func (c *cache) quit() bool {
	select {
	case <-c.exit:
		return true
	default:
		return false
	}
}

//从缓存中删除服务记录
func (c *cache) del(service string) {
	// don't blow away cache in error state
	if err := c.status; err != nil {
		return
	}
	// otherwise delete entries
	delete(c.cache, service)
	delete(c.ttls, service)
}

//私有方法,传入服务名,获得已注册服务列表
//1、首先尝试从本地缓存中获取已注册服务列表
//2、如果服务还未被观察,则纳入观察中
//3、如果缓存已失效,则需要调用registry.GetService方法获取并更新缓存;而如果获取失败,则只能把已失效的缓存返回给用户
func (c *cache) get(service string) ([]*registry.Service, error) {
	// read lock
	c.RLock()

	//1、首先尝试从本地缓存中获取已注册服务列表
	services := c.cache[service]
	// get cache ttl
	//获得缓存的TTL
	ttl := c.ttls[service]
	// 生成一份拷贝数据
	cp := util.Copy(services)

	// 检查服务缓存数据是否有效
	if c.isValid(cp, ttl) {
		c.RUnlock()
		// 若有效,直接从本地缓存中返回数据
		return cp, nil
	}

	// 定义方法,从registry中读取服务信息,并更新缓存
	get := func(service string, cached []*registry.Service) ([]*registry.Service, error) {
		//注意,这里使用了singleflight,将多个相同的Registry.GetService执行请求合并成一个
		val, err, _ := c.sg.Do(service, func() (interface{}, error) {
			return c.Registry.GetService(service)
		})
		services, _ := val.([]*registry.Service)
		if err != nil {
			//todo 这里很有意思。在缓存失效的情况下尝试从Registry那里重新获取最新的数据,但获取出错
			//todo 那么这时候,如果缓存还有数据,就把缓存中的数据给用户,同时在status中记一下错误,意思就是虽然数据给你了,但是不保证正确
			//todo 这个设计某些时候确实能起到弥补的作用,但我认为应该加一个option参数,让用户自己选择
			if len(cached) > 0 {
				//cache.status=err
				c.setStatus(err)

				//返回过时的缓存
				return cached, nil
			}
			//如果连缓存中都没有数据,直接报错返回
			return nil, err
		}

		//重置cache.status=nil
		if err := c.getStatus(); err != nil {
			c.setStatus(nil)
		}

		// 缓存结果
		c.Lock()
		c.set(service, util.Copy(services))
		c.Unlock()

		//返回数据
		return services, nil
	}

	// 2、如果服务还未被观察,则纳入观察中
	_, ok := c.watched[service]

	// unlock the read lock
	c.RUnlock()

	// check if its being watched
	//检查是否正在观察
	if c.opts.TTL > 0 && !ok {
		c.Lock()

		// 状态设为观察中
		c.watched[service] = true

		// 在观察停止的情况下启动观察
		if !c.watchedRunning[service] {
			go c.run(service)
		}

		c.Unlock()
	}

	// 3、如果缓存已失效,则需要调用registry.GetService方法获取并更新缓存;而如果获取失败,则只能把已失效的缓存返回给用户
	return get(service, cp)
}

//将服务注册信息写入缓存
func (c *cache) set(service string, services []*registry.Service) {
	c.cache[service] = services
	c.ttls[service] = time.Now().Add(c.opts.TTL)
}

//使用观察信息更新缓存
func (c *cache) update(res *registry.Result) {
	if res == nil || res.Service == nil {
		return
	}

	c.Lock()
	defer c.Unlock()

	//只更新观察中的服务缓存
	if _, ok := c.watched[res.Service.Name]; !ok {
		return
	}

	//todo 这一段逻辑很有意思
	//todo 如果缓存中目前还没有对应服务名的数据,则不做任何操作,直接退出
	//todo 即:除非用户已经做过服务发现，否则就不缓存任何对应服务的内容
	//todo 这也是一种懒加载的方式
	services, ok := c.cache[res.Service.Name]
	if !ok {
		return
	}

	//如果观察数据中节点数=0,且Action="delete"
	//删除缓存中服务信息
	if len(res.Service.Nodes) == 0 {
		switch res.Action {
		case "delete":
			c.del(res.Service.Name)
		}
		return
	}

	// existing service found
	var service *registry.Service
	var index int
	for i, s := range services {
		if s.Version == res.Service.Version {
			service = s
			index = i
		}
	}

	switch res.Action {
	case "create", "update":
		if service == nil {
			c.set(res.Service.Name, append(services, res.Service))
			return
		}

		// append old nodes to new service
		for _, cur := range service.Nodes {
			var seen bool
			for _, node := range res.Service.Nodes {
				if cur.Id == node.Id {
					seen = true
					break
				}
			}
			if !seen {
				res.Service.Nodes = append(res.Service.Nodes, cur)
			}
		}

		services[index] = res.Service
		c.set(res.Service.Name, services)
	case "delete":
		if service == nil {
			return
		}

		var nodes []*registry.Node

		// filter cur nodes to remove the dead one
		for _, cur := range service.Nodes {
			var seen bool
			for _, del := range res.Service.Nodes {
				if del.Id == cur.Id {
					seen = true
					break
				}
			}
			if !seen {
				nodes = append(nodes, cur)
			}
		}

		// still got nodes, save and return
		if len(nodes) > 0 {
			service.Nodes = nodes
			services[index] = service
			c.set(service.Name, services)
			return
		}

		// zero nodes left

		// only have one thing to delete
		// nuke the thing
		if len(services) == 1 {
			c.del(service.Name)
			return
		}

		// still have more than 1 service
		// check the version and keep what we know
		var srvs []*registry.Service
		for _, s := range services {
			if s.Version != service.Version {
				srvs = append(srvs, s)
			}
		}

		// save
		c.set(service.Name, srvs)
	case "override": //撤销
		if service == nil {
			return
		}

		c.del(service.Name)
	}
}

// run starts the cache watcher loop
// it creates a new watcher if there's a problem.
func (c *cache) run(service string) {
	c.Lock()
	c.watchedRunning[service] = true
	c.Unlock()
	logger := c.opts.Logger
	// reset watcher on exit
	defer func() {
		c.Lock()
		c.watched = make(map[string]bool)
		c.watchedRunning[service] = false
		c.Unlock()
	}()

	var a, b int

	for {
		// exit early if already dead
		if c.quit() {
			return
		}

		// jitter before starting
		j := rand.Int63n(100)
		time.Sleep(time.Duration(j) * time.Millisecond)

		// create new watcher
		w, err := c.Registry.Watch(registry.WatchService(service))
		if err != nil {
			if c.quit() {
				return
			}

			d := backoff(a)
			c.setStatus(err)

			if a > 3 {
				logger.Logf(log.DebugLevel, "rcache: ", err, " backing off ", d)
				a = 0
			}

			time.Sleep(d)
			a++

			continue
		}

		// reset a
		a = 0

		// watch for events
		if err := c.watch(w); err != nil {
			if c.quit() {
				return
			}

			d := backoff(b)
			c.setStatus(err)

			if b > 3 {
				logger.Logf(log.DebugLevel, "rcache: ", err, " backing off ", d)
				b = 0
			}

			time.Sleep(d)
			b++

			continue
		}

		// reset b
		b = 0
	}
}

// watch loops the next event and calls update
// it returns if there's an error.
//循环观察获取下一个事件并调用私有方法update
func (c *cache) watch(w registry.Watcher) error {
	// used to stop the watch
	stop := make(chan bool)

	// manage this loop
	go func() {
		defer w.Stop()

		select {
		// wait for exit
		case <-c.exit:
			return
		// we've been stopped
		case <-stop:
			return
		}
	}()

	for {
		res, err := w.Next()
		if err != nil {
			close(stop)
			return err
		}

		// reset the error status since we succeeded
		if err := c.getStatus(); err != nil {
			// reset status
			c.setStatus(nil)
		}

		c.update(res)
	}
}

//1、首先尝试从本地缓存中获取已注册服务列表
//2、如果服务还未被观察,则纳入观察中
//3、如果缓存已失效,则需要调用registry.GetService方法获取并更新缓存;而如果获取失败,则只能把已失效的缓存返回给用户
func (c *cache) GetService(service string, opts ...registry.GetOption) ([]*registry.Service, error) {
	// get the service
	services, err := c.get(service)
	if err != nil {
		return nil, err
	}

	// if there's nothing return err
	if len(services) == 0 {
		return nil, registry.ErrNotFound
	}

	// return services
	return services, nil
}

func (c *cache) Stop() {
	c.Lock()
	defer c.Unlock()

	select {
	case <-c.exit:
		return
	default:
		close(c.exit)
	}
}

func (c *cache) String() string {
	return "cache"
}

// New returns a new cache.
//返回一个新的cache实例
//默认缓存TTL:1分钟
func New(r registry.Registry, opts ...Option) Cache {
	rand.Seed(time.Now().UnixNano())

	options := Options{
		TTL:    DefaultTTL,
		Logger: log.DefaultLogger,
	}

	for _, o := range opts {
		o(&options)
	}

	return &cache{
		Registry:       r,
		opts:           options,
		watched:        make(map[string]bool),
		watchedRunning: make(map[string]bool),
		cache:          make(map[string][]*registry.Service),
		ttls:           make(map[string]time.Time),
		exit:           make(chan bool),
	}
}
