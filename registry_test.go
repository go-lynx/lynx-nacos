package nacos

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/go-kratos/kratos/v2/registry"
	"github.com/go-lynx/lynx-nacos/conf"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- mock naming client ----

type mockNamingClient struct {
	registerErr   error
	deregisterErr error
	selectErr     error
	subscribeErr  error
	instances     []model.Instance
	subscribed    bool
	unsubscribed  bool
}

func (m *mockNamingClient) RegisterInstance(param vo.RegisterInstanceParam) (bool, error) {
	if m.registerErr != nil {
		return false, m.registerErr
	}
	return true, nil
}

func (m *mockNamingClient) BatchRegisterInstance(param vo.BatchRegisterInstanceParam) (bool, error) {
	return true, nil
}

func (m *mockNamingClient) DeregisterInstance(param vo.DeregisterInstanceParam) (bool, error) {
	if m.deregisterErr != nil {
		return false, m.deregisterErr
	}
	return true, nil
}

func (m *mockNamingClient) UpdateInstance(param vo.UpdateInstanceParam) (bool, error) {
	return true, nil
}

func (m *mockNamingClient) GetService(param vo.GetServiceParam) (model.Service, error) {
	return model.Service{}, nil
}

func (m *mockNamingClient) GetAllServicesInfo(param vo.GetAllServiceInfoParam) (model.ServiceList, error) {
	return model.ServiceList{}, nil
}

func (m *mockNamingClient) SelectAllInstances(param vo.SelectAllInstancesParam) ([]model.Instance, error) {
	return m.instances, m.selectErr
}

func (m *mockNamingClient) SelectInstances(param vo.SelectInstancesParam) ([]model.Instance, error) {
	return m.instances, m.selectErr
}

func (m *mockNamingClient) SelectOneHealthyInstance(param vo.SelectOneHealthInstanceParam) (*model.Instance, error) {
	if len(m.instances) == 0 {
		return nil, errors.New("no instance")
	}
	return &m.instances[0], nil
}

func (m *mockNamingClient) Subscribe(param *vo.SubscribeParam) error {
	m.subscribed = true
	return m.subscribeErr
}

func (m *mockNamingClient) Unsubscribe(param *vo.SubscribeParam) (err error) {
	m.unsubscribed = true
	return nil
}

func (m *mockNamingClient) CloseClient()     {}
func (m *mockNamingClient) ServerHealthy() bool { return true }

// ---- NacosRegistrar tests ----

