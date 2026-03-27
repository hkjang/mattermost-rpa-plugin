package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/require"
)

func TestStoredConfigNormalize(t *testing.T) {
	cfg := storedPluginConfig{
		Operations: AnalysisOperations{
			RetentionDays:      0,
			HotTopicLimit:      0,
			ReportKeywordLimit: 0,
		},
		Dictionaries: []DictionaryEntry{
			{
				ID:            "dict-1",
				MajorCategory: "장애",
				SubCategory:   "시스템 장애",
				Purpose:       "장애 탐지",
				Keywords:      []string{"다운", "다운"},
				Enabled:       true,
			},
		},
		Stopwords: []string{"그리고", "그리고"},
	}

	normalized, err := cfg.normalize()
	require.NoError(t, err)
	require.Equal(t, defaultRetentionDays, normalized.Operations.RetentionDays)
	require.Equal(t, defaultHotTopicLimit, normalized.Operations.HotTopicLimit)
	require.Equal(t, defaultReportKeywordLimit, normalized.Operations.ReportKeywordLimit)
	require.Len(t, normalized.Dictionaries, 1)
	require.Equal(t, []string{"다운"}, normalized.Dictionaries[0].Keywords)
	require.Contains(t, normalized.Stopwords, "그리고")
}

func TestAnalyzePostRecord(t *testing.T) {
	runtimeCfg, err := defaultStoredPluginConfig().normalize()
	require.NoError(t, err)

	record, ok := analyzePostRecord(
		&model.Post{
			Id:        "post-1",
			Message:   "서버 다운 장애 확인 부탁드립니다",
			ChannelId: "channel-1",
			CreateAt:  time.Now().UnixMilli(),
		},
		mockChannel("channel-1", "ops", "운영", "team-1"),
		mockUser("user-1", "alice", false),
		nil,
		runtimeCfg,
		false,
		"",
	)
	require.True(t, ok)
	require.Equal(t, "post-1", record.MessageID)
	require.Contains(t, record.MajorCategories, "장애")
	require.NotEmpty(t, record.KeywordMatches)
	require.Greater(t, record.UrgencyScore, 0.0)
}

func TestAnalyzePostRecordSkipsGratitudePhrase(t *testing.T) {
	runtimeCfg, err := defaultStoredPluginConfig().normalize()
	require.NoError(t, err)

	record, ok := analyzePostRecord(
		&model.Post{
			Id:        "post-thanks",
			Message:   "확인 감사합니다",
			ChannelId: "channel-1",
			CreateAt:  time.Now().UnixMilli(),
		},
		mockChannel("channel-1", "ops", "운영", "team-1"),
		mockUser("user-1", "alice", false),
		nil,
		runtimeCfg,
		false,
		"",
	)
	require.False(t, ok)
	require.Empty(t, record.KeywordMatches)
}

func TestDetectBotRequestInDirectMessage(t *testing.T) {
	api := &plugintest.API{}
	api.On("GetUser", "bot-user").Return(&model.User{Id: "bot-user", Username: "helper-bot", IsBot: true}, (*model.AppError)(nil)).Once()

	p := &Plugin{}
	p.SetAPI(api)

	isBotRequest, botTargetName := p.detectBotRequest(
		&model.Post{Id: "post-1", ChannelId: "dm-1", UserId: "user-1", Message: "배포 상태 확인해줘"},
		&model.Channel{Id: "dm-1", Name: "bot-user__user-1", Type: model.ChannelTypeDirect},
		mockUser("user-1", "alice", false),
	)
	require.True(t, isBotRequest)
	require.Equal(t, "helper-bot", botTargetName)
	api.AssertExpectations(t)
}

func TestDetectBotRequestByMention(t *testing.T) {
	api := &plugintest.API{}
	api.On("GetUserByUsername", "mattermostbot").Return(&model.User{Id: "bot-user", Username: "mattermostbot", IsBot: true}, (*model.AppError)(nil)).Once()

	p := &Plugin{}
	p.SetAPI(api)

	isBotRequest, botTargetName := p.detectBotRequest(
		&model.Post{Id: "post-2", ChannelId: "channel-1", UserId: "user-1", Message: "@mattermostbot dns 설정 방법 알려줘"},
		mockChannel("channel-1", "ops", "운영", "team-1"),
		mockUser("user-1", "alice", false),
	)
	require.True(t, isBotRequest)
	require.Equal(t, "mattermostbot", botTargetName)
	api.AssertExpectations(t)
}

func TestExtractMentionedUsernamesSkipsReservedMentions(t *testing.T) {
	usernames := extractMentionedUsernames("안녕하세요 @mattermostbot @channel @all @helper.bot")
	require.Equal(t, []string{"mattermostbot", "helper.bot"}, usernames)
}

