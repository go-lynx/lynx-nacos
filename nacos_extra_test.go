package nacos

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-lynx/lynx"
	"github.com/go-lynx/lynx-nacos/conf"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- helpers ----

// errorConfigClient is an IConfigClient that always returns errors.
type errorConfigClient struct {
	getErr    error
	listenErr error
	closed    int32
}

func (c *errorConfigClient) GetConfig(vo.ConfigParam) (string, error) {
	return "", c.getErr
}
func (c *errorConfigClient) PublishConfig(vo.ConfigParam) (bool, error) { return false, nil }
func (c *errorConfigClient) DeleteConfig(vo.ConfigParam) (bool, error)  { return false, nil }
func (c *errorConfigClient) ListenConfig(vo.ConfigParam) error {
	return c.listenErr
}
func (c *errorConfigClient) CancelListenConfig(vo.ConfigParam) error { return nil }
func (c *errorConfigClient) SearchConfig(vo.SearchConfigParam) (*model.ConfigPage, error) {
	return nil, nil
}
func (c *errorConfigClient) CloseClient() { atomic.StoreInt32(&c.closed, 1) }

// successConfigClient always returns content for GetConfig.
type successConfigClient struct {
	content string
	closed  int32
}

func (c *successConfigClient) GetConfig(vo.ConfigParam) (string, error) { return c.content, nil }
func (c *successConfigClient) PublishConfig(vo.ConfigParam) (bool, error) {
	return false, nil
}
func (c *successConfigClient) DeleteConfig(vo.ConfigParam) (bool, error) { return false, nil }
func (c *successConfigClient) ListenConfig(vo.ConfigParam) error         { return nil }
func (c *successConfigClient) CancelListenConfig(vo.ConfigParam) error   { return nil }
func (c *successConfigClient) SearchConfig(vo.SearchConfigParam) (*model.ConfigPage, error) {
	return nil, nil
}
func (c *successConfigClient) CloseClient() { atomic.StoreInt32(&c.closed, 1) }

// ---- Metrics tests ----

func TestNewNacosMetrics(t *testing.T) {
	m := NewNacosMetrics()
	require.NotNil(t, m)
	// Calling each recording method should not panic.
	m.RecordSDKOperation("test_op", "start")
	m.RecordSDKOperation("test_op", "success")
	m.RecordSDKError("test_op", "timeout")
	m.RecordHealthCheck("nacos", "start")
	m.RecordHealthCheck("nacos", "success")
	m.RecordHealthCheckFailed("nacos", "connect_err")
	m.RecordServiceDiscovery("my-service", "success")
	m.RecordConfigOperation("get", "app.yaml", "success")
}

// ---- NacosConfigSource tests ----

func TestNewNacosConfigSource_NilClient(t *testing.T) {
	_, err := NewNacosConfigSource(nil, "app.yaml", conf.DefaultGroup, "yaml")
	assert.Error(t, err)
}

func TestNewNacosConfigSource_GetConfigError(t *testing.T) {
	client := &errorConfigClient{getErr: errors.New("network error")}
	_, err := NewNacosConfigSource(client, "app.yaml", conf.DefaultGroup, "yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load initial config")
}

func TestNewNacosConfigSource_Success(t *testing.T) {
	client := &successConfigClient{content: "key: value"}
	src, err := NewNacosConfigSource(client, "app.yaml", conf.DefaultGroup, "yaml")
	require.NoError(t, err)
	require.NotNil(t, src)

	kvs, err := src.Load()
	require.NoError(t, err)
	require.Len(t, kvs, 1)
	assert.Equal(t, "app.yaml", kvs[0].Key)
	assert.Equal(t, []byte("key: value"), kvs[0].Value)
}

func TestNacosConfigSource_Load_EmptyContent(t *testing.T) {
	client := &successConfigClient{content: ""}
	// GetConfig returns empty string – constructor succeeds but Load should return error.
	src := &NacosConfigSource{
		client: client,
		dataId: "app.yaml",
		group:  conf.DefaultGroup,
		format: "yaml",
	}
	_, err := src.Load()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "config content is empty")
}

