package selector

import (
	"math/rand"
	"sync"
	"time"

	"go-micro.dev/v5/registry"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

//todo 这里有坑  按照代码逻辑看,作者的意图应该是获得next后，同一个next可以多次复用。但这里存在很大的问题
//todo 假设参数传入服务节点状态正常，那么复用next是没有问题的。但是假设过一段时间后服务节点中有失效呢？此时next使用的数据还是失效前的数据,会选到失效的节点
//todo 所以,这里应该设计为即时读取服务节点列表,而不是通过传参获得


//随机策略
func Random(services []*registry.Service) Next {
	nodes := make([]*registry.Node, 0, len(services))

	for _, service := range services {
		nodes = append(nodes, service.Nodes...)
	}

	return func() (*registry.Node, error) {
		if len(nodes) == 0 {
			return nil, ErrNoneAvailable
		}

		i := rand.Int() % len(nodes)
		return nodes[i], nil
	}
}

//轮询策略
//要注意这里特意定义了一个随机数,为的就是第一次轮询不要全部落在同一个Node上,形成瞬间峰值
func RoundRobin(services []*registry.Service) Next {
	nodes := make([]*registry.Node, 0, len(services))

	for _, service := range services {
		nodes = append(nodes, service.Nodes...)
	}

	var i = rand.Int()
	var mtx sync.Mutex

	return func() (*registry.Node, error) {
		if len(nodes) == 0 {
			return nil, ErrNoneAvailable
		}

		mtx.Lock()
		node := nodes[i%len(nodes)]
		i++
		mtx.Unlock()

		return node, nil
	}
}
