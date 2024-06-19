package web

import (
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/urfave/cli/v2"
	"go-micro.dev/v5"
	log "go-micro.dev/v5/logger"
	"go-micro.dev/v5/registry"
	maddr "go-micro.dev/v5/util/addr"
	"go-micro.dev/v5/util/backoff"
	mhttp "go-micro.dev/v5/util/http"
	mnet "go-micro.dev/v5/util/net"
	signalutil "go-micro.dev/v5/util/signal"
	mls "go-micro.dev/v5/util/tls"
)

type service struct {
	mux *http.ServeMux    //ServeMux是一个HTTP请求多路复用器。它将每个传入请求的URL与注册的模式列表进行匹配，并调用与URL最匹配的模式的处理程序
	srv *registry.Service //需要注册的服务

	exit chan chan error //接收web服务停止信息
	ex   chan bool       //接收服务注册停止信息
	opts Options

	sync.RWMutex //读写锁
	running      bool
	static       bool //是否开启静态文件访问handle
}

func newService(opts ...Option) Service {
	options := newOptions(opts...)
	s := &service{
		opts:   options,
		mux:    http.NewServeMux(),
		static: true,
		ex:     make(chan bool),
	}
	s.srv = s.genSrv()

	return s
}

//生成需注册的服务信息,服务注册信息是在调用NewService方法时传入的，示例代码:
//  service := web.NewService(
//      web.Name("s1"),                //服务名
//      web.Version("1"),              //服务版本
//      web.Address("127.0.0.1:8080"), //服务地址
//      web.Registry(etcdReg),         //服务注册器
//      web.Handler(NewRouter()),
//)
func (s *service) genSrv() *registry.Service {
	var (
		host string
		port string
		err  error
	)

	logger := s.opts.Logger

	//如果Options中的Address有值,则尝试获取Address中的地址和端口作为服务节点地址
	if len(s.opts.Address) > 0 {
		host, port, err = net.SplitHostPort(s.opts.Address)
		if err != nil {
			logger.Log(log.FatalLevel, err)
		}
	}

	// check the advertise address first
	// if it exists then use it, otherwise
	// use the address
	//如果mdns广播地址有值,则使用广播地址作为服务节点地址
	if len(s.opts.Advertise) > 0 {
		host, port, err = net.SplitHostPort(s.opts.Advertise)
		if err != nil {
			logger.Log(log.FatalLevel, err)
		}
	}

	//Extract返回一个有效的IP地址。如果提供的地址是有效地址，则会直接返回。否则，迭代可用接口以找到IP地址，优选私有地址
	addr, err := maddr.Extract(host)
	if err != nil {
		logger.Log(log.FatalLevel, err)
	}

	if strings.Count(addr, ":") > 0 {
		addr = "[" + addr + "]"
	}

	return &registry.Service{
		Name:    s.opts.Name,
		Version: s.opts.Version,
		Nodes: []*registry.Node{{
			Id:       s.opts.Id,
			Address:  net.JoinHostPort(addr, port),
			Metadata: s.opts.Metadata,
		}},
	}
}

//循环注册服务（默认每隔30秒）,直到收到退出信息
func (s *service) run() {
	s.RLock()
	if s.opts.RegisterInterval <= time.Duration(0) {
		s.RUnlock()
		return
	}

	t := time.NewTicker(s.opts.RegisterInterval)
	s.RUnlock()

	for {
		select {
		case <-t.C:
			s.register()
		case <-s.ex:
			t.Stop()
			return
		}
	}
}

//服务注册
func (s *service) register() error {
	s.Lock()
	defer s.Unlock()

	if s.srv == nil {
		return nil
	}

	logger := s.opts.Logger

	//默认使用micro.Service下的注册器
	r := s.opts.Service.Client().Options().Registry
	//如果用户指定注册器,则使用自定义的
	if s.opts.Registry != nil {
		r = s.opts.Registry
	}

	//服务信息重新生成，因为节点地址可能更改
	srv := s.genSrv()
	srv.Endpoints = s.srv.Endpoints
	s.srv = srv

	// 在注册之前使用RegisterCheck函数
	if err := s.opts.RegisterCheck(s.opts.Context); err != nil {
		logger.Logf(log.ErrorLevel, "Server %s-%s register check error: %s", s.opts.Name, s.opts.Id, err)
		return err
	}

	var regErr error

	//尝试做3次服务注册,其中任意一次成功即返回
	for i := 0; i < 3; i++ {
		// attempt to register
		if err := r.Register(s.srv, registry.RegisterTTL(s.opts.RegisterTTL)); err != nil {
			// set the error
			regErr = err
			// backoff then retry
			time.Sleep(backoff.Do(i + 1))

			continue
		}
		// success so nil error
		regErr = nil

		break
	}

	return regErr
}

