package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	defaultAISummaryTimeout = 45 * time.Second
	defaultAISummaryTokens  = 1200
)

type aiSummaryFilter struct {
	Keyword       string `json:"keyword,omitempty"`
	MajorCategory string `json:"major_category,omitempty"`
	ChannelID     string `json:"channel_id,omitempty"`
}

type aiSummaryRequest struct {
	FromDate              string `json:"from_date"`
	ToDate                string `json:"to_date"`
	TimezoneOffsetMinutes int    `json:"timezone_offset_minutes"`
	Keyword               string `json:"keyword,omitempty"`
	MajorCategory         string `json:"major_category,omitempty"`
	ChannelID             string `json:"channel_id,omitempty"`
}

type aiSummaryResponse struct {
	Range              statsRange      `json:"range"`
	Filter             aiSummaryFilter `json:"filter"`
	Model              string          `json:"model"`
	GeneratedAt        int64           `json:"generated_at"`
	SourceMessageCount int             `json:"source_message_count"`
	BotRequestCount    int             `json:"bot_request_count"`
	Summary            string          `json:"summary"`
	Highlights         []string        `json:"highlights"`
	Risks              []string        `json:"risks"`
	RecommendedActions []string        `json:"recommended_actions"`
	Watchouts          []string        `json:"watchouts"`
}

type aiPromptEnvelope struct {
	GeneratedAt      string            `json:"generated_at"`
	Range            statsRange        `json:"range"`
	Filter           aiSummaryFilter   `json:"filter"`
	Summary          analyticsSummary  `json:"summary"`
	Trend            []trendPoint      `json:"trend"`
	TopKeywords      []keywordStat     `json:"top_keywords"`
	TopCategories    []categoryStat    `json:"top_categories"`
	TopChannels      []channelStat     `json:"top_channels"`
	HotTopics        []keywordStat     `json:"hot_topics"`
	Alerts           []alertStatus     `json:"alerts"`
	RecentMessages   []aiPromptMessage `json:"recent_messages"`
	RecentBotRequest []aiPromptMessage `json:"recent_bot_requests"`
}

type aiPromptMessage struct {
	When         string   `json:"when"`
	Channel      string   `json:"channel,omitempty"`
	BotName      string   `json:"bot_name,omitempty"`
	Categories   []string `json:"categories,omitempty"`
	Keywords     []string `json:"keywords,omitempty"`
	UrgencyScore float64  `json:"urgency_score"`
	Preview      string   `json:"preview"`
}

type aiGeneratedSummary struct {
	Summary            string   `json:"summary"`
	Highlights         []string `json:"highlights"`
	Risks              []string `json:"risks"`
	RecommendedActions []string `json:"recommended_actions"`
	Watchouts          []string `json:"watchouts"`
}

type vllmChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type vllmChatCompletionRequest struct {
	Model       string            `json:"model"`
	Messages    []vllmChatMessage `json:"messages"`
	Temperature float64           `json:"temperature"`
	MaxTokens   int               `json:"max_tokens"`
	Stream      bool              `json:"stream"`
}

type vllmChatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *Plugin) generateAISummary(request aiSummaryRequest) (aiSummaryResponse, error) {
	runtimeCfg, err := p.getRuntimeConfiguration()
	if err != nil {
		return aiSummaryResponse{}, err
	}
	if err := validateVLLMSettings(runtimeCfg.AI); err != nil {
		return aiSummaryResponse{}, err
	}

	rangeValue, fromUTC, toUTC, err := buildStatsRange(request.FromDate, request.ToDate, request.TimezoneOffsetMinutes)
	if err != nil {
		return aiSummaryResponse{}, err
	}

	stats, err := p.buildStats(request.FromDate, request.ToDate, request.Keyword, request.MajorCategory, request.ChannelID, request.TimezoneOffsetMinutes)
	if err != nil {
		return aiSummaryResponse{}, err
	}

	allRecords, err := p.loadMessageRecords(fromUTC, toUTC)
	if err != nil {
		return aiSummaryResponse{}, err
	}
	filteredRecords := filterRecords(allRecords, request.Keyword, request.MajorCategory, request.ChannelID)
	filter := aiSummaryFilter{
		Keyword:       strings.TrimSpace(request.Keyword),
		MajorCategory: strings.TrimSpace(request.MajorCategory),
		ChannelID:     strings.TrimSpace(request.ChannelID),
	}

	response := aiSummaryResponse{
		Range:              rangeValue,
		Filter:             filter,
		Model:              runtimeCfg.AI.VLLMModel,
		GeneratedAt:        time.Now().UnixMilli(),
		SourceMessageCount: len(filteredRecords),
		BotRequestCount:    countBotRequests(filteredRecords),
	}
	if len(filteredRecords) == 0 {
		response.Summary = "선택한 범위에서 분석된 메시지가 없어 AI 요약을 생성하지 않았습니다."
		return response, nil
	}

	promptPayload := buildAISummaryPromptPayload(rangeValue, filter, request.TimezoneOffsetMinutes, stats, filteredRecords)
	content, err := callVLLMChatCompletion(runtimeCfg.AI, promptPayload)
	if err != nil {
		return aiSummaryResponse{}, err
	}

	generated := parseAISummaryContent(content)
	if generated.Summary == "" {
		generated.Summary = "vLLM 응답을 받았지만 구조화된 요약을 해석하지 못해 원문을 표시합니다. " + trimForStorage(strings.TrimSpace(content), 1200)
	}

	response.Summary = generated.Summary
	response.Highlights = generated.Highlights
	response.Risks = generated.Risks
	response.RecommendedActions = generated.RecommendedActions
	response.Watchouts = generated.Watchouts
	return response, nil
}

func validateVLLMSettings(settings AISettings) error {
	switch {
	case strings.TrimSpace(settings.VLLMURL) == "":
		return fmt.Errorf("vLLM API URL을 먼저 설정해 주세요")
	case strings.TrimSpace(settings.VLLMKey) == "":
		return fmt.Errorf("vLLM API Key를 먼저 설정해 주세요")
	case strings.TrimSpace(settings.VLLMModel) == "":
		return fmt.Errorf("vLLM 모델명을 먼저 설정해 주세요")
	default:
		return nil
	}
}

func buildAISummaryPromptPayload(rangeValue statsRange, filter aiSummaryFilter, offsetMinutes int, stats statsResponse, records []analyzedMessageRecord) aiPromptEnvelope {
	return aiPromptEnvelope{
		GeneratedAt:      formatLocalDateTime(time.Now().UTC(), offsetMinutes),
		Range:            rangeValue,
		Filter:           filter,
		Summary:          stats.Summary,
		Trend:            limitTrendPoints(stats.Trend, 14),
		TopKeywords:      limitKeywordStats(stats.KeywordTable, 12),
		TopCategories:    limitCategoryStats(stats.Categories, 6),
		TopChannels:      limitChannelStats(stats.Channels, 6),
		HotTopics:        limitKeywordStats(stats.HotTopics, 8),
		Alerts:           limitAlertStatuses(stats.Alerts, 6),
		RecentMessages:   selectPromptMessages(records, offsetMinutes, 12, false),
		RecentBotRequest: selectPromptMessages(records, offsetMinutes, 10, true),
	}
}

func limitTrendPoints(points []trendPoint, limit int) []trendPoint {
	if limit <= 0 || len(points) <= limit {
		return append([]trendPoint{}, points...)
	}
	return append([]trendPoint{}, points[len(points)-limit:]...)
}

func limitCategoryStats(rows []categoryStat, limit int) []categoryStat {
	if limit <= 0 || len(rows) <= limit {
		return append([]categoryStat{}, rows...)
	}
	return append([]categoryStat{}, rows[:limit]...)
}

func limitChannelStats(rows []channelStat, limit int) []channelStat {
	if limit <= 0 || len(rows) <= limit {
		return append([]channelStat{}, rows...)
	}
	return append([]channelStat{}, rows[:limit]...)
}

func limitAlertStatuses(rows []alertStatus, limit int) []alertStatus {
	filtered := make([]alertStatus, 0, len(rows))
	for _, row := range rows {
		if row.Status == "triggered" || row.Count > 0 {
			filtered = append(filtered, row)
		}
	}
	if limit <= 0 || len(filtered) <= limit {
		return filtered
	}
	return append([]alertStatus{}, filtered[:limit]...)
}

