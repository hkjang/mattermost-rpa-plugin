package main

import (
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
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
	)
	require.True(t, ok)
	require.Equal(t, "post-1", record.MessageID)
	require.Contains(t, record.MajorCategories, "장애")
	require.NotEmpty(t, record.KeywordMatches)
	require.Greater(t, record.UrgencyScore, 0.0)
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
	}

	for _, keyword := range required {
		_, ok := keywordSet[strings.ToLower(keyword)]
		require.Truef(t, ok, "expected predefined keyword %q to exist", keyword)
	}
}

func mockChannel(id, name, displayName, teamID string) *model.Channel {
	return &model.Channel{Id: id, Name: name, DisplayName: displayName, TeamId: teamID, Type: model.ChannelTypeOpen}
}

func mockUser(id, username string, isBot bool) *model.User {
	return &model.User{Id: id, Username: username, IsBot: isBot}
}