func TestNacosRegistrar_NilClient(t *testing.T) {
	r := NewNacosRegistrar(nil, "ns", conf.DefaultGroup, conf.DefaultCluster, nil, 1.0)
	err := r.Register(context.Background(), &registry.ServiceInstance{
		Name:      "svc",
		Endpoints: []string{"grpc://127.0.0.1:8080"},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestNacosRegistrar_NilService(t *testing.T) {
	client := &mockNamingClient{}
	r := NewNacosRegistrar(client, "ns", conf.DefaultGroup, conf.DefaultCluster, nil, 1.0)
	err := r.Register(context.Background(), nil)
	assert.Error(t, err)
}

func TestNacosRegistrar_EmptyServiceName(t *testing.T) {
	client := &mockNamingClient{}
	r := NewNacosRegistrar(client, "ns", conf.DefaultGroup, conf.DefaultCluster, nil, 1.0)
	err := r.Register(context.Background(), &registry.ServiceInstance{
		Name:      "",
		Endpoints: []string{"grpc://127.0.0.1:8080"},
	})
	assert.Error(t, err)
}

func TestNacosRegistrar_Register_Success(t *testing.T) {
	client := &mockNamingClient{}
	r := NewNacosRegistrar(client, "ns", conf.DefaultGroup, conf.DefaultCluster, map[string]string{"env": "test"}, 1.0)
	err := r.Register(context.Background(), &registry.ServiceInstance{
		Name:      "my-service",
		Endpoints: []string{"grpc://127.0.0.1:9090"},
		Metadata:  map[string]string{"version": "v1"},
	})
	require.NoError(t, err)
}

func TestNacosRegistrar_Register_Failure(t *testing.T) {
	client := &mockNamingClient{registerErr: errors.New("nacos error")}
	r := NewNacosRegistrar(client, "ns", conf.DefaultGroup, conf.DefaultCluster, nil, 1.0)
	err := r.Register(context.Background(), &registry.ServiceInstance{
		Name:      "my-service",
		Endpoints: []string{"grpc://127.0.0.1:9090"},
	})
	assert.Error(t, err)
}

func TestNacosRegistrar_Register_ContextCancelled(t *testing.T) {
	client := &mockNamingClient{}
	r := NewNacosRegistrar(client, "ns", conf.DefaultGroup, conf.DefaultCluster, nil, 1.0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.Register(ctx, &registry.ServiceInstance{
		Name:      "my-service",
		Endpoints: []string{"grpc://127.0.0.1:9090"},
	})
	assert.Error(t, err)
}

func TestNacosRegistrar_Deregister_NilClient(t *testing.T) {
	r := NewNacosRegistrar(nil, "ns", conf.DefaultGroup, conf.DefaultCluster, nil, 1.0)
	err := r.Deregister(context.Background(), &registry.ServiceInstance{
		Name:      "svc",
		Endpoints: []string{"grpc://127.0.0.1:8080"},
	})
	assert.Error(t, err)
}

func TestNacosRegistrar_Deregister_Success(t *testing.T) {
	client := &mockNamingClient{}
	r := NewNacosRegistrar(client, "ns", conf.DefaultGroup, conf.DefaultCluster, nil, 1.0)
	err := r.Deregister(context.Background(), &registry.ServiceInstance{
		Name:      "my-service",
		Endpoints: []string{"grpc://127.0.0.1:9090"},
	})
	require.NoError(t, err)
}

func TestNacosRegistrar_Deregister_NilService(t *testing.T) {
	client := &mockNamingClient{}
	r := NewNacosRegistrar(client, "ns", conf.DefaultGroup, conf.DefaultCluster, nil, 1.0)
	err := r.Deregister(context.Background(), nil)
	assert.Error(t, err)
}

func TestNacosRegistrar_Deregister_Failure(t *testing.T) {
	client := &mockNamingClient{deregisterErr: errors.New("nacos error")}
	r := NewNacosRegistrar(client, "ns", conf.DefaultGroup, conf.DefaultCluster, nil, 1.0)
	err := r.Deregister(context.Background(), &registry.ServiceInstance{
		Name:      "my-service",
		Endpoints: []string{"grpc://127.0.0.1:9090"},
	})
	assert.Error(t, err)
}

// ---- NacosDiscovery tests ----

func TestNacosDiscovery_GetService_NilClient(t *testing.T) {
	d := NewNacosDiscovery(nil, "ns", conf.DefaultGroup, conf.DefaultCluster)
	_, err := d.GetService(context.Background(), "my-service")
	assert.Error(t, err)
}

func TestNacosDiscovery_GetService_EmptyName(t *testing.T) {
	client := &mockNamingClient{}
	d := NewNacosDiscovery(client, "ns", conf.DefaultGroup, conf.DefaultCluster)
	_, err := d.GetService(context.Background(), "")
	assert.Error(t, err)
}

func TestNacosDiscovery_GetService_Success(t *testing.T) {
	client := &mockNamingClient{
		instances: []model.Instance{
			{Ip: "10.0.0.1", Port: 8080, InstanceId: "id1", Metadata: map[string]string{"protocol": "grpc", "version": "v1"}},
		},
	}
	d := NewNacosDiscovery(client, "ns", conf.DefaultGroup, conf.DefaultCluster)
	instances, err := d.GetService(context.Background(), "my-service")
	require.NoError(t, err)
	require.Len(t, instances, 1)
	assert.Equal(t, "my-service", instances[0].Name)
}

func TestNacosDiscovery_GetService_ContextCancelled(t *testing.T) {
	client := &mockNamingClient{}
	d := NewNacosDiscovery(client, "ns", conf.DefaultGroup, conf.DefaultCluster)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := d.GetService(ctx, "my-service")
	assert.Error(t, err)
}

func TestNacosDiscovery_GetService_ClientError(t *testing.T) {
	client := &mockNamingClient{selectErr: errors.New("nacos down")}
	d := NewNacosDiscovery(client, "ns", conf.DefaultGroup, conf.DefaultCluster)
	_, err := d.GetService(context.Background(), "my-service")
	assert.Error(t, err)
}

func TestNacosDiscovery_Watch_NilClient(t *testing.T) {
	d := NewNacosDiscovery(nil, "ns", conf.DefaultGroup, conf.DefaultCluster)
	_, err := d.Watch(context.Background(), "my-service")
	assert.Error(t, err)
}

func TestNacosDiscovery_Watch_EmptyName(t *testing.T) {
	client := &mockNamingClient{}
	d := NewNacosDiscovery(client, "ns", conf.DefaultGroup, conf.DefaultCluster)
	_, err := d.Watch(context.Background(), "")
	assert.Error(t, err)
}

func TestNacosDiscovery_Watch_ContextCancelled(t *testing.T) {
	client := &mockNamingClient{}
	d := NewNacosDiscovery(client, "ns", conf.DefaultGroup, conf.DefaultCluster)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := d.Watch(ctx, "my-service")
	assert.Error(t, err)
}

func TestNacosDiscovery_Watch_Success(t *testing.T) {
	client := &mockNamingClient{}
	d := NewNacosDiscovery(client, "ns", conf.DefaultGroup, conf.DefaultCluster)
	watcher, err := d.Watch(context.Background(), "my-service")
	require.NoError(t, err)
	require.NotNil(t, watcher)
	// Second watch call returns the cached watcher.
	watcher2, err := d.Watch(context.Background(), "my-service")
	require.NoError(t, err)
	assert.Equal(t, watcher, watcher2)
	assert.NoError(t, watcher.Stop())
}

func TestNacosDiscovery_Watch_SubscribeError(t *testing.T) {
	client := &mockNamingClient{subscribeErr: errors.New("subscribe failed")}
	d := NewNacosDiscovery(client, "ns", conf.DefaultGroup, conf.DefaultCluster)
	_, err := d.Watch(context.Background(), "my-service")
	assert.Error(t, err)
}

// ---- ServiceWatcher tests ----

func TestNewServiceWatcher(t *testing.T) {
	client := &mockNamingClient{}
	w := NewServiceWatcher(client, "svc", conf.DefaultGroup, conf.DefaultCluster)
	require.NotNil(t, w)
	assert.Equal(t, "svc", w.serviceName)
}

func TestServiceWatcher_Start_AlreadyRunning(t *testing.T) {
	client := &mockNamingClient{}
	w := NewServiceWatcher(client, "svc", conf.DefaultGroup, conf.DefaultCluster)
	require.NoError(t, w.Start(context.Background()))
	err := w.Start(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already running")
	assert.NoError(t, w.Stop())
}

func TestServiceWatcher_Stop_NotRunning(t *testing.T) {
	client := &mockNamingClient{}
	w := NewServiceWatcher(client, "svc", conf.DefaultGroup, conf.DefaultCluster)
	assert.NoError(t, w.Stop())
}

func TestServiceWatcher_Stop_Idempotent(t *testing.T) {
	client := &mockNamingClient{}
	w := NewServiceWatcher(client, "svc", conf.DefaultGroup, conf.DefaultCluster)
	require.NoError(t, w.Start(context.Background()))
	assert.NoError(t, w.Stop())
	assert.NoError(t, w.Stop()) // second stop should be no-op
}

func TestServiceWatcher_Next_StoppedReturnsError(t *testing.T) {
	client := &mockNamingClient{}
	w := NewServiceWatcher(client, "svc", conf.DefaultGroup, conf.DefaultCluster)
	require.NoError(t, w.Start(context.Background()))
	require.NoError(t, w.Stop())

	_, err := w.Next()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stopped")
}

func TestServiceWatcher_handleServiceChange(t *testing.T) {
	client := &mockNamingClient{}
	w := NewServiceWatcher(client, "svc", conf.DefaultGroup, conf.DefaultCluster)
	require.NoError(t, w.Start(context.Background()))

	// Fire a service change.
	w.handleServiceChange([]model.Instance{
		{Ip: "10.0.0.1", Port: 8080},
	}, nil)

	instances, err := w.Next()
	require.NoError(t, err)
	require.Len(t, instances, 1)

	assert.NoError(t, w.Stop())
}

// ---- NewServiceRegistry / NewServiceDiscovery on PlugNacos ----

func TestPlugNacos_NewServiceRegistry_WithClient(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{
		EnableRegister: true,
		ServiceConfig:  &conf.ServiceConfig{Group: conf.DefaultGroup, Cluster: conf.DefaultCluster},
	}
	p.namingClient = &mockNamingClient{}
	atomic.StoreInt32(&p.initialized, 1)
	reg := p.NewServiceRegistry()
	assert.NotNil(t, reg)
}

func TestPlugNacos_NewServiceDiscovery_WithClient(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{
		EnableDiscovery: true,
		ServiceConfig:   &conf.ServiceConfig{Group: conf.DefaultGroup, Cluster: conf.DefaultCluster},
	}
	p.namingClient = &mockNamingClient{}
	atomic.StoreInt32(&p.initialized, 1)
	disc := p.NewServiceDiscovery()
	assert.NotNil(t, disc)
}
