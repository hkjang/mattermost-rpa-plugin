import manifest from 'manifest';

let siteURL = '';

type RequestOptions = Omit<RequestInit, 'headers'> & {
    headers?: Record<string, string>;
};

export type AnalysisScope = {
    included_team_ids: string[];
    included_channel_ids: string[];
    excluded_channel_ids: string[];
    include_public_channel: boolean;
    include_private_channel: boolean;
    include_direct_channel: boolean;
    include_group_channel: boolean;
    include_threads: boolean;
    exclude_bot_messages: boolean;
    exclude_system_messages: boolean;
};

export type AnalysisOperations = {
    retention_days: number;
    hot_topic_limit: number;
    report_keyword_limit: number;
    alert_window_hours: number;
    alert_spike_threshold: number;
    anonymize_authors: boolean;
    reindex_batch_size: number;
};

export type DictionaryEntry = {
    id: string;
    major_category: string;
    sub_category: string;
    purpose: string;
    keywords: string[];
    enabled: boolean;
};

export type AlertRule = {
    id: string;
    name: string;
    major_category: string;
    sub_category: string;
    keyword: string;
    threshold: number;
    window_hours: number;
    enabled: boolean;
};

export type AISettings = {
    vllm_url: string;
    vllm_key: string;
};

export type TeamOption = {
    id: string;
    name: string;
    display_name: string;
};

export type ChannelOption = {
    id: string;
    team_id?: string;
    team_name?: string;
    name: string;
    display_name: string;
    type: string;
};

export type AdminLookupCatalog = {
    teams: TeamOption[];
    channels: ChannelOption[];
};

export type AdminPluginConfig = {
    scope: AnalysisScope;
    operations: AnalysisOperations;
    dictionaries: DictionaryEntry[];
    stopwords: string[];
    alert_rules: AlertRule[];
    ai: AISettings;
};

export type PluginStatus = {
    plugin_id: string;
    config_error?: string;
    source: string;
    included_teams: number;
    included_channels: number;
    excluded_channels: number;
    dictionary_entries: number;
    alert_rules: number;
    analyzed_days: number;
    updated_at: number;
    last_reindex: {
        status: string;
        last_run_at: number;
        last_range_from?: string;
        last_range_to?: string;
        last_channels: number;
        last_posts: number;
        last_indexed: number;
        last_error?: string;
    };
};

export type AdminConfigResponse = {
    config: AdminPluginConfig;
    source: string;
    status: PluginStatus;
    catalog: AdminLookupCatalog;
};

export type AnalyticsSummary = {
    analyzed_messages: number;
    unique_authors: number;
    unique_channels: number;
    unique_keywords: number;
    urgent_messages: number;
};

export type KeywordStat = {
    keyword: string;
    major_category: string;
    sub_category: string;
    purpose: string;
    count: number;
    previous_count: number;
    delta: number;
    change_rate: number;
};

export type CategoryStat = {
    major_category: string;
    count: number;
    subcategories: Array<{
        sub_category: string;
        purpose: string;
        count: number;
    }>;
};

export type ChannelStat = {
    channel_id: string;
    channel_name: string;
    team_name: string;
    channel_type: string;
    message_count: number;
    urgent_messages: number;
    top_category: string;
    top_keywords: string[];
};

export type TrendPoint = {
    date: string;
    label: string;
    messages: number;
    urgent_messages: number;
};

export type MessageDetail = {
    message_id: string;
    created_at: number;
    channel_id: string;
    channel_name: string;
    team_name: string;
    author_display_name: string;
    preview: string;
    urgency_score: number;
    sentiment: string;
    major_categories: string[];
    keyword_matches: Array<{
        keyword: string;
        major_category: string;
        sub_category: string;
        purpose: string;
        count: number;
    }>;
};

export type AlertStatus = {
    id: string;
    name: string;
    status: string;
    description: string;
    count: number;
    threshold: number;
    window_hours: number;
    keyword: string;
    major_category: string;
    sub_category: string;
};