func selectPromptMessages(records []analyzedMessageRecord, offsetMinutes int, limit int, onlyBotRequests bool) []aiPromptMessage {
	if len(records) == 0 || limit <= 0 {
		return nil
	}

	selected := make([]analyzedMessageRecord, 0, len(records))
	for _, record := range records {
		if onlyBotRequests && !record.IsBotRequest {
			continue
		}
		selected = append(selected, record)
	}

	sort.Slice(selected, func(i, j int) bool {
		if selected[i].CreatedAt == selected[j].CreatedAt {
			return selected[i].MessageID > selected[j].MessageID
		}
		return selected[i].CreatedAt > selected[j].CreatedAt
	})

	if len(selected) > limit {
		selected = selected[:limit]
	}

	rows := make([]aiPromptMessage, 0, len(selected))
	for _, record := range selected {
		rows = append(rows, aiPromptMessage{
			When:         formatLocalDateTime(time.UnixMilli(record.CreatedAt), offsetMinutes),
			Channel:      firstNonEmpty(record.TeamName, "-") + " / " + firstNonEmpty(record.ChannelDisplayName, record.ChannelName),
			BotName:      strings.TrimSpace(record.BotTargetName),
			Categories:   append([]string{}, record.MajorCategories...),
			Keywords:     promptKeywords(record.KeywordMatches, 4),
			UrgencyScore: record.UrgencyScore,
			Preview:      trimForStorage(record.MessagePreview, 200),
		})
	}

	return rows
}

func promptKeywords(matches []keywordMatch, limit int) []string {
	values := map[string]int{}
	for _, match := range matches {
		values[match.Keyword] += match.Count
	}
	return topStrings(values, limit)
}

func countBotRequests(records []analyzedMessageRecord) int {
	total := 0
	for _, record := range records {
		if record.IsBotRequest {
			total++
		}
	}
	return total
}

func callVLLMChatCompletion(settings AISettings, payload aiPromptEnvelope) (string, error) {
	endpoint, err := buildVLLMChatCompletionsURL(settings.VLLMURL)
	if err != nil {
		return "", err
	}

	promptData, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to encode vLLM prompt: %w", err)
	}

	requestBody, err := json.Marshal(vllmChatCompletionRequest{
		Model: settings.VLLMModel,
		Messages: []vllmChatMessage{
			{
				Role: "system",
				Content: strings.TrimSpace(`
당신은 Mattermost IT/RPA 대화 분석 결과를 운영 리포트로 정리하는 시니어 분석가입니다.
항상 한국어로 답변하고, 입력 데이터에 없는 사실을 추측하지 마세요.
반드시 JSON 객체만 출력하고, 아래 키만 사용하세요.
{
  "summary": "기간 내 핵심 흐름을 3~5문장으로 요약",
  "highlights": ["핵심 변화 또는 중요한 사실"],
  "risks": ["주의가 필요한 리스크 또는 이상 징후"],
  "recommended_actions": ["관리자나 운영팀이 바로 실행할 조치"],
  "watchouts": ["추가 관찰 포인트 또는 후속 확인 사항"]
}
각 배열은 0~5개 항목으로 제한하고, 비어 있으면 빈 배열을 사용하세요.
summary에는 기간, 주요 키워드/카테고리, 핫토픽, 봇 문의 경향이 있으면 함께 반영하세요.
`),
			},
			{
				Role:    "user",
				Content: "다음 분석 데이터를 바탕으로 운영 요약을 작성하세요.\n" + string(promptData),
			},
		},
		Temperature: 0.2,
		MaxTokens:   defaultAISummaryTokens,
		Stream:      false,
	})
	if err != nil {
		return "", fmt.Errorf("failed to encode vLLM request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultAISummaryTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return "", fmt.Errorf("failed to create vLLM request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(settings.VLLMKey); key != "" {
		if !strings.HasPrefix(strings.ToLower(key), "bearer ") {
			key = "Bearer " + key
		}
		req.Header.Set("Authorization", key)
	}

	httpClient := &http.Client{Timeout: defaultAISummaryTimeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("vLLM 호출에 실패했습니다: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", fmt.Errorf("failed to read vLLM response: %w", err)
	}

	if resp.StatusCode >= 400 {
		message := strings.TrimSpace(string(body))
		parsed := vllmChatCompletionResponse{}
		if json.Unmarshal(body, &parsed) == nil && parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
			message = parsed.Error.Message
		}
		return "", fmt.Errorf("vLLM returned %s: %s", resp.Status, trimForStorage(message, 300))
	}

	content, err := extractVLLMContent(body)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("vLLM 응답 본문이 비어 있습니다")
	}
	return content, nil
}

