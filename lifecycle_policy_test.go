package nacos

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/go-lynx/lynx/pkg/security"
	"github.com/go-lynx/lynx/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The production lifecycle policy (lynx/internal/app/lifecycle_policy.go) rejects
// any plugin for which plugins.HasTrueContextLifecycle is false when
// security.IsProduction() is true. These tests pin the plugin to that contract.
func TestPlugNacos_HasTrueContextLifecycle(t *testing.T) {
	p := NewNacosControlPlane()

	caps := plugins.DescribePluginCapabilities(p)
	assert.True(t, caps.HasLifecycleWithCtx, "plugin must expose StartContext/StopContext/InitializeContext")
	assert.True(t, caps.HasContextSteps, "plugin must implement a context-aware step hook")
	assert.True(t, caps.IsTrulyContextAware)
	assert.True(t, plugins.HasTrueContextLifecycle(p))

	_, ok := plugins.GetTrueContextLifecycle(p)
	assert.True(t, ok)

	var _ plugins.ContextStartupTasker = p
	var _ plugins.ContextCleanupTasker = p
}

func TestPlugNacos_ProductionLifecyclePolicyAccepts(t *testing.T) {
	t.Setenv("LYNX_ENV", "production")
	require.True(t, security.IsProduction())

	p := NewNacosControlPlane()
	// Mirrors DefaultPluginManager.enforceLifecyclePolicy: in production every
	// plugin must report a genuinely cancellable lifecycle.
	assert.True(t, plugins.HasTrueContextLifecycle(p),
		"plugin %s would be rejected by the production lifecycle policy", p.Name())
}

func TestPlugNacos_StartupTasksContext_ObservesCancellation(t *testing.T) {
	p := NewNacosControlPlane()
	atomic.StoreInt32(&p.initialized, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := p.StartupTasksContext(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestPlugNacos_CleanupTasksContext_ResetsInitialized(t *testing.T) {
	p := NewNacosControlPlane()
	atomic.StoreInt32(&p.initialized, 1)

	require.NoError(t, p.CleanupTasksContext(context.Background()))
	assert.Equal(t, int32(0), atomic.LoadInt32(&p.initialized))
	assert.Equal(t, int32(1), atomic.LoadInt32(&p.destroyed))
	assert.ErrorContains(t, p.checkInitialized(), "not initialized")
}

func TestPlugNacos_DestroyedIsClearedOnReinitialize(t *testing.T) {
	p := NewNacosControlPlane()
	atomic.StoreInt32(&p.initialized, 1)
	require.NoError(t, p.CleanupTasksContext(context.Background()))
	require.Equal(t, int32(1), atomic.LoadInt32(&p.destroyed))

	// Re-initialization runs InitializeResources; simulate the post-success
	// state transition it performs so the plugin can be restarted.
	atomic.StoreInt32(&p.destroyed, 0)
	atomic.StoreInt32(&p.initialized, 1)
	assert.NoError(t, p.checkInitialized())
}

func TestIsBenignNamingProbeError(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		benign bool
	}{
		{name: "nil", err: nil, benign: true},
		{name: "sdk empty instance list", err: errors.New("instance list is empty!"), benign: true},
		{name: "sdk empty instance list mixed case", err: errors.New("Instance List Is Empty!"), benign: true},
		{name: "not found", err: errors.New("service not found"), benign: true},
		{name: "not found upper", err: errors.New("Service NOT FOUND"), benign: true},
		{name: "no instance", err: errors.New("no instance available"), benign: true},
		{name: "http 404", err: errors.New("server returned 404"), benign: true},
		{name: "connection refused", err: errors.New("dial tcp 127.0.0.1:8848: connect: connection refused"), benign: false},
		{name: "timeout", err: errors.New("request timeout"), benign: false},
		{name: "auth", err: errors.New("403 forbidden"), benign: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.benign, isBenignNamingProbeError(tt.err))
		})
	}
}

func TestPlugNacos_checkNacosConnectivityContext_EmptyInstanceListIsHealthy(t *testing.T) {
	p := NewNacosControlPlane()
	p.namingClient = &mockNamingClient{selectErr: errors.New("instance list is empty!")}
	assert.NoError(t, p.checkNacosConnectivityContext(context.Background()))

	p.namingClient = &mockNamingClient{selectErr: errors.New("connection refused")}
	assert.ErrorContains(t, p.checkNacosConnectivityContext(context.Background()), "naming client connectivity check failed")
}