func TestNacosConfigSource_Watch(t *testing.T) {
	client := &successConfigClient{content: "key: value"}
	src := &NacosConfigSource{
		client:  client,
		dataId:  "app.yaml",
		group:   conf.DefaultGroup,
		format:  "yaml",
		content: "key: value",
	}

	watcher, err := src.Watch()
	require.NoError(t, err)
	require.NotNil(t, watcher)
	assert.NoError(t, watcher.Stop())
}

// ---- ConfigWatcher tests ----

func TestNewConfigWatcher(t *testing.T) {
	client := &successConfigClient{}
	nacosWatcher := NewNacosConfigWatcher(client, "app.yaml", conf.DefaultGroup, "yaml")
	cw := NewConfigWatcher(context.Background(), nacosWatcher)
	require.NotNil(t, cw)
	assert.NoError(t, cw.Start())
	assert.NoError(t, cw.Stop())
}

func TestConfigWatcher_Stop_Nil(t *testing.T) {
	var cw *ConfigWatcher
	assert.NoError(t, cw.Stop())
}

func TestConfigWatcher_Stop_NilWatcher(t *testing.T) {
	cw := &ConfigWatcher{cancel: func() {}, watcher: nil}
	assert.NoError(t, cw.Stop())
}

// ---- GetConfig tests ----

func TestPlugNacos_GetConfig_NotInitialized(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{EnableConfig: true}
	// Not initialized
	_, err := p.GetConfig("app.yaml", "")
	assert.Error(t, err)
}

