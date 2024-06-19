// 选择器是提供了选择服务节点列表的方法,可以把它看作是一个负载均衡器
// 例如:Mc服务注册后，有3个服务节点;服务发现获得了3个节点,但是使用其中哪个节点?此时就需要用到选择器
package selector

import (
	"errors"

	"go-micro.dev/v5/registry"
)


//Selector建立在注册器(registry)的基础上，作为一种选取节点并标记其状态的机制，允许使用各种算法来构建主机池等
type Selector interface {
	Init(opts ...Option) error
	Options() Options
	// Select returns a function which should return the next node
	//Next的定义:func() (*registry.Node, error)
	//用于返回下一个节点
	Select(service string, opts ...SelectOption) (Next, error)
	// Mark sets the success/error against a node
	//标注节点状态(success/error),可用来过滤掉掉状态不正常的节点
	Mark(service string, node *registry.Node, err error)
	// Reset returns state back to zero for a service
	//服务状态重置
	Reset(service string)
	// Close renders the selector unusable

	//关闭选择器,使其不可用
	Close() error
	// Name of the selector
	String() string
}

//基于选择器的策略返回下一个节点的func
type Next func() (*registry.Node, error)

// Filter is used to filter a service during the selection process.
//用于筛选服务
type Filter func([]*registry.Service) []*registry.Service

//选择策略，例如随机、循环
type Strategy func([]*registry.Service) Next

var (
	DefaultSelector = NewSelector()

	ErrNotFound      = errors.New("not found")
	ErrNoneAvailable = errors.New("none available")
)
