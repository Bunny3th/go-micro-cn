package etcd

import (
	"context"

	"go-micro.dev/v5/registry"
	"go.uber.org/zap"
)

type authKey struct{}

type logConfigKey struct{}

type authCreds struct {
	Username string
	Password string
}

//此方法配置registry.Options中 Context 字段,其目的是连接etcd时提供用户名与密码
func Auth(username, password string) registry.Option {
	return func(o *registry.Options) {
		if o.Context == nil {
			o.Context = context.Background()
		}
		o.Context = context.WithValue(o.Context, authKey{}, &authCreds{Username: username, Password: password})
	}
}

//此方法配置registry.Options中 Context 字段,其目的是配置zap log
func LogConfig(config *zap.Config) registry.Option {
	return func(o *registry.Options) {
		if o.Context == nil {
			o.Context = context.Background()
		}
		o.Context = context.WithValue(o.Context, logConfigKey{}, config)
	}
}