export type StatsResponse = {
    range: {
        from_date: string;
        to_date: string;
        timezone_offset_minutes: number;
    };
    summary: AnalyticsSummary;
    trend: TrendPoint[];
    tag_cloud: KeywordStat[];
    keyword_table: KeywordStat[];
    categories: CategoryStat[];
    channels: ChannelStat[];
    hot_topics: KeywordStat[];
    alerts: AlertStatus[];
    messages: MessageDetail[];
    available_categories: string[];
    available_keywords: string[];
    available_channels: ChannelOption[];
    selected_keyword?: string;
    selected_category?: string;
    selected_channel_id?: string;
    reindex: PluginStatus['last_reindex'];
    generated_at: number;
};

export type ReindexResponse = {
    status: string;
    processed_channels: number;
    processed_posts: number;
    indexed_messages: number;
    from_date: string;
    to_date: string;
    completed_at: number;
};

export function setSiteURL(value: string) {
    siteURL = value.replace(/\/+$/, '');
}

function pluginURL(path: string) {
    const base = siteURL || window.location.origin;
    return `${base}/plugins/${manifest.id}/api/v1${path}`;
}

function getCookie(name: string) {
    if (typeof document === 'undefined' || typeof document.cookie !== 'string') {
        return '';
    }

    const prefix = `${name}=`;
    for (const part of document.cookie.split(';')) {
        const value = part.trim();
        if (value.startsWith(prefix)) {
            return value.slice(prefix.length);
        }
    }

    return '';
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
    const method = (options.method || 'GET').toUpperCase();
    const csrfToken = getCookie('MMCSRF');
    const response = await fetch(pluginURL(path), {
        ...options,
        credentials: 'include',
        headers: {
            Accept: 'application/json',
            'Content-Type': 'application/json',
            'X-Requested-With': 'XMLHttpRequest',
            ...(method !== 'GET' && csrfToken ? {'X-CSRF-Token': csrfToken} : {}),
            ...(options.headers || {}),
        },
    });

    const data = await response.json().catch(() => ({}));
    if (!response.ok) {
        const failure = data as {error?: string};
        throw new Error(failure.error || 'Request failed');
    }

    return data as T;
}

export async function getAdminConfig() {
    return request<AdminConfigResponse>('/config');
}

export async function getStatus() {
    return request<PluginStatus>('/status');
}

export async function getStats(params: {
    fromDate: string;
    toDate: string;
    timezoneOffsetMinutes: number;
    keyword?: string;
    majorCategory?: string;
    channelID?: string;
}) {
    const query = new URLSearchParams({
        from_date: params.fromDate,
        to_date: params.toDate,
        timezone_offset_minutes: String(params.timezoneOffsetMinutes),
    });

    if (params.keyword) {
        query.set('keyword', params.keyword);
    }
    if (params.majorCategory) {
        query.set('major_category', params.majorCategory);
    }
    if (params.channelID) {
        query.set('channel_id', params.channelID);
    }

    return request<StatsResponse>(`/stats?${query.toString()}`);
}

export async function startReindex(payload: {
    fromDate: string;
    toDate: string;
    timezoneOffsetMinutes: number;
    channelIDs?: string[];
}) {
    return request<ReindexResponse>('/reindex', {
        method: 'POST',
        body: JSON.stringify({
            from_date: payload.fromDate,
            to_date: payload.toDate,
            timezone_offset_minutes: payload.timezoneOffsetMinutes,
            channel_ids: payload.channelIDs || [],
        }),
    });
}

export function buildReportURL(params: {
    format: 'json' | 'csv';
    fromDate: string;
    toDate: string;
    timezoneOffsetMinutes: number;
    keyword?: string;
    majorCategory?: string;
    channelID?: string;
}) {
    const query = new URLSearchParams({
        format: params.format,
        from_date: params.fromDate,
        to_date: params.toDate,
        timezone_offset_minutes: String(params.timezoneOffsetMinutes),
    });

    if (params.keyword) {
        query.set('keyword', params.keyword);
    }
    if (params.majorCategory) {
        query.set('major_category', params.majorCategory);
    }
    if (params.channelID) {
        query.set('channel_id', params.channelID);
    }

    return pluginURL(`/report?${query.toString()}`);
}
