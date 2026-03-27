package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

type pluginStatusResponse struct {
	PluginID          string       `json:"plugin_id"`
	ConfigError       string       `json:"config_error,omitempty"`
	Source            string       `json:"source"`
	IncludedTeams     int          `json:"included_teams"`
	IncludedChannels  int          `json:"included_channels"`
	ExcludedChannels  int          `json:"excluded_channels"`
	DictionaryEntries int          `json:"dictionary_entries"`
	AlertRules        int          `json:"alert_rules"`
	AnalyzedDays      int          `json:"analyzed_days"`
	LastReindex       reindexState `json:"last_reindex"`
	UpdatedAt         int64        `json:"updated_at"`
}

type adminConfigResponse struct {
	Config  storedPluginConfig   `json:"config"`
	Source  string               `json:"source"`
	Status  pluginStatusResponse `json:"status"`
	Catalog adminLookupCatalog   `json:"catalog"`
}

type reindexRequest struct {
	FromDate              string   `json:"from_date"`
	ToDate                string   `json:"to_date"`
	TimezoneOffsetMinutes int      `json:"timezone_offset_minutes"`
	ChannelIDs            []string `json:"channel_ids"`
}

type reindexResponse struct {
	Status            string `json:"status"`
	ProcessedChannels int    `json:"processed_channels"`
	ProcessedPosts    int    `json:"processed_posts"`
	IndexedMessages   int    `json:"indexed_messages"`
	FromDate          string `json:"from_date"`
	ToDate            string `json:"to_date"`
	CompletedAt       int64  `json:"completed_at"`
}

func (p *Plugin) initRouter() *mux.Router {
	router := mux.NewRouter()
	apiRouter := router.PathPrefix("/api/v1").Subrouter()
	apiRouter.HandleFunc("/config", p.handleAdminConfig).Methods(http.MethodGet)
	apiRouter.HandleFunc("/status", p.handleStatus).Methods(http.MethodGet)
	apiRouter.HandleFunc("/stats", p.handleStats).Methods(http.MethodGet)
	apiRouter.HandleFunc("/ai/summary", p.handleAISummary).Methods(http.MethodPost)
	apiRouter.HandleFunc("/reindex", p.handleReindex).Methods(http.MethodPost)
	apiRouter.HandleFunc("/report", p.handleReport).Methods(http.MethodGet)
	return router
}

func (p *Plugin) handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	if err := p.requireSystemAdmin(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}

	stored, source, err := p.getConfiguration().getStoredPluginConfig()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	userID := p.requestUserID(r)
	writeJSON(w, http.StatusOK, adminConfigResponse{
		Config:  stored.adminEditableConfig(),
		Source:  source,
		Status:  p.currentStatus(source),
		Catalog: p.buildAdminLookupCatalog(userID),
	})
}

func (p *Plugin) handleStatus(w http.ResponseWriter, r *http.Request) {
	if err := p.requireSystemAdmin(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}

	_, source, _ := p.getConfiguration().getStoredPluginConfig()
	writeJSON(w, http.StatusOK, p.currentStatus(source))
}

func (p *Plugin) handleStats(w http.ResponseWriter, r *http.Request) {
	if err := p.requireSystemAdmin(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}

	fromDate := strings.TrimSpace(r.URL.Query().Get("from_date"))
	toDate := strings.TrimSpace(r.URL.Query().Get("to_date"))
	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	majorCategory := strings.TrimSpace(r.URL.Query().Get("major_category"))
	channelID := strings.TrimSpace(r.URL.Query().Get("channel_id"))
	offsetMinutes, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("timezone_offset_minutes")))

	if fromDate == "" || toDate == "" {
		today := formatLocalDate(time.Now().UTC(), offsetMinutes)
		if fromDate == "" {
			fromDate = today
		}
		if toDate == "" {
			toDate = today
		}
	}

	response, err := p.buildStats(fromDate, toDate, keyword, majorCategory, channelID, offsetMinutes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, response)
}

func (p *Plugin) handleReindex(w http.ResponseWriter, r *http.Request) {
	if err := p.requireSystemAdmin(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}

	var request reindexRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	userID := p.requestUserID(r)
	response, err := p.runReindex(userID, request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, response)
}