//撤销已注册服务
func (s *service) deregister() error {
	s.Lock()
	defer s.Unlock()

	if s.srv == nil {
		return nil
	}

	//默认使用micro.Service下的注册器
	r := s.opts.Service.Client().Options().Registry
	//如果用户指定注册器,则使用自定义的
	if s.opts.Registry != nil {
		r = s.opts.Registry
	}

	//撤销注册
	return r.Deregister(s.srv)
}

//1、执行BeforeStart方法
//2、生成需注册的服务信息
//3、如果用户没有自定义Handler,且service.statics为true则设置一个路由地址为"/"的文件夹访问handler
//4、开启服务监听
//5、开启exit 管道,开始监听服务关闭事件;将running状态设为true
func (s *service) start() error {
	s.Lock()
	defer s.Unlock()

	//如果已经在runing状态,则返回
	if s.running {
		return nil
	}

	//1、执行BeforeStart方法
	for _, fn := range s.opts.BeforeStart {
		if err := fn(); err != nil {
			return err
		}
	}

	//生成listener
	listener, err := s.listen("tcp", s.opts.Address)
	if err != nil {
		return err
	}

	logger := s.opts.Logger

	//2、生成需注册的服务信息
	s.opts.Address = listener.Addr().String()
	srv := s.genSrv()
	srv.Endpoints = s.srv.Endpoints
	s.srv = srv

	var handler http.Handler

	//3、如果用户没有自定义Handler,则设置一个路由地址为"/"的文件夹访问handler
	if s.opts.Handler != nil { //如果用户自定义了Handler(比如gin),则使用自定义Handler
		handler = s.opts.Handler
	} else { //否则使用http.ServeMux
		handler = s.mux
		var r sync.Once

		// 设置一个路由地址为"/"的文件夹访问handler
		r.Do(func() {
			// static dir
			static := s.opts.StaticDir
			if s.opts.StaticDir[0] != '/' {
				dir, _ := os.Getwd()
				static = filepath.Join(dir, static)
			}

			// set static if no / handler is registered
			if s.static {
				_, err := os.Stat(static)
				if err == nil {
					logger.Logf(log.InfoLevel, "Enabling static file serving from %s", static)
					s.mux.Handle("/", http.FileServer(http.Dir(static)))
				}
			}
		})
	}

	var httpSrv *http.Server
	if s.opts.Server != nil { //如果用户定义了http.Server,则使用自定义的
		httpSrv = s.opts.Server
	} else { //否则默认使用*http.Server
		httpSrv = &http.Server{}
	}

	httpSrv.Handler = handler

	//4、开启服务监听
	go httpSrv.Serve(listener)

	for _, fn := range s.opts.AfterStart {
		if err := fn(); err != nil {
			return err
		}
	}

	//5、开启exit 管道,开始监听服务关闭事件;将running状态设为true
	s.exit = make(chan chan error, 1)
	s.running = true

	go func() {
		ch := <-s.exit
		ch <- listener.Close()
	}()

	logger.Logf(log.InfoLevel, "Listening on %v", listener.Addr().String())

	return nil
}