func TestPlugNacos_GetConfig_ConfigDisabled(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{EnableConfig: false}
	atomic.StoreInt32(&p.initialized, 1)
	_, err := p.GetConfig("app.yaml", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

func TestPlugNacos_GetConfig_NilClient(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{EnableConfig: true}
	atomic.StoreInt32(&p.initialized, 1)
	_, err := p.GetConfig("app.yaml", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestPlugNacos_GetConfig_DefaultGroup(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{EnableConfig: true}
	atomic.StoreInt32(&p.initialized, 1)
	p.configClient = &successConfigClient{content: "key: value"}

	src, err := p.GetConfig("app.yaml", "")
	require.NoError(t, err)
	require.NotNil(t, src)
}

func TestPlugNacos_GetConfig_WithGroup(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{EnableConfig: true}
	atomic.StoreInt32(&p.initialized, 1)
	p.configClient = &successConfigClient{content: "name: test"}

	src, err := p.GetConfig("app.yaml", "MY_GROUP")
	require.NoError(t, err)
	require.NotNil(t, src)
}

// ---- GetConfigSources tests ----

func TestPlugNacos_GetConfigSources_NotInitialized(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{}
	_, err := p.GetConfigSources()
	assert.Error(t, err)
}

func TestPlugNacos_GetConfigSources_NoAdditional(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{EnableConfig: true}
	atomic.StoreInt32(&p.initialized, 1)
	p.configClient = &successConfigClient{content: "key: value"}

	srcs, err := p.GetConfigSources()
	require.NoError(t, err)
	assert.Len(t, srcs, 1)
}

func TestPlugNacos_GetConfigSources_WithAdditional(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{
		EnableConfig: true,
		AdditionalConfigs: []*conf.ConfigSource{
			{DataId: "extra.yaml", Group: conf.DefaultGroup},
		},
	}
	atomic.StoreInt32(&p.initialized, 1)
	p.configClient = &successConfigClient{content: "key: value"}

	srcs, err := p.GetConfigSources()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(srcs), 1)
}

// ---- GetConfigWatchTargets tests ----

func TestPlugNacos_GetConfigWatchTargets_NotInitialized(t *testing.T) {
	p := NewNacosControlPlane()
	_, err := p.GetConfigWatchTargets("myapp")
	assert.Error(t, err)
}

func TestPlugNacos_GetConfigWatchTargets_Basic(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{}
	atomic.StoreInt32(&p.initialized, 1)

	targets, err := p.GetConfigWatchTargets("myapp")
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, "myapp.yaml", targets[0].FileName)
}

func TestPlugNacos_GetConfigWatchTargets_WithServiceConfig(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{
		ServiceConfig: &conf.ServiceConfig{
			ServiceName: "my-service",
			Group:       "CUSTOM_GROUP",
		},
		AdditionalConfigs: []*conf.ConfigSource{
			{DataId: "common.yaml", Group: conf.DefaultGroup},
		},
	}
	atomic.StoreInt32(&p.initialized, 1)

	targets, err := p.GetConfigWatchTargets("")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(targets), 1)
	assert.Equal(t, "my-service.yaml", targets[0].FileName)
	assert.Equal(t, "CUSTOM_GROUP", targets[0].Group)
}

// ---- WatchControlPlaneConfig tests ----

func TestPlugNacos_WatchControlPlaneConfig_NotInitialized(t *testing.T) {
	p := NewNacosControlPlane()
	_, err := p.WatchControlPlaneConfig(context.Background(), lynxTarget("app.yaml", ""))
	assert.Error(t, err)
}

func TestPlugNacos_WatchControlPlaneConfig_NilClient(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{}
	atomic.StoreInt32(&p.initialized, 1)

	_, err := p.WatchControlPlaneConfig(context.Background(), lynxTarget("app.yaml", ""))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestPlugNacos_WatchControlPlaneConfig_DefaultGroup(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{}
	atomic.StoreInt32(&p.initialized, 1)
	p.configClient = &successConfigClient{content: "key: value"}

	watcher, err := p.WatchControlPlaneConfig(context.Background(), lynxTarget("app.yaml", ""))
	require.NoError(t, err)
	assert.NoError(t, watcher.Stop())
}

// ---- WatchConfig tests ----

func TestPlugNacos_WatchConfig_NotInitialized(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{EnableConfig: true}
	err := p.WatchConfig("app.yaml", "", func(string) {})
	assert.Error(t, err)
}

func TestPlugNacos_WatchConfig_Disabled(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{EnableConfig: false}
	atomic.StoreInt32(&p.initialized, 1)
	err := p.WatchConfig("app.yaml", "", func(string) {})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

func TestPlugNacos_WatchConfig_NilClient(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{EnableConfig: true}
	atomic.StoreInt32(&p.initialized, 1)
	err := p.WatchConfig("app.yaml", "", func(string) {})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestPlugNacos_WatchConfig_Success(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{EnableConfig: true}
	atomic.StoreInt32(&p.initialized, 1)
	p.configClient = &successConfigClient{}

	err := p.WatchConfig("app.yaml", conf.DefaultGroup, func(string) {})
	assert.NoError(t, err)

	// Duplicate call should return nil (already watching).
	err = p.WatchConfig("app.yaml", conf.DefaultGroup, func(string) {})
	assert.NoError(t, err)

	// Cleanup.
	_ = p.CleanupTasks()
}

// ---- ControlPlaneCapabilities tests ----

func TestPlugNacos_ControlPlaneCapabilities(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{
		EnableRegister:  true,
		EnableDiscovery: true,
	}

	caps := p.ControlPlaneCapabilities()
	assert.GreaterOrEqual(t, len(caps), 4)
}

func TestPlugNacos_ControlPlaneCapabilities_NilConf(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = nil
	caps := p.ControlPlaneCapabilities()
	// Should still return base capabilities.
	assert.GreaterOrEqual(t, len(caps), 2)
}

// ---- GetNamespace tests ----

func TestPlugNacos_GetNamespace_FromNamespace(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{Namespace: "my-ns"}
	assert.Equal(t, "my-ns", p.GetNamespace())
}

func TestPlugNacos_GetNamespace_FromNamespaceId(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{NamespaceId: "ns-id-123"}
	assert.Equal(t, "ns-id-123", p.GetNamespace())
}

func TestPlugNacos_GetNamespace_Default(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{}
	assert.Equal(t, conf.DefaultNamespace, p.GetNamespace())
}

// ---- HTTPRateLimit / GRPCRateLimit / NewNodeRouter ----

func TestPlugNacos_RateLimitsAndRouter(t *testing.T) {
	p := NewNacosControlPlane()
	assert.Nil(t, p.HTTPRateLimit())
	assert.Nil(t, p.GRPCRateLimit())
	assert.Nil(t, p.NewNodeRouter("svc"))
}

// ---- CheckHealth ----

func TestPlugNacos_CheckHealth_NotInitialized(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{Timeout: 1}
	err := p.CheckHealth()
	assert.Error(t, err)
}

// ---- buildServerConfigs / buildClientConfig ----

func TestPlugNacos_buildServerConfigs_SingleAddress(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{
		ServerAddresses: "10.0.0.1:8848",
		ContextPath:     "/nacos",
	}
	p.setDefaultConfig()
	configs := p.buildServerConfigs()
	require.Len(t, configs, 1)
	assert.Equal(t, "10.0.0.1", configs[0].IpAddr)
	assert.Equal(t, uint64(8848), configs[0].Port)
}

func TestPlugNacos_buildServerConfigs_MultipleAddresses(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{
		ServerAddresses: "10.0.0.1:8848,10.0.0.2:8849",
	}
	p.setDefaultConfig()
	configs := p.buildServerConfigs()
	assert.Len(t, configs, 2)
}

func TestPlugNacos_buildServerConfigs_EmptyAddresses(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{}
	p.setDefaultConfig()
	// Falls back to 127.0.0.1:8848
	configs := p.buildServerConfigs()
	require.Len(t, configs, 1)
	assert.Equal(t, "127.0.0.1", configs[0].IpAddr)
}

func TestPlugNacos_buildClientConfig_WithAuth(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{
		ServerAddresses: "127.0.0.1:8848",
		Username:        "admin",
		Password:        "password",
		Endpoint:        "http://endpoint.example.com",
		RegionId:        "cn-hangzhou",
		Timeout:         5,
	}
	p.setDefaultConfig()
	cc := p.buildClientConfig()
	require.NotNil(t, cc)
	assert.Equal(t, "admin", cc.Username)
	assert.Equal(t, "password", cc.Password)
}

func TestPlugNacos_buildClientConfig_WithAccessKey(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{
		ServerAddresses: "127.0.0.1:8848",
		AccessKey:       "ak",
		SecretKey:       "sk",
		Timeout:         5,
	}
	p.setDefaultConfig()
	cc := p.buildClientConfig()
	require.NotNil(t, cc)
	assert.Equal(t, "ak", cc.AccessKey)
	assert.Equal(t, "sk", cc.SecretKey)
}

// ---- parseAddress / splitAddress ----

func TestParseAddress_WithPort(t *testing.T) {
	host, port, err := parseAddress("192.168.1.1:9090")
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.1", host)
	assert.Equal(t, uint64(9090), port)
}

func TestParseAddress_NoPort(t *testing.T) {
	host, port, err := parseAddress("192.168.1.1")
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.1", host)
	assert.Equal(t, uint64(8848), port)
}

func TestParseAddress_InvalidPort(t *testing.T) {
	host, port, err := parseAddress("192.168.1.1:abc")
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.1", host)
	assert.Equal(t, uint64(8848), port)
}

func TestSplitAddress_IPv6(t *testing.T) {
	parts := splitAddress("[::1]:8848")
	require.Len(t, parts, 2)
	assert.Equal(t, "[::1]", parts[0])
	assert.Equal(t, "8848", parts[1])
}

func TestSplitAddress_IPv4(t *testing.T) {
	parts := splitAddress("127.0.0.1:8848")
	require.Len(t, parts, 2)
	assert.Equal(t, "127.0.0.1", parts[0])
	assert.Equal(t, "8848", parts[1])
}

func TestSplitAddress_NoPort(t *testing.T) {
	parts := splitAddress("127.0.0.1")
	assert.Len(t, parts, 1)
}

// ---- getNamespaceID ----

func TestPlugNacos_getNamespaceID_FromNamespaceId(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{NamespaceId: "ns-123"}
	assert.Equal(t, "ns-123", p.getNamespaceID())
}

func TestPlugNacos_getNamespaceID_FallbackToNamespace(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{Namespace: "my-ns"}
	assert.Equal(t, "my-ns", p.getNamespaceID())
}

// ---- resilience: RetryManager ----

func TestRetryManager_DoWithRetry_Success(t *testing.T) {
	rm := NewRetryManager(2, time.Millisecond)
	calls := 0
	err := rm.DoWithRetry(func() error {
		calls++
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestRetryManager_DoWithRetry_SuccessAfterRetry(t *testing.T) {
	rm := NewRetryManager(3, time.Millisecond)
	calls := 0
	err := rm.DoWithRetry(func() error {
		calls++
		if calls < 2 {
			return errors.New("transient")
		}
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 2, calls)
}

func TestRetryManager_DoWithRetry_Exhausted(t *testing.T) {
	rm := NewRetryManager(1, time.Millisecond)
	err := rm.DoWithRetry(func() error { return errors.New("fail") })
	assert.Error(t, err)
}

func TestRetryManager_DoWithRetryContext_Cancelled(t *testing.T) {
	rm := NewRetryManager(10, 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := rm.DoWithRetryContext(ctx, func() error { return errors.New("fail") })
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cancelled")
}

func TestRetryManager_calculateBackoff(t *testing.T) {
	rm := NewRetryManager(5, time.Second)
	b0 := rm.calculateBackoff(0)
	b1 := rm.calculateBackoff(1)
	b2 := rm.calculateBackoff(2)
	assert.LessOrEqual(t, b0, b1)
	assert.LessOrEqual(t, b1, b2)
}

// ---- resilience: CircuitBreaker ----

func TestCircuitBreaker_Do_Success(t *testing.T) {
	cb := NewCircuitBreaker(0.5, conf.DefaultCircuitBreakerHalfOpenTimeout)
	err := cb.Do(func() error { return nil })
	assert.NoError(t, err)
	assert.Equal(t, CircuitStateClosed, cb.state)
}

func TestCircuitBreaker_Do_Failure_OpensCircuit(t *testing.T) {
	cb := NewCircuitBreaker(0.5, conf.DefaultCircuitBreakerHalfOpenTimeout)
	// One success, one failure -> 50% failure rate => open.
	cb.Do(func() error { return nil })
	cb.Do(func() error { return errors.New("fail") })
	assert.Equal(t, CircuitStateOpen, cb.state)
}

func TestCircuitBreaker_Do_OpenReturnsError(t *testing.T) {
	cb := NewCircuitBreaker(0.5, conf.DefaultCircuitBreakerHalfOpenTimeout)
	cb.Do(func() error { return nil })
	cb.Do(func() error { return errors.New("fail") })
	// Circuit is open.
	err := cb.Do(func() error { return nil })
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "circuit breaker is open")
}

func TestCircuitBreaker_HalfOpen_SuccessCloses(t *testing.T) {
	cb := NewCircuitBreaker(0.5, 1*time.Millisecond)
	cb.Do(func() error { return nil })
	cb.Do(func() error { return errors.New("fail") })
	assert.Equal(t, CircuitStateOpen, cb.state)

	time.Sleep(5 * time.Millisecond)

	err := cb.Do(func() error { return nil })
	assert.NoError(t, err)
	assert.Equal(t, CircuitStateClosed, cb.state)
}

func TestCircuitBreaker_HalfOpen_FailureReopens(t *testing.T) {
	cb := NewCircuitBreaker(0.5, 1*time.Millisecond)
	cb.Do(func() error { return nil })
	cb.Do(func() error { return errors.New("fail") })

	time.Sleep(5 * time.Millisecond)

	err := cb.Do(func() error { return errors.New("still failing") })
	assert.Error(t, err)
	assert.Equal(t, CircuitStateOpen, cb.state)
}

func TestCircuitBreaker_resetCounters(t *testing.T) {
	cb := NewCircuitBreaker(0.9, conf.DefaultCircuitBreakerHalfOpenTimeout)
	cb.failureCount = 3
	cb.successCount = 2
	cb.resetCounters()
	assert.Equal(t, 0, cb.failureCount)
	assert.Equal(t, 0, cb.successCount)
}

// ---- voSelectInstancesParam / voConfigParam ----

func TestVoHelperFunctions(t *testing.T) {
	sp := voSelectInstancesParam()
	assert.Equal(t, "lynx-nacos-health-probe", sp.ServiceName)
	assert.Equal(t, conf.DefaultGroup, sp.GroupName)

	cp := voConfigParam()
	assert.Equal(t, "lynx-nacos-health-probe", cp.DataId)
	assert.Equal(t, conf.DefaultGroup, cp.Group)
}

// ---- stringsContainsAny ----

func TestStringsContainsAny(t *testing.T) {
	assert.True(t, stringsContainsAny("config not found in nacos", "not found", "404"))
	assert.True(t, stringsContainsAny("HTTP 404 error", "not found", "404"))
	assert.False(t, stringsContainsAny("unknown error", "not found", "404"))
}

// ---- cleanupContext ----

func TestPlugNacos_cleanupContext(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{Timeout: 5}
	ctx, cancel := p.cleanupContext()
	defer cancel()
	assert.NotNil(t, ctx)
}

func TestPlugNacos_cleanupContext_DefaultTimeout(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{Timeout: 0}
	ctx, cancel := p.cleanupContext()
	defer cancel()
	assert.NotNil(t, ctx)
}

// ---- runWithContext ----

func TestPlugNacos_runWithContext_Success(t *testing.T) {
	p := NewNacosControlPlane()
	ctx := context.Background()
	err := p.runWithContext(ctx, func() error { return nil })
	assert.NoError(t, err)
}

func TestPlugNacos_runWithContext_Error(t *testing.T) {
	p := NewNacosControlPlane()
	ctx := context.Background()
	err := p.runWithContext(ctx, func() error { return errors.New("op failed") })
	assert.Error(t, err)
}

func TestPlugNacos_runWithContext_Cancelled(t *testing.T) {
	p := NewNacosControlPlane()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := p.runWithContext(ctx, func() error {
		time.Sleep(time.Second)
		return nil
	})
	assert.Error(t, err)
}

// ---- initComponents ----
// Note: initComponents calls NewNacosMetrics which uses promauto (global registry).
// It can only be called ONCE across the whole test binary; call it via a plugin that
// hasn't yet had its metrics initialised.
func TestPlugNacos_initComponents_RetryAndBreaker(t *testing.T) {
	p := NewNacosControlPlane()
	// metrics were already registered globally by an earlier test – skip creating
	// them again; just verify the retry manager and circuit breaker.
	p.retryManager = NewRetryManager(3, conf.DefaultRetryInterval)
	p.circuitBreaker = NewCircuitBreaker(conf.DefaultCircuitBreakerThreshold, conf.DefaultCircuitBreakerHalfOpenTimeout)
	assert.NotNil(t, p.retryManager)
	assert.NotNil(t, p.circuitBreaker)
}

// ---- NacosConfigWatcher.Start with listen error ----

func TestNacosConfigWatcher_Start_ListenError(t *testing.T) {
	client := &errorConfigClient{listenErr: errors.New("listen failed")}
	w := NewNacosConfigWatcher(client, "app.yaml", conf.DefaultGroup, "yaml")
	err := w.Start(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to listen config")
}

func TestNacosConfigWatcher_Start_AlreadyRunning(t *testing.T) {
	client := &successConfigClient{}
	w := NewNacosConfigWatcher(client, "app.yaml", conf.DefaultGroup, "yaml")
	require.NoError(t, w.Start(context.Background()))
	err := w.Start(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already running")
	require.NoError(t, w.Stop())
}

// ---- NacosConfigWatcher.Stop idempotency ----

func TestNacosConfigWatcher_Stop_Idempotent(t *testing.T) {
	client := &successConfigClient{}
	w := NewNacosConfigWatcher(client, "app.yaml", conf.DefaultGroup, "yaml")
	require.NoError(t, w.Start(context.Background()))
	assert.NoError(t, w.Stop())
	assert.NoError(t, w.Stop()) // second call should be a no-op
}

// ---- NacosConfigWatcher context cancellation ----

func TestNacosConfigWatcher_ContextCancellation(t *testing.T) {
	client := &successConfigClient{}
	w := NewNacosConfigWatcher(client, "app.yaml", conf.DefaultGroup, "yaml")
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, w.Start(ctx))

	cancel()
	time.Sleep(20 * time.Millisecond) // allow the goroutine to react

	// After context cancel, watcher should be stopped.
	_, err := w.Next()
	assert.Error(t, err)
}

// ---- cleanupTasksContext: skip nil watchers ----

func TestPlugNacos_cleanupTasksContext_NilWatcher(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{Timeout: 1}
	atomic.StoreInt32(&p.initialized, 1)

	// Insert a nil watcher.
	p.watcherMutex.Lock()
	p.configWatchers["nil-watcher:DEFAULT_GROUP"] = nil
	p.watcherMutex.Unlock()

	ctx := context.Background()
	err := p.cleanupTasksContext(ctx)
	assert.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&p.destroyed))
}

// ---- closeSDKClients ----

func TestPlugNacos_closeSDKClients_ConfigClientClosed(t *testing.T) {
	p := NewNacosControlPlane()
	client := &successConfigClient{}
	p.configClient = client

	err := p.closeSDKClients(context.Background())
	assert.NoError(t, err)
	assert.Nil(t, p.configClient)
	assert.Equal(t, int32(1), atomic.LoadInt32(&client.closed))
}

// slowCloseClient is a config client whose CloseClient blocks until unblocked.
type slowCloseClient struct {
	unblock chan struct{}
}

func (c *slowCloseClient) GetConfig(vo.ConfigParam) (string, error)           { return "", nil }
func (c *slowCloseClient) PublishConfig(vo.ConfigParam) (bool, error)         { return false, nil }
func (c *slowCloseClient) DeleteConfig(vo.ConfigParam) (bool, error)          { return false, nil }
func (c *slowCloseClient) ListenConfig(vo.ConfigParam) error                  { return nil }
func (c *slowCloseClient) CancelListenConfig(vo.ConfigParam) error            { return nil }
func (c *slowCloseClient) SearchConfig(vo.SearchConfigParam) (*model.ConfigPage, error) {
	return nil, nil
}
func (c *slowCloseClient) CloseClient() { <-c.unblock }

func TestPlugNacos_closeSDKClients_ContextCancelled(t *testing.T) {
	p := NewNacosControlPlane()
	unblock := make(chan struct{})
	p.configClient = &slowCloseClient{unblock: unblock}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := p.closeSDKClients(ctx)
	close(unblock)
	// Context times out; error expected.
	assert.Error(t, err)
}

// ---- IsContextAware ----

func TestPlugNacos_IsContextAware(t *testing.T) {
	p := NewNacosControlPlane()
	assert.True(t, p.IsContextAware())
}

// ---- InitializeContext ----

func TestPlugNacos_InitializeContext_Cancelled(t *testing.T) {
	p := NewNacosControlPlane()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := p.InitializeContext(ctx, p, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "canceled")
}

// ---- StopContext ----

func TestPlugNacos_StopContext_NotActive(t *testing.T) {
	p := NewNacosControlPlane()
	err := p.StopContext(context.Background(), p)
	assert.Error(t, err)
}

// ---- checkNacosConnectivityContext ----

func TestPlugNacos_checkNacosConnectivityContext_NoClients(t *testing.T) {
	p := NewNacosControlPlane()
	err := p.checkNacosConnectivityContext(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no Nacos client initialized")
}

func TestPlugNacos_checkNacosConnectivityContext_ConfigClientNotFound(t *testing.T) {
	p := NewNacosControlPlane()
	// "config not found" is a known benign error for the health probe.
	p.configClient = &errorConfigClient{getErr: errors.New("config not found")}
	err := p.checkNacosConnectivityContext(context.Background())
	assert.NoError(t, err)
}

func TestPlugNacos_checkNacosConnectivityContext_ConfigClientHardError(t *testing.T) {
	p := NewNacosControlPlane()
	p.configClient = &errorConfigClient{getErr: errors.New("connection refused")}
	err := p.checkNacosConnectivityContext(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "config client connectivity check failed")
}

// ---- currentLynxName / currentLynxApp ----

func TestCurrentLynxName(t *testing.T) {
	// Should not panic; result can be empty if app is not initialized.
	name := currentLynxName()
	_ = name
}

// ---- NacosRegistrar / NacosDiscovery factory helpers ----

func TestPlugNacos_NewServiceRegistry_NilClient(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{}
	// No naming client – should return nil.
	reg := p.NewServiceRegistry()
	assert.Nil(t, reg)
}

func TestPlugNacos_NewServiceDiscovery_NilClient(t *testing.T) {
	p := NewNacosControlPlane()
	p.conf = &conf.Nacos{}
	disc := p.NewServiceDiscovery()
	assert.Nil(t, disc)
}

// ---- WrapInitError / WrapOperationError ----

func TestWrapErrors(t *testing.T) {
	base := errors.New("base")
	w := WrapInitError(base, "context message")
	assert.Error(t, w)
	assert.Contains(t, w.Error(), "context message")
	assert.True(t, errors.Is(w, base))

	w2 := WrapOperationError(base, "op message")
	assert.Error(t, w2)
	assert.Contains(t, w2.Error(), "op message")
	assert.True(t, errors.Is(w2, base))
}

// ---- checkInitialized after destroy ----

func TestPlugNacos_checkInitialized_Destroyed(t *testing.T) {
	p := NewNacosControlPlane()
	atomic.StoreInt32(&p.initialized, 1)
	atomic.StoreInt32(&p.destroyed, 1)
	err := p.checkInitialized()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "destroyed")
}

// ---- getFileExtension edge cases ----

func TestGetFileExtension_EdgeCases(t *testing.T) {
	assert.Equal(t, "yaml", getFileExtension("config.yml"))
	assert.Equal(t, "yaml", getFileExtension("config.yaml"))
	assert.Equal(t, "json", getFileExtension("config.json"))
	assert.Equal(t, "properties", getFileExtension("config.props"))
	assert.Equal(t, "properties", getFileExtension("config.properties"))
	assert.Equal(t, "xml", getFileExtension("config.xml"))
	assert.Equal(t, "", getFileExtension("noextension"))
	assert.Equal(t, "", getFileExtension(""))
}

// lynxTarget builds a lynx.ControlPlaneConfigTarget for tests.
func lynxTarget(fileName, group string) lynx.ControlPlaneConfigTarget {
	return lynx.ControlPlaneConfigTarget{FileName: fileName, Group: group}
}

// Ensure registry/discovery factory functions exist.
func TestPlugNacos_NewNacosRegistrar_Direct(t *testing.T) {
	reg := NewNacosRegistrar(nil, "ns", "group", "cluster", nil, 1.0)
	assert.NotNil(t, reg)
}

func TestPlugNacos_NewNacosDiscovery_Direct(t *testing.T) {
	disc := NewNacosDiscovery(nil, "ns", "group", "cluster")
	assert.NotNil(t, disc)
}