func (p *Plugin) handleAISummary(w http.ResponseWriter, r *http.Request) {
	if err := p.requireSystemAdmin(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}

	var request aiSummaryRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if strings.TrimSpace(request.FromDate) == "" || strings.TrimSpace(request.ToDate) == "" {
		today := formatLocalDate(time.Now().UTC(), request.TimezoneOffsetMinutes)
		if strings.TrimSpace(request.FromDate) == "" {
			request.FromDate = today
		}
		if strings.TrimSpace(request.ToDate) == "" {
			request.ToDate = today
		}
	}

	response, err := p.generateAISummary(request)
	if err != nil {
		statusCode := http.StatusBadRequest
		lowered := strings.ToLower(err.Error())
		if strings.Contains(lowered, "vllm returned") || strings.Contains(lowered, "vllm 호출") || strings.Contains(lowered, "vllm error") {
			statusCode = http.StatusBadGateway
		}
		writeError(w, statusCode, err)
		return
	}

	writeJSON(w, http.StatusOK, response)
}

func (p *Plugin) handleReport(w http.ResponseWriter, r *http.Request) {
	if err := p.requireSystemAdmin(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}

	offsetMinutes, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("timezone_offset_minutes")))
	fromDate := strings.TrimSpace(r.URL.Query().Get("from_date"))
	toDate := strings.TrimSpace(r.URL.Query().Get("to_date"))
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	majorCategory := strings.TrimSpace(r.URL.Query().Get("major_category"))
	channelID := strings.TrimSpace(r.URL.Query().Get("channel_id"))

	if fromDate == "" || toDate == "" {
		today := formatLocalDate(time.Now().UTC(), offsetMinutes)
		if fromDate == "" {
			fromDate = today
		}
		if toDate == "" {
			toDate = today
		}
	}

	stats, err := p.buildStats(fromDate, toDate, keyword, majorCategory, channelID, offsetMinutes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	filename := fmt.Sprintf("rpa-dialog-analysis-%s-%s", fromDate, toDate)
	switch format {
	case "json", "":
		payload, marshalErr := buildReportJSON(stats)
		if marshalErr != nil {
			writeError(w, http.StatusInternalServerError, marshalErr)
			return
		}
		writeBytes(w, http.StatusOK, "application/json", filename+".json", payload)
	case "csv":
		payload, marshalErr := buildReportCSV(stats)
		if marshalErr != nil {
			writeError(w, http.StatusInternalServerError, marshalErr)
			return
		}
		writeBytes(w, http.StatusOK, "text/csv", filename+".csv", payload)
	default:
		writeError(w, http.StatusBadRequest, errors.New("supported formats are json and csv"))
	}
}

func (p *Plugin) currentStatus(source string) pluginStatusResponse {
	cfg := defaultStoredPluginConfig()
	configError := ""
	if stored, _, err := p.getConfiguration().getStoredPluginConfig(); err == nil {
		cfg = stored
	} else {
		configError = err.Error()
	}

	p.analyticsLock.Lock()
	dayIndex, _ := p.loadDayIndexLocked()
	reindexStateValue, _ := p.loadReindexStateLocked()
	p.analyticsLock.Unlock()

	return pluginStatusResponse{
		PluginID:          manifest.Id,
		ConfigError:       configError,
		Source:            source,
		IncludedTeams:     len(cfg.Scope.IncludedTeamIDs),
		IncludedChannels:  len(cfg.Scope.IncludedChannelIDs),
		ExcludedChannels:  len(cfg.Scope.ExcludedChannelIDs),
		DictionaryEntries: len(cfg.Dictionaries),
		AlertRules:        len(cfg.AlertRules),
		AnalyzedDays:      len(dayIndex),
		LastReindex:       reindexStateValue,
		UpdatedAt:         time.Now().UnixMilli(),
	}
}

func (p *Plugin) requireSystemAdmin(r *http.Request) error {
	userID := p.requestUserID(r)
	if userID == "" {
		return errors.New("not authorized")
	}
	if !p.API.HasPermissionTo(userID, model.PermissionManageSystem) {
		return errors.New("only system administrators can access this endpoint")
	}
	return nil
}

func (p *Plugin) requestUserID(r *http.Request) string {
	if r == nil {
		return ""
	}

	userID := strings.TrimSpace(r.Header.Get("Mattermost-User-Id"))
	if userID == "" {
		userID = strings.TrimSpace(r.Header.Get("Mattermost-User-ID"))
	}
	if userID != "" {
		return userID
	}

	pluginCtx, ok := r.Context().Value(pluginContextKey).(*plugin.Context)
	if !ok || pluginCtx == nil || pluginCtx.SessionId == "" {
		return ""
	}

	session, appErr := p.API.GetSession(pluginCtx.SessionId)
	if appErr != nil || session == nil {
		return ""
	}

	return strings.TrimSpace(session.UserId)
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeBytes(w http.ResponseWriter, statusCode int, contentType, filename string, payload []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(statusCode)
	_, _ = w.Write(payload)
}

func writeError(w http.ResponseWriter, statusCode int, err error) {
	writeJSON(w, statusCode, map[string]string{"error": err.Error()})
}
