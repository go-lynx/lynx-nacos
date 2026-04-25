package nacos

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-lynx/lynx-nacos/conf"
	"github.com/go-lynx/lynx/plugins"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"github.com/stretchr/testify/assert"
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
	atomic.StoreInt32(&plugin.initialized, 1)
	plugin.SetStatus(plugins.StatusInactive)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := plugin.StartContext(ctx, plugin)
	close(client.unblock)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "context deadline exceeded")
	}
	assert.Less(t, time.Since(start), time.Second)
	assert.Equal(t, plugins.StatusFailed, plugin.Status(plugin))
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