func TestIsEligibleForAnalysisAllowsBotDirectRequest(t *testing.T) {
	runtimeCfg, err := defaultStoredPluginConfig().normalize()
	require.NoError(t, err)
	require.False(t, runtimeCfg.Scope.IncludeDirectChan)

	eligible := isEligibleForAnalysis(
		&model.Post{
			Id:        "post-dm",
			Message:   "dns 설정 방법 알려줘",
			ChannelId: "dm-1",
			CreateAt:  time.Now().UnixMilli(),
		},
		&model.Channel{Id: "dm-1", Name: "bot-user__user-1", Type: model.ChannelTypeDirect},
		mockUser("user-1", "alice", false),
		runtimeCfg,
		true,
	)
	require.True(t, eligible)
}

func TestBuildStatsRange(t *testing.T) {
	rangeValue, fromUTC, toUTC, err := buildStatsRange("2026-03-20", "2026-03-21", 540)
	require.NoError(t, err)
	require.Equal(t, "2026-03-20", rangeValue.FromDate)
	require.Equal(t, "2026-03-21", rangeValue.ToDate)
	require.Equal(t, time.Date(2026, 3, 19, 15, 0, 0, 0, time.UTC), fromUTC)
	require.Equal(t, time.Date(2026, 3, 21, 14, 59, 59, int(time.Millisecond*999), time.UTC), toUTC)
}

func TestDefaultDictionaryEntriesContainRequestedKeywords(t *testing.T) {
	entries := defaultDictionaryEntries()
	keywordSet := map[string]struct{}{}

	for _, entry := range entries {
		for _, keyword := range entry.Keywords {
			keywordSet[strings.ToLower(keyword)] = struct{}{}
		}
	}

	required := []string{
		"다운", "hang", "timeout", "intermittent", "warning",
		"release", "rollback", "실패", "hotfix",
		"docker", "k8s", "dns", "firewall", "bandwidth",
		"deadlock", "connection", "sso", "cve",
		"pr", "unit", "pipeline", "alert",
		"ec2", "bigquery", "blob", "api",
		"계정 잠금", "접근 요청", "cpu 사용량", "urgent", "root cause",
		"etl", "audit", "ssl", "terraform", "automation",
		"llm", "prompt", "rag", "embedding", "vllm", "mlops", "gpu",
		"신용등급", "아웃룩", "등급위원회", "회사채", "abcp", "부동산pf", "감사보고서", "금감원",
		"회원사 문의", "보완자료", "평가 일정", "보고서 송부", "회원사 포털", "평가 수수료", "정정 요청",
	}

	for _, keyword := range required {
		_, ok := keywordSet[strings.ToLower(keyword)]
		require.Truef(t, ok, "expected predefined keyword %q to exist", keyword)
	}
}

func TestAdminEditableConfigStripsPredefinedKeywordPayload(t *testing.T) {
	cfg := defaultStoredPluginConfig().adminEditableConfig()
	require.Empty(t, cfg.Dictionaries)
	require.Empty(t, cfg.Stopwords)
	require.NotEmpty(t, cfg.AlertRules)
}

func TestRequestUserIDFallsBackToSession(t *testing.T) {
	api := &plugintest.API{}
	api.On("GetSession", "session-123").Return(&model.Session{UserId: "admin-user"}, (*model.AppError)(nil)).Once()

	p := &Plugin{}
	p.SetAPI(api)

	req := httptest.NewRequest("POST", "/api/v1/reindex", nil)
	req = req.WithContext(context.WithValue(req.Context(), pluginContextKey, &plugin.Context{SessionId: "session-123"}))

	require.Equal(t, "admin-user", p.requestUserID(req))
	api.AssertExpectations(t)
}

func TestRequestUserIDReadsMattermostHeader(t *testing.T) {
	p := &Plugin{}

	req := httptest.NewRequest("POST", "/api/v1/reindex", nil)
	req.Header.Set("Mattermost-User-Id", "admin-user")

	require.Equal(t, "admin-user", p.requestUserID(req))
}

func TestRequireSystemAdminUsesSessionFallback(t *testing.T) {
	api := &plugintest.API{}
	api.On("GetSession", "session-123").Return(&model.Session{UserId: "admin-user"}, (*model.AppError)(nil)).Once()
	api.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Once()

	p := &Plugin{}
	p.SetAPI(api)

	req := httptest.NewRequest("POST", "/api/v1/reindex", nil)
	req = req.WithContext(context.WithValue(req.Context(), pluginContextKey, &plugin.Context{SessionId: "session-123"}))

	require.NoError(t, p.requireSystemAdmin(req))
	api.AssertExpectations(t)
}

func mockChannel(id, name, displayName, teamID string) *model.Channel {
	return &model.Channel{Id: id, Name: name, DisplayName: displayName, TeamId: teamID, Type: model.ChannelTypeOpen}
}

func mockUser(id, username string, isBot bool) *model.User {
	return &model.User{Id: id, Username: username, IsBot: isBot}
}
