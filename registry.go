package nacos

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-kratos/kratos/v2/registry"
	"github.com/go-lynx/lynx-nacos/conf"
	"github.com/go-lynx/lynx/log"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

// NacosRegistrar implements registry.Registrar interface for Nacos
type NacosRegistrar struct {
	client    naming_client.INamingClient
	namespace string
	group     string
	cluster   string
	metadata  map[string]string
	weight    float64
	mu        sync.RWMutex
	instances map[string]*vo.RegisterInstanceParam
}

// NewNacosRegistrar creates a new Nacos registrar
func NewNacosRegistrar(client naming_client.INamingClient, namespace, group, cluster string, metadata map[string]string, weight float64) *NacosRegistrar {
	return &NacosRegistrar{
		client:    client,
		namespace: namespace,
		group:     group,
		cluster:   cluster,
		metadata:  metadata,
		weight:    weight,
		instances: make(map[string]*vo.RegisterInstanceParam),
	}
}

// Register registers a service instance to Nacos
func (r *NacosRegistrar) Register(ctx context.Context, service *registry.ServiceInstance) error {
	if r.client == nil {
		return fmt.Errorf("nacos naming client is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if service == nil {
		return fmt.Errorf("service instance is nil")
	}
	if service.Name == "" {
		return fmt.Errorf("service name is required")
	}

	// Parse endpoint to get host and port
	host, port, err := parseEndpoint(service.Endpoints)
	if err != nil {
		return fmt.Errorf("failed to parse endpoint: %w", err)
	}

	// Build metadata
	metadata := make(map[string]string)
	if r.metadata != nil {
		for k, v := range r.metadata {
			metadata[k] = v
		}
	}
	if service.Metadata != nil {
		for k, v := range service.Metadata {
			metadata[k] = v
		}
	}

	// Create register instance param
	param := vo.RegisterInstanceParam{
		Ip:          host,
		Port:        uint64(port),
		ServiceName: service.Name,
		GroupName:   r.group,
		ClusterName: r.cluster,
		Weight:      r.weight,
		Enable:      true,
		Healthy:     true,
		Ephemeral:   true,
		Metadata:    metadata,
	}

	// Register instance
	success, err := r.client.RegisterInstance(param)
	if err != nil {
		return WrapOperationError(err, "register instance")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !success {
		return fmt.Errorf("failed to register instance: registration returned false")
	}

	// Store instance info
	instanceKey := fmt.Sprintf("%s:%s:%d", service.Name, host, port)
	r.mu.Lock()
	r.instances[instanceKey] = &param
	r.mu.Unlock()

	log.Infof("Service instance registered to Nacos - Service: %s, Host: %s, Port: %d",
		service.Name, host, port)

	return nil
}

// Deregister deregisters a service instance from Nacos
func (r *NacosRegistrar) Deregister(ctx context.Context, service *registry.ServiceInstance) error {
	if r.client == nil {
		return fmt.Errorf("nacos naming client is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if service == nil {
		return fmt.Errorf("service instance is nil")
	}
	if service.Name == "" {
		return fmt.Errorf("service name is required")
	}

	// Parse endpoint to get host and port
	host, port, err := parseEndpoint(service.Endpoints)
	if err != nil {
		return fmt.Errorf("failed to parse endpoint: %w", err)
	}

	// Create deregister instance param
	param := vo.DeregisterInstanceParam{
		Ip:          host,
		Port:        uint64(port),
		ServiceName: service.Name,
		GroupName:   r.group,
		Cluster:     r.cluster,
		Ephemeral:   true,
	}

	// Deregister instance
	success, err := r.client.DeregisterInstance(param)
	if err != nil {
		return WrapOperationError(err, "deregister instance")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !success {
		return fmt.Errorf("failed to deregister instance: deregistration returned false")
	}

	// Remove instance info
	instanceKey := fmt.Sprintf("%s:%s:%d", service.Name, host, port)
	r.mu.Lock()
	delete(r.instances, instanceKey)
	r.mu.Unlock()

	log.Infof("Service instance deregistered from Nacos - Service: %s, Host: %s, Port: %d",
		service.Name, host, port)

	return nil
}

// parseEndpoint parses endpoint string to extract host and port.
// Supports IPv4 (host:port), IPv6 ([host]:port), and protocol-prefixed formats.
func parseEndpoint(endpoints []string) (string, int, error) {
	if len(endpoints) == 0 {
		return "", 0, fmt.Errorf("no endpoints provided")
	}

	endpoint := endpoints[0]

	// Remove protocol prefix
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "grpc://")
	endpoint = strings.TrimPrefix(endpoint, "grpcs://")

	// Handle IPv6 format: [::1]:8080 or [2001:db8::1]:8080
	var host, portStr string
	if strings.HasPrefix(endpoint, "[") {
		closeBracket := strings.Index(endpoint, "]")
		if closeBracket == -1 {
			return "", 0, fmt.Errorf("invalid IPv6 endpoint format: %s", endpoints[0])
		}
		host = endpoint[1:closeBracket]
		rest := endpoint[closeBracket+1:]
		if !strings.HasPrefix(rest, ":") {
			return "", 0, fmt.Errorf("invalid IPv6 endpoint format (missing port): %s", endpoints[0])
		}
		portStr = strings.TrimPrefix(rest, ":")
	} else {
		// IPv4 format: host:port
		parts := strings.SplitN(endpoint, ":", 2)
		if len(parts) != 2 {
			return "", 0, fmt.Errorf("invalid endpoint format: %s", endpoints[0])
		}
		host = parts[0]
		portStr = parts[1]
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port in endpoint: %w", err)
	}
	if port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("invalid port in endpoint: %d", port)
	}

	return host, port, nil
}

// NacosDiscovery implements registry.Discovery interface for Nacos
type NacosDiscovery struct {
	client    naming_client.INamingClient
	namespace string
	group     string
	cluster   string
	mu        sync.RWMutex
	watchers  map[string]*ServiceWatcher
}

// NewNacosDiscovery creates a new Nacos discovery client
func NewNacosDiscovery(client naming_client.INamingClient, namespace, group, cluster string) *NacosDiscovery {
	return &NacosDiscovery{
		client:    client,
		namespace: namespace,
		group:     group,
		cluster:   cluster,
		watchers:  make(map[string]*ServiceWatcher),
	}
}

// GetService gets service instances from Nacos
func (d *NacosDiscovery) GetService(ctx context.Context, serviceName string) ([]*registry.ServiceInstance, error) {
	if d.client == nil {
		return nil, fmt.Errorf("nacos naming client is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if serviceName == "" {
		return nil, fmt.Errorf("service name is required")
	}

	// Create subscribe param
	param := vo.SelectInstancesParam{
		ServiceName: serviceName,
		GroupName:   d.group,
		Clusters:    []string{d.cluster},
		HealthyOnly: true,
	}

	// Get instances
	instances, err := d.client.SelectInstances(param)
	if err != nil {
		return nil, WrapOperationError(err, "get service instances")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Convert to registry.ServiceInstance
	var serviceInstances []*registry.ServiceInstance
	for _, instance := range instances {
		// Build endpoint
		endpoint := fmt.Sprintf("%s:%d", instance.Ip, instance.Port)
		if instance.Metadata != nil {
			if protocol, ok := instance.Metadata["protocol"]; ok {
				endpoint = fmt.Sprintf("%s://%s", protocol, endpoint)
			}
		}

		// Build metadata
		metadata := make(map[string]string)
		if instance.Metadata != nil {
			for k, v := range instance.Metadata {
				metadata[k] = v
			}
		}

		serviceInstance := &registry.ServiceInstance{
			ID:        instance.InstanceId,
			Name:      serviceName,
			Version:   metadata["version"],
			Metadata:  metadata,
			Endpoints: []string{endpoint},
		}

		serviceInstances = append(serviceInstances, serviceInstance)
	}

	return serviceInstances, nil
}

// Watch watches service changes
func (d *NacosDiscovery) Watch(ctx context.Context, serviceName string) (registry.Watcher, error) {
	if d.client == nil {
		return nil, fmt.Errorf("nacos naming client is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if serviceName == "" {
		return nil, fmt.Errorf("service name is required")
	}

	// Check if watcher already exists
	d.mu.RLock()
	if watcher, exists := d.watchers[serviceName]; exists {
		d.mu.RUnlock()
		return watcher, nil
	}
	d.mu.RUnlock()

	// Create new watcher
	watcher := NewServiceWatcher(d.client, serviceName, d.group, d.cluster)

	// Start watching
	if err := watcher.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start watcher: %w", err)
	}

	d.mu.Lock()
	if existing, exists := d.watchers[serviceName]; exists && existing != watcher {
		d.mu.Unlock()
		_ = watcher.Stop()
		return existing, nil
	}
	d.watchers[serviceName] = watcher
	d.mu.Unlock()

	return watcher, nil
}

// ServiceWatcher implements registry.Watcher interface
type ServiceWatcher struct {
	client      naming_client.INamingClient
	serviceName string
	group       string
	cluster     string
	stopCh      chan struct{}
	eventCh     chan []*registry.ServiceInstance
	mu          sync.RWMutex
	running     bool
	stopOnce    sync.Once
	closed      int32 // Use atomic for checking if channels are closed
}

// NewServiceWatcher creates a new service watcher
func NewServiceWatcher(client naming_client.INamingClient, serviceName, group, cluster string) *ServiceWatcher {
	return &ServiceWatcher{
		client:      client,
		serviceName: serviceName,
		group:       group,
		cluster:     cluster,
		stopCh:      make(chan struct{}),
		eventCh:     make(chan []*registry.ServiceInstance, 10),
	}
}

// Start starts watching service changes
func (w *ServiceWatcher) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return fmt.Errorf("watcher is already running")
	}
	w.running = true
	w.mu.Unlock()

	// Subscribe to service changes
	param := &vo.SubscribeParam{
		ServiceName:       w.serviceName,
		GroupName:         w.group,
		Clusters:          []string{w.cluster},
		SubscribeCallback: w.handleServiceChange,
	}

	err := w.client.Subscribe(param)
	if err != nil {
		w.mu.Lock()
		w.running = false
		w.mu.Unlock()
		return fmt.Errorf("failed to subscribe to service: %w", err)
	}

	// Start background goroutine to handle context cancellation
	go func() {
		select {
		case <-ctx.Done():
			w.Stop()
		case <-w.stopCh:
		}
	}()

	return nil
}

// handleServiceChange handles service change events
func (w *ServiceWatcher) handleServiceChange(services []model.Instance, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Warnf("Recovered from panic in service watcher for %s: %v", w.serviceName, r)
		}
	}()

	if err != nil {
		log.Errorf("Service watcher error for %s: %v", w.serviceName, err)
		return
	}

	if atomic.LoadInt32(&w.closed) == 1 {
		return
	}

	// Convert to registry.ServiceInstance
	var serviceInstances []*registry.ServiceInstance
	for _, instance := range services {
		endpoint := fmt.Sprintf("%s:%d", instance.Ip, instance.Port)
		metadata := make(map[string]string)
		if instance.Metadata != nil {
			for k, v := range instance.Metadata {
				metadata[k] = v
			}
		}

		serviceInstance := &registry.ServiceInstance{
			ID:        instance.InstanceId,
			Name:      w.serviceName,
			Version:   metadata["version"],
			Metadata:  metadata,
			Endpoints: []string{endpoint},
		}

		serviceInstances = append(serviceInstances, serviceInstance)
	}

	select {
	case <-w.stopCh:
		return
	case w.eventCh <- serviceInstances:
	default:
		log.Warnf("Service watcher event channel full, dropping event for %s", w.serviceName)
	}
}

