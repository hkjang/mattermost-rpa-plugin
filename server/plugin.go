package main

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

type Plugin struct {
	plugin.MattermostPlugin

	client *pluginapi.Client
	router *mux.Router

	configurationLock sync.RWMutex
	configuration     *configuration

	analyticsLock sync.Mutex
}

type requestContextKey string

const pluginContextKey requestContextKey = "plugin-context"

func (p *Plugin) OnActivate() error {
	p.client = pluginapi.NewClient(p.API, p.Driver)
	p.router = p.initRouter()

	if err := p.OnConfigurationChange(); err != nil {
		return err
	}

	p.analyticsLock.Lock()
	defer p.analyticsLock.Unlock()

	if runtimeCfg, err := p.getRuntimeConfiguration(); err == nil {
		_ = p.cleanupExpiredAnalysisLocked(time.Now(), runtimeCfg)
	}

	return nil
}

func (p *Plugin) OnDeactivate() error {
	return nil
}

func (p *Plugin) ServeHTTP(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	if c != nil {
		r = r.WithContext(context.WithValue(r.Context(), pluginContextKey, c))
	}
	p.router.ServeHTTP(w, r)
}
