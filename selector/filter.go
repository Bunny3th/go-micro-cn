package selector

import (
	"go-micro.dev/v5/registry"
)

//基于端点的选择筛选器，返回指定端点名的服务(比较registry.Service.Endpoint.Name)
func FilterEndpoint(name string) Filter {
	return func(old []*registry.Service) []*registry.Service {
		var services []*registry.Service

		for _, service := range old {
			for _, ep := range service.Endpoints {
				if ep.Name == name {
					services = append(services, service)
					break
				}
			}
		}

		return services
	}
}

//基于标签的选择筛选器，返回具有指定标签的服务(比较registry.Service.Nodes.Metadata[key]val)
func FilterLabel(key, val string) Filter {
	return func(old []*registry.Service) []*registry.Service {
		var services []*registry.Service

		for _, service := range old {
			serv := new(registry.Service)
			var nodes []*registry.Node

			for _, node := range service.Nodes {
				if node.Metadata == nil {
					continue
				}

				if node.Metadata[key] == val {
					nodes = append(nodes, node)
				}
			}

			// only add service if there's some nodes
			if len(nodes) > 0 {
				// copy
				*serv = *service
				serv.Nodes = nodes
				services = append(services, serv)
			}
		}

		return services
	}
}

//基于版本的选择筛选器，返回指定版本的服务(比较registry.Service.Version)
func FilterVersion(version string) Filter {
	return func(old []*registry.Service) []*registry.Service {
		var services []*registry.Service

		for _, service := range old {
			if service.Version == version {
				services = append(services, service)
			}
		}

		return services
	}
}