//执行以下步骤
//1、依次执行BeforeStop方法组(方法定义func() error)
//2、service.exit通道中写入error[与start方法中监听通道呼应],关闭web服务监听;将running状态设为false
//3、依次执行AfterStop方法组(方法定义func() error)
func (s *service) stop() error {
	s.Lock()
	defer s.Unlock()

	if !s.running {
		return nil
	}

	//1、依次执行BeforeStop方法组
	for _, fn := range s.opts.BeforeStop {
		if err := fn(); err != nil {
			return err
		}
	}

	//2、service.exit通道中写入error[与start方法中监听通道呼应],关闭web服务监听;将running状态设为false
	ch := make(chan error, 1)
	s.exit <- ch
	s.running = false

	s.opts.Logger.Log(log.InfoLevel, "Stopping")

	//3、依次执行AfterStop方法组
	for _, fn := range s.opts.AfterStop {
		if err := fn(); err != nil {
			if chErr := <-ch; chErr != nil {
				return chErr
			}

			return err
		}
	}

	return <-ch
}

func (s *service) Client() *http.Client {
	rt := mhttp.NewRoundTripper(
		mhttp.WithRegistry(s.opts.Registry),
	)
	return &http.Client{
		Transport: rt,
	}
}

//1、往Endpoints中注册http.Handler
//2、往service.mux(*http.ServeMux)中注册http.Handler
func (s *service) Handle(pattern string, handler http.Handler) {
	var seen bool
	s.RLock()
	for _, ep := range s.srv.Endpoints {
		if ep.Name == pattern {
			seen = true
			break
		}
	}
	s.RUnlock()

	// if its unseen then add an endpoint
	if !seen {
		s.Lock()
		s.srv.Endpoints = append(s.srv.Endpoints, &registry.Endpoint{
			Name: pattern,
		})
		s.Unlock()
	}

	// disable static serving
	if pattern == "/" {
		s.Lock()
		s.static = false
		s.Unlock()
	}

	// register the handler
	s.mux.Handle(pattern, handler)
}

//1、往Endpoints中注册func(http.ResponseWriter, *http.Request)
//2、往service.mux(*http.ServeMux)中注册func(http.ResponseWriter, *http.Request)
func (s *service) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	var seen bool

	s.RLock()
	for _, ep := range s.srv.Endpoints {
		if ep.Name == pattern {
			seen = true
			break
		}
	}
	s.RUnlock()

	if !seen {
		s.Lock()
		s.srv.Endpoints = append(s.srv.Endpoints, &registry.Endpoint{
			Name: pattern,
		})
		s.Unlock()
	}

	// disable static serving
	if pattern == "/" {
		s.Lock()
		s.static = false
		s.Unlock()
	}

	s.mux.HandleFunc(pattern, handler)
}

//初始化选项 --注意,这个方法一般不用
func (s *service) Init(opts ...Option) error {
	s.Lock()

	for _, o := range opts {
		o(&s.opts)
	}

	serviceOpts := []micro.Option{}

	if len(s.opts.Flags) > 0 {
		serviceOpts = append(serviceOpts, micro.Flags(s.opts.Flags...))
	}

	if s.opts.Registry != nil {
		serviceOpts = append(serviceOpts, micro.Registry(s.opts.Registry))
	}

	s.Unlock()

	serviceOpts = append(serviceOpts, micro.Action(func(ctx *cli.Context) error {
		s.Lock()
		defer s.Unlock()

		if ttl := ctx.Int("register_ttl"); ttl > 0 {
			s.opts.RegisterTTL = time.Duration(ttl) * time.Second
		}

		if interval := ctx.Int("register_interval"); interval > 0 {
			s.opts.RegisterInterval = time.Duration(interval) * time.Second
		}

		if name := ctx.String("server_name"); len(name) > 0 {
			s.opts.Name = name
		}

		if ver := ctx.String("server_version"); len(ver) > 0 {
			s.opts.Version = ver
		}

		if id := ctx.String("server_id"); len(id) > 0 {
			s.opts.Id = id
		}

		if addr := ctx.String("server_address"); len(addr) > 0 {
			s.opts.Address = addr
		}

		if adv := ctx.String("server_advertise"); len(adv) > 0 {
			s.opts.Advertise = adv
		}

		if s.opts.Action != nil {
			s.opts.Action(ctx)
		}

		return nil
	}))

	s.RLock()
	// pass in own name and version
	if s.opts.Service.Name() == "" {
		serviceOpts = append(serviceOpts, micro.Name(s.opts.Name))
	}

	serviceOpts = append(serviceOpts, micro.Version(s.opts.Version))

	s.RUnlock()

	s.opts.Service.Init(serviceOpts...)

	s.Lock()
	srv := s.genSrv()
	srv.Endpoints = s.srv.Endpoints
	s.srv = srv
	s.Unlock()

	return nil
}


