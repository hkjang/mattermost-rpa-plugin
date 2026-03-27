import {
    normalizeAdminConfig,
    parseStoredConfigValue,
} from './config_setting';

describe('config setting drafts', () => {
    test('normalizeAdminConfig preserves local IDs for matching alert rules', () => {
        const previous = {
            scope: {
                included_team_ids: [],
                included_channel_ids: [],
                excluded_channel_ids: [],
                include_public_channel: true,
                include_private_channel: true,
                include_direct_channel: false,
                include_group_channel: false,
                include_threads: true,
                exclude_bot_messages: true,
                exclude_system_messages: true,
            },
            operations: {
                retention_days: 90,
                hot_topic_limit: 10,
                report_keyword_limit: 20,
                alert_window_hours: 24,
                alert_spike_threshold: 5,
                anonymize_authors: false,
                reindex_batch_size: 200,
            },
            alert_rules: [{
                id: 'alert-1',
                name: '장애 급증',
                major_category: '장애',
                sub_category: '',
                keyword: '',
                threshold: 5,
                window_hours: 24,
                enabled: true,
                local_id: 'rule-local-1',
            }],
            ai: {
                vllm_url: '',
                vllm_key: '',
                vllm_model: '',
            },
        };

        const next = normalizeAdminConfig({
            scope: previous.scope,
            operations: previous.operations,
            dictionaries: [{
                id: 'dict-outage',
                major_category: '장애',
                sub_category: '시스템 장애',
                purpose: '장애 탐지',
                keywords: ['다운', '응답없음'],
                enabled: true,
            }],
            stopwords: ['그리고'],
            alert_rules: [{
                id: 'alert-1',
                name: '장애 급증',
                major_category: '장애',
                sub_category: '',
                keyword: '',
                threshold: 5,
                window_hours: 24,
                enabled: true,
            }],
            ai: previous.ai,
        }, previous);

        expect(next.alert_rules[0].local_id).toBe('rule-local-1');
    });

    test('parseStoredConfigValue ignores predefined dictionary payloads in draft state', () => {
        const result = parseStoredConfigValue(JSON.stringify({
            scope: {
                included_team_ids: [],
                included_channel_ids: [],
                excluded_channel_ids: [],
                include_public_channel: true,
                include_private_channel: true,
                include_direct_channel: false,
                include_group_channel: false,
                include_threads: true,
                exclude_bot_messages: true,
                exclude_system_messages: true,
            },
            operations: {
                retention_days: 90,
                hot_topic_limit: 10,
                report_keyword_limit: 20,
                alert_window_hours: 24,
                alert_spike_threshold: 5,
                anonymize_authors: false,
                reindex_batch_size: 200,
            },
            dictionaries: [{
                id: 'dict-ops',
                major_category: '운영요청',
                sub_category: '문의',
                purpose: 'FAQ',
                keywords: ['문의'],
                enabled: true,
            }],
            stopwords: ['그리고'],
            alert_rules: [],
            ai: {
                vllm_url: '',
                vllm_key: '',
                vllm_model: '',
            },
        }));

        expect(result.ok).toBe(true);
        expect(result.config).not.toHaveProperty('dictionaries');
        expect(result.config).not.toHaveProperty('stopwords');
        expect(result.config.alert_rules).toEqual([]);
    });
});