// Next returns the next service change event
func (w *ServiceWatcher) Next() ([]*registry.ServiceInstance, error) {
	if atomic.LoadInt32(&w.closed) == 1 {
		return nil, fmt.Errorf("watcher stopped")
	}
	select {
	case instances, ok := <-w.eventCh:
		if !ok {
			return nil, fmt.Errorf("watcher stopped")
		}
		return instances, nil
	case <-w.stopCh:
		return nil, fmt.Errorf("watcher stopped")
	}
}

// Stop stops the watcher
func (w *ServiceWatcher) Stop() error {
	var wasRunning bool
	w.mu.Lock()
	wasRunning = w.running
	w.running = false
	w.mu.Unlock()

	if !wasRunning {
		return nil
	}

	// Use sync.Once to ensure channels are closed only once
	w.stopOnce.Do(func() {
		// Mark as closed atomically
		atomic.StoreInt32(&w.closed, 1)

		// Unsubscribe
		param := &vo.SubscribeParam{
			ServiceName:       w.serviceName,
			GroupName:         w.group,
			Clusters:          []string{w.cluster},
			SubscribeCallback: w.handleServiceChange,
		}
		if w.client != nil {
			_ = w.client.Unsubscribe(param)
		}

		close(w.stopCh)
	})

	return nil
}

