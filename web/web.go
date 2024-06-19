// Package web提供基于web的微服务
package web

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
)

//内置服务发现的web服务接口
type Service interface {
	Client() *http.Client
	Init(opts ...Option) error
	Options() Options
	Handle(pattern string, handler http.Handler)
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
	Start() error
	Stop() error
	Run() error
}

// Option for web.
//又是熟悉的Option模式
type Option func(o *Options)

// 定义默认值
var (
	// For serving.
	DefaultName    = "go-web"
	DefaultVersion = "latest"
	DefaultId      = uuid.New().String()
	DefaultAddress = ":0"

	// for registration.
	DefaultRegisterTTL      = time.Second * 90
	DefaultRegisterInterval = time.Second * 30

	// static directory.
	DefaultStaticDir     = "html"
	DefaultRegisterCheck = func(context.Context) error { return nil }
)

// NewService返回一个默认web服务
func NewService(opts ...Option) Service {
	return newService(opts...)
}
