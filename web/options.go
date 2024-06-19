package web

import (
	"context"
	"crypto/tls"
	"net/http"
	"time"

	"github.com/urfave/cli/v2"
	"go-micro.dev/v5"
	"go-micro.dev/v5/logger"
	"go-micro.dev/v5/registry"
)

// Options for web.
type Options struct {
	Handler http.Handler

	Logger logger.Logger

	Service micro.Service

	Registry registry.Registry

	// Alternative Options
	Context context.Context

	Action    func(*cli.Context)
	Metadata  map[string]string
	TLSConfig *tls.Config

	Server *http.Server

	// 在注册服务之前运行检查函数
	RegisterCheck func(context.Context) error

	Version string

	// Static directory
	StaticDir string

	Advertise string

	Address string
	Name    string
	Id      string
	Flags   []cli.Flag

	BeforeStart []func() error
	BeforeStop  []func() error
	AfterStart  []func() error
	AfterStop   []func() error

	RegisterInterval time.Duration

	RegisterTTL time.Duration

	Secure bool

	Signal bool
}

//使用option模式:
//1、对Options赋默认值
//2、应用传入的opts方法,更改Options中字段
func newOptions(opts ...Option) Options {
	//1、赋默认值
	opt := Options{
		Name:             DefaultName,
		Version:          DefaultVersion,
		Id:               DefaultId,
		Address:          DefaultAddress,
		RegisterTTL:      DefaultRegisterTTL,
		RegisterInterval: DefaultRegisterInterval,
		StaticDir:        DefaultStaticDir,
		Service:          micro.NewService(),
		Context:          context.TODO(),
		Signal:           true,
		Logger:           logger.DefaultLogger,
	}

	//2、应用传入的opts方法,更改Options中字段
	for _, o := range opts {
		o(&opt)
	}

	//3、如果没有传入注册检查函数,则使用默认检查函数  func(context.Context) error { return nil }
	if opt.RegisterCheck == nil {
		opt.RegisterCheck = DefaultRegisterCheck
	}

	return opt
}

// 设置web service 名称
func Name(n string) Option {
	return func(o *Options) {
		o.Name = n
	}
}

// 设置要在UI中加载的ico图标url
func Icon(ico string) Option {
	return func(o *Options) {
		if o.Metadata == nil {
			o.Metadata = make(map[string]string)
		}

		o.Metadata["icon"] = ico
	}
}

// 设置唯一服务器Id
func Id(id string) Option {
	return func(o *Options) {
		o.Id = id
	}
}

// 设置服务版本
func Version(v string) Option {
	return func(o *Options) {
		o.Version = v
	}
}

// 设置与服务关联的元数据
func Metadata(md map[string]string) Option {
	return func(o *Options) {
		o.Metadata = md
	}
}

// 要绑定的地址(主机:端口)
func Address(a string) Option {
	return func(o *Options) {
		o.Address = a
	}
}

// 要为服务发现而播发的地址(主机:端口) [mdns用]
func Advertise(a string) Option {
	return func(o *Options) {
		o.Advertise = a
	}
}

//指定服务的上下文。
//1、可用于发出服务关闭的信号
//2、可用于额外的选项值
func Context(ctx context.Context) Option {
	return func(o *Options) {
		o.Context = ctx
	}
}

// 用于服务发现的注册器
func Registry(r registry.Registry) Option {
	return func(o *Options) {
		o.Registry = r
	}
}

// 注册服务时使用的TTL.
func RegisterTTL(t time.Duration) Option {
	return func(o *Options) {
		o.RegisterTTL = t
	}
}

// 循环注册服务的间隔时间
func RegisterInterval(t time.Duration) Option {
	return func(o *Options) {
		o.RegisterInterval = t
	}
}

// 自定义web处理Handler(如gin)
func Handler(h http.Handler) Option {
	return func(o *Options) {
		o.Handler = h
	}
}

// 自定义http.Server
func Server(srv *http.Server) Option {
	return func(o *Options) {
		o.Server = srv
	}
}

// 设置内部使用的micro服务
func MicroService(s micro.Service) Option {
	return func(o *Options) {
		o.Service = s
	}
}

// 设置命令行flags
func Flags(flags ...cli.Flag) Option {
	return func(o *Options) {
		o.Flags = append(o.Flags, flags...)
	}
}

// 设置命令行操作
func Action(a func(*cli.Context)) Option {
	return func(o *Options) {
		o.Action = a
	}
}

// 设置在服务启动之前执行的func
func BeforeStart(fn func() error) Option {
	return func(o *Options) {
		o.BeforeStart = append(o.BeforeStart, fn)
	}
}

// 设置在服务器停止之前执行的func
func BeforeStop(fn func() error) Option {
	return func(o *Options) {
		o.BeforeStop = append(o.BeforeStop, fn)
	}
}

// 设置在服务启动之后执行的func
func AfterStart(fn func() error) Option {
	return func(o *Options) {
		o.AfterStart = append(o.AfterStart, fn)
	}
}

// 设置在服务停止之后执行的func
func AfterStop(fn func() error) Option {
	return func(o *Options) {
		o.AfterStop = append(o.AfterStop, fn)
	}
}

//是否使用安全通信。
//如果未指定TLSConfig，将使用InsecureSkipVerify并生成自签名证书
func Secure(b bool) Option {
	return func(o *Options) {
		o.Secure = b
	}
}

// TLS设置
func TLSConfig(t *tls.Config) Option {
	return func(o *Options) {
		o.TLSConfig = t
	}
}

// 设置静态文件目录,默认为"/html"
func StaticDir(d string) Option {
	return func(o *Options) {
		o.StaticDir = d
	}
}

// 设置在注册服务之前运行的func
func RegisterCheck(fn func(context.Context) error) Option {
	return func(o *Options) {
		o.RegisterCheck = fn
	}
}

// HandleSignal toggles automatic installation of the signal handler that
// traps TERM, INT, and QUIT.  Users of this feature to disable the signal
// handler, should control liveness of the service through the context.
func HandleSignal(b bool) Option {
	return func(o *Options) {
		o.Signal = b
	}
}

// Logger sets the underline logger.
func Logger(l logger.Logger) Option {
	return func(o *Options) {
		o.Logger = l
	}
}
