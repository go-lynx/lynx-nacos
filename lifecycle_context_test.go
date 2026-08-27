package nacos

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/config"
	"github.com/go-lynx/lynx-nacos/conf"
	"github.com/go-lynx/lynx/plugins"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type blockingConfigClient struct {
	unblock chan struct{}
	closed  int32
}

func (c *blockingConfigClient) GetConfig(vo.ConfigParam) (string, error) {
	<-c.unblock
	return "", nil
}

func (c *blockingConfigClient) PublishConfig(vo.ConfigParam) (bool, error) { return false, nil }

func (c *blockingConfigClient) DeleteConfig(vo.ConfigParam) (bool, error) { return false, nil }

func (c *blockingConfigClient) ListenConfig(vo.ConfigParam) error { return nil }

func (c *blockingConfigClient) CancelListenConfig(vo.ConfigParam) error { return nil }

func (c *blockingConfigClient) SearchConfig(vo.SearchConfigParam) (*model.ConfigPage, error) {
	return nil, nil
}

func (c *blockingConfigClient) CloseClient() {
	atomic.StoreInt32(&c.closed, 1)
}

func TestPlugNacos_StartContext_UsesCallerContextOnConnectivityFailure(t *testing.T) {
	plugin := NewNacosControlPlane()
	plugin.conf = &conf.Nacos{EnableConfig: true, Timeout: 1}
	plugin.retryManager = NewRetryManager(0, time.Millisecond)
	plugin.circuitBreaker = NewCircuitBreaker(conf.DefaultCircuitBreakerThreshold, conf.DefaultCircuitBreakerHalfOpenTimeout)

	client := &blockingConfigClient{unblock: make(chan struct{})}
	plugin.configClient = client
	attachTestRuntime(t, plugin)
	atomic.StoreInt32(&plugin.initialized, 1)
	plugin.SetStatus(plugins.StatusInactive)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := plugin.StartContext(ctx, plugin)
	close(client.unblock)

	if assert.Error(t, err) {
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	}
	assert.Less(t, time.Since(start), time.Second)
	assert.Equal(t, plugins.StatusFailed, plugin.Status(plugin))
}

// attachTestRuntime binds a runtime to the plugin without connecting to Nacos:
// the core base records the runtime before running InitializeResources, and an
// empty lynx.nacos section fails validation before any SDK client is created.
func attachTestRuntime(t *testing.T, plugin *PlugNacos) {
	t.Helper()
	cfg := config.New(config.WithSource(&memorySource{kv: &config.KeyValue{
		Key:    t.Name() + ".yaml",
		Format: "yaml",
		Value:  []byte("lynx:\n  nacos: {}\n"),
	}}))
	require.NoError(t, cfg.Load())
	t.Cleanup(func() { _ = cfg.Close() })

	rt := plugins.NewUnifiedRuntime()
	rt.SetConfig(cfg)
	err := plugin.BasePlugin.Initialize(plugin, rt)
	require.Error(t, err, "empty nacos config must fail validation before SDK clients are created")
	require.Contains(t, err.Error(), "server_addresses or endpoint must be configured")
}

type memorySource struct{ kv *config.KeyValue }

func (s *memorySource) Load() ([]*config.KeyValue, error) { return []*config.KeyValue{s.kv}, nil }
func (s *memorySource) Watch() (config.Watcher, error) {
	return &memoryWatcher{stop: make(chan struct{})}, nil
}

type memoryWatcher struct {
	stop chan struct{}
	once sync.Once
}

func (w *memoryWatcher) Next() ([]*config.KeyValue, error) {
	<-w.stop
	return nil, errors.New("watcher stopped")
}

func (w *memoryWatcher) Stop() error {
	w.once.Do(func() { close(w.stop) })
	return nil
}

func TestPlugNacos_CleanupTasks_ClosesConfigClientAndResetsInitialized(t *testing.T) {
	plugin := NewNacosControlPlane()
	client := &blockingConfigClient{unblock: make(chan struct{})}
	close(client.unblock)
	plugin.configClient = client
	atomic.StoreInt32(&plugin.initialized, 1)

	err := plugin.CleanupTasks()
	assert.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&client.closed))
	assert.Equal(t, int32(0), atomic.LoadInt32(&plugin.initialized))
	assert.Nil(t, plugin.configClient)
}

func TestNacosConfigWatcher_StopRejectsLateEvents(t *testing.T) {
	client := &blockingConfigClient{unblock: make(chan struct{})}
	close(client.unblock)
	watcher := NewNacosConfigWatcher(client, "app.yaml", conf.DefaultGroup, "yaml")

	assert.NoError(t, watcher.Start(context.Background()))
	assert.NoError(t, watcher.Stop())

	watcher.handleConfigChange("", conf.DefaultGroup, "app.yaml", "late: true")

	kvs, err := watcher.Next()
	assert.Nil(t, kvs)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "watcher stopped")
}
