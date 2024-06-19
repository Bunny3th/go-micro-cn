package selector

import (
	"context"

	"go-micro.dev/v5/logger"
	"go-micro.dev/v5/registry"
)

type Options struct {
	Registry registry.Registry //服务注册器
	Strategy Strategy          //选择策略

	// Other options for implementations of the interface
	// can be stored in a context
	Context context.Context
	// Logger is the underline logger
	Logger logger.Logger
}

//选择器选项
type SelectOptions struct {

	// Other options for implementations of the interface
	// can be stored in a context
	Context  context.Context
	Strategy Strategy //选择策略

	Filters []Filter //筛选器
}

type Option func(*Options)

// SelectOption used when making a select call.
type SelectOption func(*SelectOptions)

// Registry sets the registry used by the selector.
//设置注册器
func Registry(r registry.Registry) Option {
	return func(o *Options) {
		o.Registry = r
	}
}

// SetStrategy sets the default strategy for the selector.
//设置选择策略
func SetStrategy(fn Strategy) Option {
	return func(o *Options) {
		o.Strategy = fn
	}
}

// WithFilter adds a filter function to the list of filters
// used during the Select call.
//设置过滤器
func WithFilter(fn ...Filter) SelectOption {
	return func(o *SelectOptions) {
		o.Filters = append(o.Filters, fn...)
	}
}

// Strategy sets the selector strategy.
//设置选择器使用的策略
func WithStrategy(fn Strategy) SelectOption {
	return func(o *SelectOptions) {
		o.Strategy = fn
	}
}

// WithLogger sets the underline logger.
func WithLogger(l logger.Logger) Option {
	return func(o *Options) {
		o.Logger = l
	}
}