// NewServiceRegistry creates a new Nacos service registry
func (p *PlugNacos) NewServiceRegistry() registry.Registrar {
	if err := p.checkInitialized(); err != nil {
		log.Warnf("Nacos plugin not initialized, returning nil registrar: %v", err)
		return nil
	}

	if !p.conf.EnableRegister {
		log.Warnf("Service registration is disabled in Nacos configuration")
		return nil
	}

	if p.namingClient == nil {
		log.Errorf("Nacos naming client is nil")
		return nil
	}

	// Get group and cluster from service config
	group := conf.DefaultGroup
	cluster := conf.DefaultCluster
	if p.conf.ServiceConfig != nil {
		if p.conf.ServiceConfig.Group != "" {
			group = p.conf.ServiceConfig.Group
		}
		if p.conf.ServiceConfig.Cluster != "" {
			cluster = p.conf.ServiceConfig.Cluster
		}
	}

	// Convert metadata
	metadata := make(map[string]string)
	if p.conf.Metadata != nil {
		for k, v := range p.conf.Metadata {
			metadata[k] = v
		}
	}

	return NewNacosRegistrar(p.namingClient, p.getNamespace(), group, cluster, metadata, p.conf.Weight)
}

// NewServiceDiscovery creates a new Nacos service discovery
func (p *PlugNacos) NewServiceDiscovery() registry.Discovery {
	if err := p.checkInitialized(); err != nil {
		log.Warnf("Nacos plugin not initialized, returning nil discovery: %v", err)
		return nil
	}

	if !p.conf.EnableDiscovery {
		log.Warnf("Service discovery is disabled in Nacos configuration")
		return nil
	}

	if p.namingClient == nil {
		log.Errorf("Nacos naming client is nil")
		return nil
	}

	// Get group and cluster from service config
	group := conf.DefaultGroup
	cluster := conf.DefaultCluster
	if p.conf.ServiceConfig != nil {
		if p.conf.ServiceConfig.Group != "" {
			group = p.conf.ServiceConfig.Group
		}
		if p.conf.ServiceConfig.Cluster != "" {
			cluster = p.conf.ServiceConfig.Cluster
		}
	}

	return NewNacosDiscovery(p.namingClient, p.getNamespace(), group, cluster)
}