func buildVLLMChatCompletionsURL(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("invalid vLLM API URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid vLLM API URL")
	}

	path := strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(path, "/chat/completions") {
		if path == "" {
			path = "/chat/completions"
		} else {
			path += "/chat/completions"
		}
	}
	parsed.Path = path
	return parsed.String(), nil
}

func extractVLLMContent(body []byte) (string, error) {
	response := vllmChatCompletionResponse{}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("failed to decode vLLM response: %w", err)
	}
	if response.Error != nil && strings.TrimSpace(response.Error.Message) != "" {
		return "", fmt.Errorf("vLLM error: %s", response.Error.Message)
	}
	if len(response.Choices) == 0 {
		return "", fmt.Errorf("vLLM 응답에 choices가 없습니다")
	}
	return normalizeVLLMContent(response.Choices[0].Message.Content), nil
}

func normalizeVLLMContent(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}

	type contentPart struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	var parts []contentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		lines := make([]string, 0, len(parts))
		for _, part := range parts {
			if strings.TrimSpace(part.Text) != "" {
				lines = append(lines, strings.TrimSpace(part.Text))
			}
		}
		return strings.Join(lines, "\n")
	}

	return strings.TrimSpace(string(raw))
}

func parseAISummaryContent(content string) aiGeneratedSummary {
	content = stripCodeFence(content)
	content = strings.TrimSpace(content)
	if content == "" {
		return aiGeneratedSummary{}
	}

	if object := extractJSONObject(content); object != "" {
		result := aiGeneratedSummary{}
		if err := json.Unmarshal([]byte(object), &result); err == nil {
			return normalizeAISummaryResult(result)
		}
	}

	lines := strings.Split(content, "\n")
	summaryLines := make([]string, 0, len(lines))
	bullets := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			bullets = append(bullets, strings.TrimSpace(line[2:]))
			continue
		}
		summaryLines = append(summaryLines, line)
	}

	result := aiGeneratedSummary{
		Summary:    strings.Join(summaryLines, " "),
		Highlights: bullets,
	}
	if result.Summary == "" {
		result.Summary = trimForStorage(content, 1200)
	}
	return normalizeAISummaryResult(result)
}

func normalizeAISummaryResult(result aiGeneratedSummary) aiGeneratedSummary {
	result.Summary = strings.TrimSpace(result.Summary)
	result.Highlights = normalizeAISummaryItems(result.Highlights)
	result.Risks = normalizeAISummaryItems(result.Risks)
	result.RecommendedActions = normalizeAISummaryItems(result.RecommendedActions)
	result.Watchouts = normalizeAISummaryItems(result.Watchouts)
	return result
}

func normalizeAISummaryItems(items []string) []string {
	normalized := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		item = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(item, "-"), "*"))
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, trimForStorage(item, 240))
		if len(normalized) == 5 {
			break
		}
	}
	return normalized
}

func stripCodeFence(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "```") {
		return value
	}

	lines := strings.Split(value, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			continue
		}
		filtered = append(filtered, line)
	}
	return strings.TrimSpace(strings.Join(filtered, "\n"))
}

func extractJSONObject(value string) string {
	start := strings.Index(value, "{")
	end := strings.LastIndex(value, "}")
	if start == -1 || end == -1 || end <= start {
		return ""
	}
	return value[start : end+1]
}

func formatLocalDateTime(t time.Time, offsetMinutes int) string {
	local := t.UTC().Add(time.Duration(offsetMinutes) * time.Minute)
	return local.Format("2006-01-02 15:04")
}
