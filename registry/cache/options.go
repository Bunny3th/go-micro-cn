package cache

import (
	"time"

	"go-micro.dev/v5/logger"
)


//设置缓存信息TTL
func WithTTL(t time.Duration) Option {
	return func(o *Options) {
		o.TTL = t
	}
}

//设置日志记录器
func WithLogger(l logger.Logger) Option {
	return func(o *Options) {
		o.Logger = l
	}
}
