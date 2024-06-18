// Package registry is an interface for service discovery
package registry

import (
	"errors"
)

var (
	//默认使用mdns作为注册器
	//mdns 即多播dns（Multicast DNS），mDNS主要实现了在没有传统DNS服务器的情况下使局域网内的主机实现相互发现和通信
	DefaultRegistry = NewRegistry()

	// Not found error when GetService is called.
	ErrNotFound = errors.New("service not found")
	// Watcher stopped error when watcher is stopped.
	ErrWatcherStopped = errors.New("watcher stopped")
)

//注册器为服务发现提供了一个接口，并对不同的实现提供了抽象{consur，etcd，zookeeper，…}。
type Registry interface {
	Init(...Option) error                                //配置初始化方法
	Options() Options                                    //返回选项配置
	Register(*Service, ...RegisterOption) error          //服务注册
	Deregister(*Service, ...DeregisterOption) error      //撤销注册
	GetService(string, ...GetOption) ([]*Service, error) //根据条件获取服务列表
	ListServices(...ListOption) ([]*Service, error)      //列出所有服务
	Watch(...WatchOption) (Watcher, error)               //观察已注册的服务项
	String() string
}

//定义服务结构体
type Service struct {
	Name      string            `json:"name"`
	Version   string            `json:"version"`
	Metadata  map[string]string `json:"metadata"`
	Endpoints []*Endpoint       `json:"endpoints"`
	Nodes     []*Node           `json:"nodes"`
}

//服务节点
type Node struct {
	Metadata map[string]string `json:"metadata"`
	Id       string            `json:"id"`
	Address  string            `json:"address"`
}

//端点
type Endpoint struct {
	Request  *Value            `json:"request"`
	Response *Value            `json:"response"`
	Metadata map[string]string `json:"metadata"`
	Name     string            `json:"name"`
}

type Value struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Values []*Value `json:"values"`
}

//使用Option模式做选项配置
type Option func(*Options)

type RegisterOption func(*RegisterOptions)

type WatchOption func(*WatchOptions)

type DeregisterOption func(*DeregisterOptions)

type GetOption func(*GetOptions)

type ListOption func(*ListOptions)

//注册服务节点,另外提供TTL等选项
func Register(s *Service, opts ...RegisterOption) error {
	return DefaultRegistry.Register(s, opts...)
}

//服务节点取消注册
func Deregister(s *Service) error {
	return DefaultRegistry.Deregister(s)
}

//根据名称检索服务,返回Service切片
func GetService(name string) ([]*Service, error) {
	return DefaultRegistry.GetService(name)
}

//列出所有已注册的服务
func ListServices() ([]*Service, error) {
	return DefaultRegistry.ListServices()
}

// 返回一个观察器,允许跟踪注册的更新
func Watch(opts ...WatchOption) (Watcher, error) {
	return DefaultRegistry.Watch(opts...)
}

func String() string {
	return DefaultRegistry.String()
}
