package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildVLLMChatCompletionsURL(t *testing.T) {
	urlValue, err := buildVLLMChatCompletionsURL("http://localhost:8000/v1")
	require.NoError(t, err)
	require.Equal(t, "http://localhost:8000/v1/chat/completions", urlValue)
}

func TestExtractVLLMContentSupportsStringContent(t *testing.T) {
	content, err := extractVLLMContent([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"ok\",\"highlights\":[\"a\"]}"}}]}`))
	require.NoError(t, err)
	require.Contains(t, content, `"summary":"ok"`)
}

func TestExtractVLLMContentSupportsArrayContent(t *testing.T) {
	content, err := extractVLLMContent([]byte(`{"choices":[{"message":{"content":[{"type":"text","text":"{\"summary\":\"ok\"}"}]}}]}`))
	require.NoError(t, err)
	require.Equal(t, `{"summary":"ok"}`, content)
}

func TestParseAISummaryContentExtractsStructuredResult(t *testing.T) {
	result := parseAISummaryContent("```json\n{\"summary\":\"요약\",\"highlights\":[\"핵심\"],\"risks\":[\"위험\"],\"recommended_actions\":[\"조치\"],\"watchouts\":[\"관찰\"]}\n```")
	require.Equal(t, "요약", result.Summary)
	require.Equal(t, []string{"핵심"}, result.Highlights)
	require.Equal(t, []string{"위험"}, result.Risks)
	require.Equal(t, []string{"조치"}, result.RecommendedActions)
	require.Equal(t, []string{"관찰"}, result.Watchouts)
}

func TestCallVLLMChatCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"운영 요약\",\"highlights\":[\"DNS 문의 증가\"],\"risks\":[],\"recommended_actions\":[\"DNS 가이드 정비\"],\"watchouts\":[]}"}}]}`))
	}))
	defer server.Close()

	content, err := callVLLMChatCompletion(AISettings{
		VLLMURL:   server.URL + "/v1",
		VLLMKey:   "test-key",
		VLLMModel: "Qwen/Test",
	}, aiPromptEnvelope{
		GeneratedAt: "2026-03-27 12:00",
		Range:       statsRange{FromDate: "2026-03-20", ToDate: "2026-03-27", TimezoneOffsetMinutes: 540},
		Summary:     analyticsSummary{AnalyzedMessages: 10},
	})
	require.NoError(t, err)
	require.Contains(t, content, "운영 요약")
}
