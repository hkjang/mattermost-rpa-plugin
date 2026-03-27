import React, {useEffect, useMemo, useRef, useState} from 'react';

import type {AdminLookupCatalog, AdminPluginConfig, AlertRule, DictionaryEntry, PluginStatus, StatsResponse} from '../client';
import {buildReportURL, getAdminConfig, getStats, startReindex} from '../client';

type CustomSettingProps = {
    id?: string;
    value?: unknown;
    disabled?: boolean;
    setByEnv?: boolean;
    onChange: (id: string, value: unknown) => void;
    setSaveNeeded?: () => void;
};

type DraftDictionaryEntry = DictionaryEntry & {local_id: string};
type DraftAlertRule = AlertRule & {local_id: string};
type DraftPluginConfig = AdminPluginConfig & {
    dictionaries: DraftDictionaryEntry[];
    alert_rules: DraftAlertRule[];
};
type FilterPreset = 'today'|'7d'|'30d'|'custom';

const sectionStyle: React.CSSProperties = {background: 'var(--center-channel-bg)', border: '1px solid rgba(63,67,80,.12)', borderRadius: 18, display: 'grid', gap: 14, padding: 20};
const gridStyle: React.CSSProperties = {display: 'grid', gap: 12, gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))'};
const fieldStyle: React.CSSProperties = {border: '1px solid rgba(63,67,80,.16)', borderRadius: 12, padding: '10px 12px', width: '100%'};
const buttonStyle: React.CSSProperties = {background: 'var(--button-bg)', border: 'none', borderRadius: 999, color: 'var(--button-color)', cursor: 'pointer', fontSize: 13, fontWeight: 700, padding: '9px 14px'};
const subtleButtonStyle: React.CSSProperties = {...buttonStyle, background: 'rgba(63,67,80,.08)', color: 'var(--center-channel-color)'};