//执行以下步骤:
//1、执行私有方法start
//2、执行私有方法register
//3、执行私有方法run
func (s *service) Start() error {
	if err := s.start(); err != nil {
		return err
	}

	if err := s.register(); err != nil {
		return err
	}

	// start reg loop
	go s.run()

	return nil
}

//执行以下步骤
//1、关闭service中ex通道(停止服务循环注册)
//2、撤销已注册服务
//3、执行私有的stop方法
func (s *service) Stop() error {
	// exit reg loop
	close(s.ex)

	if err := s.deregister(); err != nil {
		return err
	}

	return s.stop()
}

//*重要方法.执行以下步骤:
//1、调用私有start方法
//2、如果配置中Profile对象不为空,开启profiler(性能分析)
//3、服务注册
//4、开启后台循环服务注册(默认每30秒)
//5、监听系统停止信号,收到后注销服务,发送服务注册停止信号、执行私有deregister、stop方法
func (s *service) Run() error {
	//1、调用私有start方法
	if err := s.start(); err != nil {
		return err
	}

	logger := s.opts.Logger
	// start the profiler
	//2、如果配置中Profile对象不为空,开启profiler(性能分析)
	if s.opts.Service.Options().Profile != nil {
		// to view mutex contention
		runtime.SetMutexProfileFraction(5)
		// to view blocking profile
		runtime.SetBlockProfileRate(1)

		if err := s.opts.Service.Options().Profile.Start(); err != nil {
			return err
		}

		defer func() {
			if err := s.opts.Service.Options().Profile.Stop(); err != nil {
				logger.Log(log.ErrorLevel, err)
			}
		}()
	}

	//3、服务注册
	if err := s.register(); err != nil {
		return err
	}

	// start reg loop
	//4、开启后台循环服务注册(默认每30秒)
	go s.run()

	//5、监听系统停止信号,收到后注销服务,发送服务注册停止信号、执行私有deregister、stop方法
	ch := make(chan os.Signal, 1)
	if s.opts.Signal {
		signal.Notify(ch, signalutil.Shutdown()...)
	}

	select {
	// wait on kill signal
	case sig := <-ch:
		logger.Logf(log.InfoLevel, "Received signal %s", sig)
	// wait on context cancel
	case <-s.opts.Context.Done():
		logger.Log(log.InfoLevel, "Received context shutdown")
	}

	// exit reg loop
	//发送服务注册停止信号
	close(s.ex)

	//注销服务
	if err := s.deregister(); err != nil {
		return err
	}

	//关闭
	return s.stop()
}

// 返回服务的Option
func (s *service) Options() Options {
	return s.opts
}

//返回网络侦听器
func (s *service) listen(network, addr string) (net.Listener, error) {
	var (
		listener net.Listener
		err      error
	)

	// TODO: support use of listen options
	//如果选项中Secure=true && 用户传入TLSConfig,则创建一个TLS侦听器
	if s.opts.Secure || s.opts.TLSConfig != nil {
		config := s.opts.TLSConfig

		fn := func(addr string) (net.Listener, error) {
			if config == nil {
				hosts := []string{addr}

				// check if its a valid host:port
				if host, _, err := net.SplitHostPort(addr); err == nil {
					if len(host) == 0 {
						hosts = maddr.IPs()
					} else {
						hosts = []string{host}
					}
				}

				// generate a certificate
				cert, err := mls.Certificate(hosts...)
				if err != nil {
					return nil, err
				}
				config = &tls.Config{Certificates: []tls.Certificate{cert}}
			}

			return tls.Listen(network, addr, config)
		}

		listener, err = mnet.Listen(addr, fn)
	} else { //否则使用net.Listener
		fn := func(addr string) (net.Listener, error) {
			return net.Listen(network, addr)
		}

		listener, err = mnet.Listen(addr, fn)
	}

	if err != nil {
		return nil, err
	}

	return listener, nil
}
