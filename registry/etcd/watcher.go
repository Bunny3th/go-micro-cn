package etcd

import (
	"context"
	"errors"
	"time"

	"go-micro.dev/v5/registry"
	clientv3 "go.etcd.io/etcd/client/v3"
)

//定义监视器结构体
type etcdWatcher struct {
	stop    chan bool          //控制是否关闭监视
	w       clientv3.WatchChan //事件通道
	client  *clientv3.Client
	timeout time.Duration
}

func newEtcdWatcher(r *etcdRegistry, timeout time.Duration, opts ...registry.WatchOption) (registry.Watcher, error) {
	//熟悉的Option模式,应用传入的WatchOption方法做配置
	var wo registry.WatchOptions
	for _, o := range opts {
		o(&wo)
	}

	ctx, cancel := context.WithCancel(context.Background())
	stop := make(chan bool, 1)

	go func() {
		<-stop
		cancel()
	}()

	//定义监视服务名称
	watchPath := prefix
	if len(wo.Service) > 0 {
		watchPath = servicePath(wo.Service) + "/"
	}

	return &etcdWatcher{
		stop:    stop,
		w:       r.client.Watch(ctx, watchPath, clientv3.WithPrefix(), clientv3.WithPrevKV()),
		client:  r.client,
		timeout: timeout,
	}, nil
}

//不断从etcdWatcher.w 通道中读取事件
func (ew *etcdWatcher) Next() (*registry.Result, error) {
	//不断获取事件
	for wresp := range ew.w {
		if wresp.Err() != nil {
			return nil, wresp.Err()
		}
		if wresp.Canceled {
			return nil, errors.New("could not get next")
		}
		for _, ev := range wresp.Events {
			service := decode(ev.Kv.Value)
			var action string

			switch ev.Type {
			case clientv3.EventTypePut:
				if ev.IsCreate() {
					action = "create"
				} else if ev.IsModify() {
					action = "update"
				}
			case clientv3.EventTypeDelete:
				action = "delete"

				// get service from prevKv
				service = decode(ev.PrevKv.Value)
			}

			if service == nil {
				continue
			}
			return &registry.Result{
				Action:  action,
				Service: service,
			}, nil
		}
	}
	// 如果没有更多的事件或watch通道关闭，返回一个错误
	return nil, errors.New("could not get next")
}

//关闭监视器
//*注意:这里使用了一个select语句来避免重复关闭stop通道。这是因为在并发编程中，多次关闭一个已关闭的通道会导致程序崩溃。
//通过select语句，先尝试从stop通道接收数据（这在实际情况下会立即返回，因为通道为空），
//如果通道已经被关闭（即case <-ew.stop:被执行），则直接返回；
//否则，执行default分支，关闭stop通道。这样可以确保stop通道只被关闭一次。
func (ew *etcdWatcher) Stop() {
	select {
	case <-ew.stop:
		return
	default:
		close(ew.stop)
	}
}
