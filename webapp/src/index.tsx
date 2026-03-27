import manifest from 'manifest';
import React from 'react';
import type {Store} from 'redux';

import type {GlobalState} from '@mattermost/types/store';

import {setSiteURL} from './client';
import ConfigSetting from './components/config_setting';
import PluginErrorBoundary from './components/error_boundary';
import type {PluginRegistry} from './types/mattermost-webapp';

const SafeConfigSetting = (props: React.ComponentProps<typeof ConfigSetting>) => (
    <PluginErrorBoundary area={'관리자 설정'}>
        <ConfigSetting {...props}/>
    </PluginErrorBoundary>
);

export default class Plugin {
    public async initialize(registry: PluginRegistry, store: Store<GlobalState>) {
        let nextSiteURL = store.getState().entities.general.config.SiteURL;
        if (!nextSiteURL) {
            nextSiteURL = window.location.origin;
        }
        setSiteURL(nextSiteURL);

        if (registry.registerAdminConsoleCustomSetting) {
            registry.registerAdminConsoleCustomSetting('Config', SafeConfigSetting);
        }
    }
}

declare global {
    interface Window {
        registerPlugin(pluginId: string, plugin: Plugin): void;
    }
}

window.registerPlugin(manifest.id, new Plugin());