export default function ConfigSetting(props: CustomSettingProps) {
    const settingKey = props.id || 'Config';
    const disabled = Boolean(props.disabled || props.setByEnv);
    const timezoneOffsetMinutes = -new Date().getTimezoneOffset();
    const [config, setConfig] = useState<DraftPluginConfig>(createDefaultConfig());
    const [status, setStatus] = useState<PluginStatus | null>(null);
    const [catalog, setCatalog] = useState<AdminLookupCatalog>({teams: [], channels: []});
    const [stats, setStats] = useState<StatsResponse | null>(null);
    const [source, setSource] = useState('config');
    const [loadError, setLoadError] = useState('');
    const [statsError, setStatsError] = useState('');
    const [preset, setPreset] = useState<FilterPreset>('7d');
    const [customRange, setCustomRange] = useState(defaultRange());
    const [keywordFilter, setKeywordFilter] = useState('');
    const [categoryFilter, setCategoryFilter] = useState('');
    const [channelFilter, setChannelFilter] = useState('');
    const [dictionarySearch, setDictionarySearch] = useState('');
    const [reindexing, setReindexing] = useState(false);
    const [reindexMessage, setReindexMessage] = useState('');
    const lastSubmittedValueRef = useRef('');
    const configRef = useRef(config);

    useEffect(() => {
        configRef.current = config;
    }, [config]);

    useEffect(() => {
        const parsed = parseStoredConfigValue(props.value, configRef.current);
        if (!parsed.ok) {
            return;
        }
        const nextValue = serialize(buildStoredConfig(parsed.config));
        if (nextValue !== lastSubmittedValueRef.current) {
            setConfig(parsed.config);
            lastSubmittedValueRef.current = nextValue;
        }
    }, [props.value]);

    useEffect(() => {
        let cancelled = false;
        getAdminConfig().then((response) => {
            if (cancelled) {
                return;
            }
            const nextConfig = normalizeAdminConfig(response.config, configRef.current);
            setConfig(nextConfig);
            setStatus(response.status);
            setCatalog(response.catalog);
            setSource(response.source);
            lastSubmittedValueRef.current = serialize(buildStoredConfig(nextConfig));
        }).catch((error) => {
            if (!cancelled) {
                setLoadError(error instanceof Error ? error.message : '설정을 불러오지 못했습니다.');
            }
        });
        return () => {
            cancelled = true;
        };
    }, []);

    const range = useMemo(() => resolveRange(preset, customRange), [preset, customRange]);

    useEffect(() => {
        let cancelled = false;
        getStats({
            fromDate: range.fromDate,
            toDate: range.toDate,
            timezoneOffsetMinutes,
            keyword: keywordFilter || undefined,
            majorCategory: categoryFilter || undefined,
            channelID: channelFilter || undefined,
        }).then((response) => {
            if (!cancelled) {
                setStats(response);
                setStatsError('');
            }
        }).catch((error) => {
            if (!cancelled) {
                setStatsError(error instanceof Error ? error.message : '통계를 불러오지 못했습니다.');
            }
        });
        return () => {
            cancelled = true;
        };
    }, [range.fromDate, range.toDate, timezoneOffsetMinutes, keywordFilter, categoryFilter, channelFilter]);

    const validationMessages = useMemo(() => validateConfig(config), [config]);
    const filteredDictionaries = useMemo(() => {
        const query = dictionarySearch.trim().toLowerCase();
        return query ? config.dictionaries.filter((entry) => `${entry.major_category} ${entry.sub_category} ${entry.purpose} ${entry.keywords.join(' ')}`.toLowerCase().includes(query)) : config.dictionaries;
    }, [config.dictionaries, dictionarySearch]);

    const applyConfig = (nextConfig: DraftPluginConfig) => {
        setConfig(nextConfig);
        const nextValue = JSON.stringify(buildStoredConfig(nextConfig), null, 2);
        lastSubmittedValueRef.current = nextValue;
        props.onChange(settingKey, nextValue);
        props.setSaveNeeded?.();
    };

    const update = (patch: Partial<DraftPluginConfig>) => applyConfig({...config, ...patch});
    const updateScope = (key: keyof AdminPluginConfig['scope'], value: string[]|boolean) => update({scope: {...config.scope, [key]: value}});
    const updateOps = (key: keyof AdminPluginConfig['operations'], value: number|boolean) => update({operations: {...config.operations, [key]: value}});
    const updateAI = (key: keyof AdminPluginConfig['ai'], value: string) => update({ai: {...config.ai, [key]: value}});

    const openReport = (format: 'json'|'csv') => window.open(buildReportURL({format, fromDate: range.fromDate, toDate: range.toDate, timezoneOffsetMinutes, keyword: keywordFilter || undefined, majorCategory: categoryFilter || undefined, channelID: channelFilter || undefined}), '_blank', 'noopener,noreferrer');
    const addDictionary = () => update({dictionaries: [...config.dictionaries, emptyDictionary()]});
    const addRule = () => update({alert_rules: [...config.alert_rules, emptyRule()]});

    const runReindex = async () => {
        setReindexing(true);
        setReindexMessage('');
        try {
            const result = await startReindex({fromDate: range.fromDate, toDate: range.toDate, timezoneOffsetMinutes, channelIDs: config.scope.included_channel_ids});
            setReindexMessage(`${result.processed_channels}개 채널, ${result.processed_posts}개 게시글 재분석 완료`);
        } catch (error) {
            setReindexMessage(error instanceof Error ? error.message : '재색인 실패');
        } finally {
            setReindexing(false);
        }
    };

    return (
        <div style={{display: 'grid', gap: 18, paddingBottom: 24}}>
            <section style={{...sectionStyle, background: 'linear-gradient(140deg, rgba(13,110,253,.1), rgba(242,159,5,.18))'}}>
                <div style={{fontSize: 12, fontWeight: 700, letterSpacing: '.08em', textTransform: 'uppercase'}}>Mattermost RPA 대화 분석 플러그인</div>
                <div style={{fontSize: 28, fontWeight: 800, lineHeight: 1.15}}>조직 내 IT 대화에서 운영 이슈, 키워드, 요청 패턴을 분석합니다.</div>
                <div style={{color: 'rgba(63,67,80,.72)', fontSize: 14, lineHeight: 1.5}}>채널과 스레드 메시지를 사전 기반으로 분류해 장애, 배포, 인프라, 보안, 운영요청 등 주제를 시각화하고 핫토픽과 이상 징후를 빠르게 확인할 수 있습니다.</div>
                <div style={{display: 'flex', flexWrap: 'wrap', gap: 10}}>
                    <Pill text={`설정 원본: ${source}`}/>
                    <Pill text={`사전 ${status?.dictionary_entries || config.dictionaries.length}`}/>
                    <Pill text={`알림 ${status?.alert_rules || config.alert_rules.length}`}/>
                    <Pill text={`분석 일수 ${status?.analyzed_days || 0}`}/>
                </div>
            </section>

            {loadError ? <div style={{...sectionStyle, color: 'var(--error-text)'}}>{loadError}</div> : null}
            {statsError ? <div style={{...sectionStyle, color: 'var(--error-text)'}}>{statsError}</div> : null}
            {validationMessages.length ? <div style={{...sectionStyle, color: 'var(--error-text)'}}>{validationMessages.join(' / ')}</div> : <div style={sectionStyle}>설정 형식은 정상입니다. 관리자 저장 시 서버에 즉시 반영됩니다.</div>}

            <section style={sectionStyle}>
                <div style={{fontSize: 18, fontWeight: 700}}>필터 및 운영</div>
                <div style={gridStyle}>
                    <select style={fieldStyle} value={preset} onChange={(e) => setPreset(e.target.value as FilterPreset)}><option value='today'>오늘</option><option value='7d'>최근 7일</option><option value='30d'>최근 30일</option><option value='custom'>사용자 지정</option></select>
                    <select style={fieldStyle} value={categoryFilter} onChange={(e) => setCategoryFilter(e.target.value)}><option value=''>전체 분류</option>{(stats?.available_categories || []).map((value) => <option key={value} value={value}>{value}</option>)}</select>
                    <select style={fieldStyle} value={keywordFilter} onChange={(e) => setKeywordFilter(e.target.value)}><option value=''>전체 키워드</option>{(stats?.available_keywords || []).map((value) => <option key={value} value={value}>{value}</option>)}</select>
                    <select style={fieldStyle} value={channelFilter} onChange={(e) => setChannelFilter(e.target.value)}><option value=''>전체 채널</option>{(stats?.available_channels || []).map((channel) => <option key={channel.id} value={channel.id}>{`${channel.team_name || '-'} / ${channel.display_name}`}</option>)}</select>
                    <input disabled={preset !== 'custom'} style={fieldStyle} type='date' value={customRange.fromDate} onChange={(e) => setCustomRange((current) => ({...current, fromDate: e.target.value}))}/>
                    <input disabled={preset !== 'custom'} style={fieldStyle} type='date' value={customRange.toDate} onChange={(e) => setCustomRange((current) => ({...current, toDate: e.target.value}))}/>
                </div>
                <div style={{display: 'flex', flexWrap: 'wrap', gap: 10}}>
                    <button disabled={reindexing} style={buttonStyle} type='button' onClick={runReindex}>{reindexing ? '재색인 중...' : '현재 범위 재색인'}</button>
                    <button style={subtleButtonStyle} type='button' onClick={() => openReport('json')}>JSON 리포트</button>
                    <button style={subtleButtonStyle} type='button' onClick={() => openReport('csv')}>CSV 리포트</button>
                    <button style={subtleButtonStyle} type='button' onClick={() => window.print()}>PDF 인쇄</button>
                </div>
                {reindexMessage ? <div>{reindexMessage}</div> : null}
            </section>

            <section style={sectionStyle}>
                <div style={gridStyle}>
                    <Metric label='분석 메시지' value={stats?.summary.analyzed_messages}/>
                    <Metric label='작성자 수' value={stats?.summary.unique_authors}/>
                    <Metric label='채널 수' value={stats?.summary.unique_channels}/>
                    <Metric label='긴급 메시지' value={stats?.summary.urgent_messages}/>
                </div>
                <div style={{display: 'flex', flexWrap: 'wrap', gap: 8}}>
                    {(stats?.tag_cloud || []).slice(0, 20).map((item) => (
                        <button
                            key={`${item.major_category}-${item.sub_category}-${item.keyword}`}
                            style={{background: 'rgba(13,110,253,.08)', border: '1px solid rgba(13,110,253,.14)', borderRadius: 999, cursor: 'pointer', fontSize: `${12 + Math.min(item.count, 12)}px`, fontWeight: 700, padding: '6px 10px'}}
                            type='button'
                            onClick={() => setKeywordFilter(item.keyword)}
                        >
                            {`${item.keyword} (${item.count})`}
                        </button>
                    ))}
                </div>
                <SimpleTable headers={['일자', '메시지', '긴급']} rows={(stats?.trend || []).map((item) => [item.date, String(item.messages), String(item.urgent_messages)])}/>
            </section>

            <section style={sectionStyle}>
                <div style={{fontSize: 18, fontWeight: 700}}>키워드 / 카테고리 / 채널</div>
                <SimpleTable headers={['키워드', '분류', '빈도']} rows={(stats?.keyword_table || []).slice(0, 20).map((item) => [item.keyword, `${item.major_category} > ${item.sub_category}`, String(item.count)])}/>
                <SimpleTable headers={['대분류', '빈도', '상위 중분류']} rows={(stats?.categories || []).map((item) => [item.major_category, String(item.count), item.subcategories.slice(0, 3).map((entry) => `${entry.sub_category}(${entry.count})`).join(', ')])}/>
                <SimpleTable headers={['채널', '유형', '메시지 수', '대표 분류', '상위 키워드']} rows={(stats?.channels || []).map((item) => [`${item.team_name || '-'} / ${item.channel_name}`, item.channel_type, String(item.message_count), item.top_category || '-', item.top_keywords.join(', ') || '-'])}/>
            </section>

            <section style={sectionStyle}>
                <div style={{fontSize: 18, fontWeight: 700}}>핫토픽 / 알림 / 메시지 상세</div>
                <SimpleTable headers={['키워드', '현재', '이전', '증가']} rows={(stats?.hot_topics || []).map((item) => [item.keyword, String(item.count), String(item.previous_count), String(item.delta)])}/>
                <SimpleTable headers={['알림', '상태', '현재', '기준']} rows={(stats?.alerts || []).map((item) => [item.name, item.status, String(item.count), String(item.threshold)])}/>
                <SimpleTable headers={['채널', '작성자', '긴급도', '본문']} rows={(stats?.messages || []).map((item) => [`${item.team_name || '-'} / ${item.channel_name}`, item.author_display_name, item.urgency_score.toFixed(0), item.preview])}/>
            </section>

            <section style={sectionStyle}>
                <div style={{fontSize: 18, fontWeight: 700}}>관리자 설정</div>
                <div style={gridStyle}>
                    <Multi label='분석 대상 팀' disabled={disabled} options={catalog.teams.map((item) => ({label: item.display_name || item.name, value: item.id}))} value={config.scope.included_team_ids} onChange={(value) => updateScope('included_team_ids', value)}/>
                    <Multi label='분석 대상 채널' disabled={disabled} options={catalog.channels.map((item) => ({label: `${item.team_name || '-'} / ${item.display_name}`, value: item.id}))} value={config.scope.included_channel_ids} onChange={(value) => updateScope('included_channel_ids', value)}/>
                    <Multi label='제외 채널' disabled={disabled} options={catalog.channels.map((item) => ({label: `${item.team_name || '-'} / ${item.display_name}`, value: item.id}))} value={config.scope.excluded_channel_ids} onChange={(value) => updateScope('excluded_channel_ids', value)}/>
                    <Check label='공개 채널 포함' checked={config.scope.include_public_channel} onChange={(value) => updateScope('include_public_channel', value)}/>
                    <Check label='비공개 채널 포함' checked={config.scope.include_private_channel} onChange={(value) => updateScope('include_private_channel', value)}/>
                    <Check label='스레드 포함' checked={config.scope.include_threads} onChange={(value) => updateScope('include_threads', value)}/>
                    <Check label='봇 메시지 제외' checked={config.scope.exclude_bot_messages} onChange={(value) => updateScope('exclude_bot_messages', value)}/>
                    <Check label='시스템 메시지 제외' checked={config.scope.exclude_system_messages} onChange={(value) => updateScope('exclude_system_messages', value)}/>
                    <Num label='보관 기간(일)' value={config.operations.retention_days} onChange={(value) => updateOps('retention_days', value)}/>
                    <Num label='핫토픽 표시 수' value={config.operations.hot_topic_limit} onChange={(value) => updateOps('hot_topic_limit', value)}/>
                    <Num label='리포트 키워드 수' value={config.operations.report_keyword_limit} onChange={(value) => updateOps('report_keyword_limit', value)}/>
                    <Num label='알림 창(시간)' value={config.operations.alert_window_hours} onChange={(value) => updateOps('alert_window_hours', value)}/>
                    <Num label='알림 임계치' value={config.operations.alert_spike_threshold} onChange={(value) => updateOps('alert_spike_threshold', value)}/>
                    <Num label='재색인 배치 크기' value={config.operations.reindex_batch_size} onChange={(value) => updateOps('reindex_batch_size', value)}/>
                    <Check label='작성자 익명화' checked={config.operations.anonymize_authors} onChange={(value) => updateOps('anonymize_authors', value)}/>
                    <Text label='vLLM API URL' value={config.ai.vllm_url} onChange={(value) => updateAI('vllm_url', value)}/>
                    <Text label='vLLM API Key' value={config.ai.vllm_key} onChange={(value) => updateAI('vllm_key', value)}/>
                </div>
            </section>

            <section style={sectionStyle}>
                <div style={{display: 'flex', flexWrap: 'wrap', gap: 10, justifyContent: 'space-between'}}>
                    <div style={{fontSize: 18, fontWeight: 700}}>사전 및 알림 규칙</div>
                    <div style={{display: 'flex', gap: 10}}>
                        <input placeholder='사전 검색' style={{...fieldStyle, maxWidth: 240}} type='text' value={dictionarySearch} onChange={(e) => setDictionarySearch(e.target.value)}/>
                        <button disabled={disabled} style={buttonStyle} type='button' onClick={addDictionary}>사전 항목 추가</button>
                        <button disabled={disabled} style={subtleButtonStyle} type='button' onClick={addRule}>알림 규칙 추가</button>
                    </div>
                </div>
                <label>
                    <div>불용어(쉼표 기준)</div>
                    <textarea disabled={disabled} style={{...textareaStyle, marginTop: 8}} value={config.stopwords.join(', ')} onChange={(e) => update({stopwords: splitCSV(e.target.value)})}/>
                </label>
                {filteredDictionaries.map((entry) => (
                    <div key={entry.local_id} style={{display: 'grid', gap: 10, padding: 12, border: '1px solid rgba(63,67,80,.08)', borderRadius: 14}}>
                        <div style={gridStyle}>
                            <Text label='대분류' value={entry.major_category} onChange={(value) => update({dictionaries: config.dictionaries.map((item) => item.local_id === entry.local_id ? {...item, major_category: value} : item)})}/>
                            <Text label='중분류' value={entry.sub_category} onChange={(value) => update({dictionaries: config.dictionaries.map((item) => item.local_id === entry.local_id ? {...item, sub_category: value} : item)})}/>
                            <Text label='활용 목적' value={entry.purpose} onChange={(value) => update({dictionaries: config.dictionaries.map((item) => item.local_id === entry.local_id ? {...item, purpose: value} : item)})}/>
                            <Text label='키워드' value={entry.keywords.join(', ')} onChange={(value) => update({dictionaries: config.dictionaries.map((item) => item.local_id === entry.local_id ? {...item, keywords: splitCSV(value)} : item)})}/>
                        </div>
                        <Check label='활성화' checked={entry.enabled} onChange={(value) => update({dictionaries: config.dictionaries.map((item) => item.local_id === entry.local_id ? {...item, enabled: value} : item)})}/>
                    </div>
                ))}
                {config.alert_rules.map((rule) => (
                    <div key={rule.local_id} style={{display: 'grid', gap: 10, padding: 12, border: '1px solid rgba(63,67,80,.08)', borderRadius: 14}}>
                        <div style={gridStyle}>
                            <Text label='규칙 이름' value={rule.name} onChange={(value) => update({alert_rules: config.alert_rules.map((item) => item.local_id === rule.local_id ? {...item, name: value} : item)})}/>
                            <Text label='대분류' value={rule.major_category} onChange={(value) => update({alert_rules: config.alert_rules.map((item) => item.local_id === rule.local_id ? {...item, major_category: value} : item)})}/>
                            <Text label='중분류' value={rule.sub_category} onChange={(value) => update({alert_rules: config.alert_rules.map((item) => item.local_id === rule.local_id ? {...item, sub_category: value} : item)})}/>
                            <Text label='키워드' value={rule.keyword} onChange={(value) => update({alert_rules: config.alert_rules.map((item) => item.local_id === rule.local_id ? {...item, keyword: value} : item)})}/>
                            <Num label='임계치' value={rule.threshold} onChange={(value) => update({alert_rules: config.alert_rules.map((item) => item.local_id === rule.local_id ? {...item, threshold: value} : item)})}/>
                            <Num label='시간 창' value={rule.window_hours} onChange={(value) => update({alert_rules: config.alert_rules.map((item) => item.local_id === rule.local_id ? {...item, window_hours: value} : item)})}/>
                        </div>
                        <Check label='활성화' checked={rule.enabled} onChange={(value) => update({alert_rules: config.alert_rules.map((item) => item.local_id === rule.local_id ? {...item, enabled: value} : item)})}/>
                    </div>
                ))}
                <pre style={{margin: 0, overflowX: 'auto', whiteSpace: 'pre-wrap'}}>{JSON.stringify(buildStoredConfig(config), null, 2)}</pre>
            </section>
        </div>
    );
}

function Metric(props: {label: string; value?: number}) { return <div style={sectionStyle}><strong>{props.label}</strong><div style={{fontSize: 28, fontWeight: 800}}>{(props.value ?? 0).toLocaleString()}</div></div>; }
function Pill(props: {text: string}) { return <span style={{background: 'rgba(25,32,56,.08)', borderRadius: 999, fontSize: 12, fontWeight: 700, padding: '6px 10px'}}>{props.text}</span>; }
function SimpleTable(props: {headers: string[]; rows: string[][]}) { return <div style={{overflowX: 'auto'}}><table style={{borderCollapse: 'collapse', width: '100%'}}><thead><tr>{props.headers.map((header) => <th key={header} style={{borderBottom: '1px solid rgba(63,67,80,.08)', fontSize: 12, padding: '8px 10px', textAlign: 'left'}}>{header}</th>)}</tr></thead><tbody>{props.rows.map((row, i) => <tr key={i}>{row.map((cell, j) => <td key={`${i}-${j}`} style={{borderBottom: '1px solid rgba(63,67,80,.08)', fontSize: 13, padding: '10px'}}>{cell}</td>)}</tr>)}</tbody></table></div>; }
function Text(props: {label: string; value: string; onChange: (value: string) => void}) { return <label><div>{props.label}</div><input style={{...fieldStyle, marginTop: 8}} type='text' value={props.value} onChange={(e) => props.onChange(e.target.value)}/></label>; }
function Num(props: {label: string; value: number; onChange: (value: number) => void}) { return <label><div>{props.label}</div><input style={{...fieldStyle, marginTop: 8}} min={1} type='number' value={props.value} onChange={(e) => props.onChange(parsePositiveNumber(e.target.value, 1))}/></label>; }
function Check(props: {label: string; checked: boolean; onChange: (value: boolean) => void}) { return <label style={{alignItems: 'center', display: 'flex', gap: 8}}><input checked={props.checked} type='checkbox' onChange={(e) => props.onChange(e.target.checked)}/>{props.label}</label>; }
function Multi(props: {label: string; value: string[]; options: Array<{label: string; value: string}>; disabled?: boolean; onChange: (value: string[]) => void}) { return <label><div>{props.label}</div><select disabled={props.disabled} multiple={true} size={8} style={{...fieldStyle, marginTop: 8, minHeight: 180}} value={props.value} onChange={(e) => props.onChange(Array.from(e.target.selectedOptions).map((item) => item.value))}>{props.options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select></label>; }

function createDefaultConfig(): DraftPluginConfig {
    return {scope: {included_team_ids: [], included_channel_ids: [], excluded_channel_ids: [], include_public_channel: true, include_private_channel: true, include_direct_channel: false, include_group_channel: false, include_threads: true, exclude_bot_messages: true, exclude_system_messages: true}, operations: {retention_days: 90, hot_topic_limit: 10, report_keyword_limit: 20, alert_window_hours: 24, alert_spike_threshold: 5, anonymize_authors: false, reindex_batch_size: 200}, dictionaries: [], stopwords: [], alert_rules: [], ai: {vllm_url: '', vllm_key: ''}};
}
function defaultRange() { const today = dateInputValue(new Date()); return {fromDate: addDays(today, -6), toDate: today}; }
export function normalizeAdminConfig(value?: AdminPluginConfig, previous?: DraftPluginConfig): DraftPluginConfig { const next = createDefaultConfig(); if (!value) { return next; } return {...next, ...value, scope: {...next.scope, ...value.scope}, operations: {...next.operations, ...value.operations}, ai: {...next.ai, ...value.ai}, stopwords: Array.isArray(value.stopwords) ? value.stopwords : [], dictionaries: (value.dictionaries || []).map((item, index) => ({...item, keywords: Array.isArray(item.keywords) ? item.keywords : [], local_id: previous?.dictionaries.find((candidate) => candidate.id === item.id)?.local_id || `dict-${index}-${item.id || index}`})), alert_rules: (value.alert_rules || []).map((item, index) => ({...item, local_id: previous?.alert_rules.find((candidate) => candidate.id === item.id)?.local_id || `rule-${index}-${item.id || index}`}))}; }
function buildStoredConfig(config: DraftPluginConfig): AdminPluginConfig { return {...config, dictionaries: config.dictionaries.map(({local_id, ...item}) => ({...item, id: item.id || local_id, keywords: splitCSV(item.keywords.join(', '))})), alert_rules: config.alert_rules.map(({local_id, ...item}) => ({...item, id: item.id || local_id}))}; }
export function parseStoredConfigValue(value: unknown, previous?: DraftPluginConfig) { if (value == null || value === '') { return {ok: false, config: createDefaultConfig()}; } const raw = typeof value === 'string' ? value : serialize(value); try { return {ok: true, config: normalizeAdminConfig(JSON.parse(raw) as AdminPluginConfig, previous)}; } catch { return {ok: false, config: createDefaultConfig()}; } }
function validateConfig(config: DraftPluginConfig) { const messages: string[] = []; if (config.dictionaries.filter((item) => item.enabled && item.keywords.length > 0).length === 0) { messages.push('활성 사전 항목이 필요합니다.'); } if (!config.scope.include_public_channel && !config.scope.include_private_channel && !config.scope.include_direct_channel) { messages.push('채널 유형을 최소 하나 선택해 주세요.'); } return messages; }
function emptyDictionary(): DraftDictionaryEntry { return {id: '', major_category: '', sub_category: '', purpose: '', keywords: [], enabled: true, local_id: `dict-${Date.now()}`}; }
function emptyRule(): DraftAlertRule { return {id: '', name: '', major_category: '', sub_category: '', keyword: '', threshold: 5, window_hours: 24, enabled: true, local_id: `rule-${Date.now()}`}; }
function resolveRange(preset: FilterPreset, customRange: {fromDate: string; toDate: string}) { const today = dateInputValue(new Date()); if (preset === 'today') { return {fromDate: today, toDate: today}; } if (preset === '30d') { return {fromDate: addDays(today, -29), toDate: today}; } if (preset === 'custom') { return customRange; } return {fromDate: addDays(today, -6), toDate: today}; }
function serialize(value: unknown) { try { return typeof value === 'string' ? value : JSON.stringify(value); } catch { return ''; } }
function splitCSV(value: string|string[]) { const list = Array.isArray(value) ? value : value.split(','); return [...new Set(list.map((item) => item.trim()).filter(Boolean))]; }
function parsePositiveNumber(value: unknown, fallback: number) { const parsed = Number(value); return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback; }
function dateInputValue(date: Date) { return `${date.getFullYear()}-${`${date.getMonth() + 1}`.padStart(2, '0')}-${`${date.getDate()}`.padStart(2, '0')}`; }
function addDays(dateValue: string, offset: number) { const date = new Date(`${dateValue}T00:00:00`); date.setDate(date.getDate() + offset); return dateInputValue(date); }
