package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type statsRange struct {
	FromDate              string `json:"from_date"`
	ToDate                string `json:"to_date"`
	TimezoneOffsetMinutes int    `json:"timezone_offset_minutes"`
}

type analyticsSummary struct {
	AnalyzedMessages int `json:"analyzed_messages"`
	UniqueAuthors    int `json:"unique_authors"`
	UniqueChannels   int `json:"unique_channels"`
	UniqueKeywords   int `json:"unique_keywords"`
	UrgentMessages   int `json:"urgent_messages"`
}

type keywordStat struct {
	Keyword       string  `json:"keyword"`
	MajorCategory string  `json:"major_category"`
	SubCategory   string  `json:"sub_category"`
	Purpose       string  `json:"purpose"`
	Count         int     `json:"count"`
	PreviousCount int     `json:"previous_count"`
	Delta         int     `json:"delta"`
	ChangeRate    float64 `json:"change_rate"`
}

type subcategoryStat struct {
	SubCategory string `json:"sub_category"`
	Purpose     string `json:"purpose"`
	Count       int    `json:"count"`
}

type categoryStat struct {
	MajorCategory string            `json:"major_category"`
	Count         int               `json:"count"`
	Subcategories []subcategoryStat `json:"subcategories"`
}

type channelStat struct {
	ChannelID      string   `json:"channel_id"`
	ChannelName    string   `json:"channel_name"`
	TeamName       string   `json:"team_name"`
	ChannelType    string   `json:"channel_type"`
	MessageCount   int      `json:"message_count"`
	UrgentMessages int      `json:"urgent_messages"`
	TopCategory    string   `json:"top_category"`
	TopKeywords    []string `json:"top_keywords"`
}

type trendPoint struct {
	Date           string `json:"date"`
	Label          string `json:"label"`
	Messages       int    `json:"messages"`
	UrgentMessages int    `json:"urgent_messages"`
}

type messageDetail struct {
	MessageID         string         `json:"message_id"`
	CreatedAt         int64          `json:"created_at"`
	ChannelID         string         `json:"channel_id"`
	ChannelName       string         `json:"channel_name"`
	TeamName          string         `json:"team_name"`
	AuthorDisplayName string         `json:"author_display_name"`
	Preview           string         `json:"preview"`
	UrgencyScore      float64        `json:"urgency_score"`
	Sentiment         string         `json:"sentiment"`
	MajorCategories   []string       `json:"major_categories"`
	KeywordMatches    []keywordMatch `json:"keyword_matches"`
}

type alertStatus struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	Description   string `json:"description"`
	Count         int    `json:"count"`
	Threshold     int    `json:"threshold"`
	WindowHours   int    `json:"window_hours"`
	Keyword       string `json:"keyword"`
	MajorCategory string `json:"major_category"`
	SubCategory   string `json:"sub_category"`
}

type statsResponse struct {
	Range               statsRange       `json:"range"`
	Summary             analyticsSummary `json:"summary"`
	Trend               []trendPoint     `json:"trend"`
	TagCloud            []keywordStat    `json:"tag_cloud"`
	KeywordTable        []keywordStat    `json:"keyword_table"`
	Categories          []categoryStat   `json:"categories"`
	Channels            []channelStat    `json:"channels"`
	HotTopics           []keywordStat    `json:"hot_topics"`
	Alerts              []alertStatus    `json:"alerts"`
	Messages            []messageDetail  `json:"messages"`
	AvailableCategories []string         `json:"available_categories"`
	AvailableKeywords   []string         `json:"available_keywords"`
	AvailableChannels   []channelOption  `json:"available_channels"`
	SelectedKeyword     string           `json:"selected_keyword,omitempty"`
	SelectedCategory    string           `json:"selected_category,omitempty"`
	SelectedChannelID   string           `json:"selected_channel_id,omitempty"`
	Reindex             reindexState     `json:"reindex"`
	GeneratedAt         int64            `json:"generated_at"`
}

func buildStatsRange(fromDate, toDate string, offsetMinutes int) (statsRange, time.Time, time.Time, error) {
	from, err := time.Parse("2006-01-02", fromDate)
	if err != nil {
		return statsRange{}, time.Time{}, time.Time{}, fmt.Errorf("invalid from_date: %w", err)
	}
	to, err := time.Parse("2006-01-02", toDate)
	if err != nil {
		return statsRange{}, time.Time{}, time.Time{}, fmt.Errorf("invalid to_date: %w", err)
	}
	if to.Before(from) {
		return statsRange{}, time.Time{}, time.Time{}, fmt.Errorf("to_date must be on or after from_date")
	}

	offset := time.Duration(offsetMinutes) * time.Minute
	fromUTC := from.Add(-offset)
	toUTC := to.Add(24*time.Hour - time.Millisecond - offset)

	return statsRange{
		FromDate:              fromDate,
		ToDate:                toDate,
		TimezoneOffsetMinutes: offsetMinutes,
	}, fromUTC, toUTC, nil
}

func (p *Plugin) buildStats(fromDate, toDate, keywordFilter, majorCategory, channelID string, offsetMinutes int) (statsResponse, error) {
	rangeValue, fromUTC, toUTC, err := buildStatsRange(fromDate, toDate, offsetMinutes)
	if err != nil {
		return statsResponse{}, err
	}

	allRecords, err := p.loadMessageRecords(fromUTC, toUTC)
	if err != nil {
		return statsResponse{}, err
	}

	runtimeCfg, err := p.getRuntimeConfiguration()
	if err != nil {
		return statsResponse{}, err
	}

	p.analyticsLock.Lock()
	reindexStateValue, _ := p.loadReindexStateLocked()
	p.analyticsLock.Unlock()

	availableCategories, availableKeywords, availableChannels := buildAvailableFilters(allRecords)
	filteredRecords := filterRecords(allRecords, keywordFilter, majorCategory, channelID)
	keywordTable := buildKeywordStats(filteredRecords)

	return statsResponse{
		Range:               rangeValue,
		Summary:             buildSummary(filteredRecords, keywordTable),
		Trend:               buildTrendSeries(fromDate, toDate, offsetMinutes, filteredRecords),
		TagCloud:            limitKeywordStats(keywordTable, runtimeCfg.Operations.ReportKeywordLimit),
		KeywordTable:        keywordTable,
		Categories:          buildCategoryStats(filteredRecords),
		Channels:            buildChannelStats(filteredRecords),
		HotTopics:           buildHotTopics(allRecords, fromUTC, toUTC, runtimeCfg.Operations.HotTopicLimit),
		Alerts:              buildAlerts(allRecords, toUTC, runtimeCfg),
		Messages:            buildMessageDetails(filteredRecords, runtimeCfg),
		AvailableCategories: availableCategories,
		AvailableKeywords:   availableKeywords,
		AvailableChannels:   availableChannels,
		SelectedKeyword:     keywordFilter,
		SelectedCategory:    majorCategory,
		SelectedChannelID:   channelID,
		Reindex:             reindexStateValue,
		GeneratedAt:         time.Now().UnixMilli(),
	}, nil
}

func buildAvailableFilters(records []analyzedMessageRecord) ([]string, []string, []channelOption) {
	categories := map[string]struct{}{}
	keywords := map[string]struct{}{}
	channels := map[string]channelOption{}

	for _, record := range records {
		for _, category := range record.MajorCategories {
			categories[category] = struct{}{}
		}
		for _, match := range record.KeywordMatches {
			keywords[match.Keyword] = struct{}{}
		}
		channels[record.ChannelID] = channelOption{
			ID:          record.ChannelID,
			TeamID:      record.TeamID,
			TeamName:    record.TeamName,
			Name:        record.ChannelName,
			DisplayName: record.ChannelDisplayName,
			Type:        record.ChannelType,
		}
	}

	categoryList := mapKeys(categories)
	keywordList := mapKeys(keywords)
	channelList := make([]channelOption, 0, len(channels))
	for _, channel := range channels {
		channelList = append(channelList, channel)
	}
	sort.Slice(channelList, func(i, j int) bool {
		if channelList[i].TeamName == channelList[j].TeamName {
			return channelList[i].DisplayName < channelList[j].DisplayName
		}
		return channelList[i].TeamName < channelList[j].TeamName
	})

	return categoryList, keywordList, channelList
}

func filterRecords(records []analyzedMessageRecord, keywordFilter, majorCategory, channelID string) []analyzedMessageRecord {
	if keywordFilter == "" && majorCategory == "" && channelID == "" {
		return append([]analyzedMessageRecord{}, records...)
	}

	filtered := make([]analyzedMessageRecord, 0, len(records))
	for _, record := range records {
		if channelID != "" && record.ChannelID != channelID {
			continue
		}
		if majorCategory != "" && !containsString(record.MajorCategories, majorCategory) {
			continue
		}
		if keywordFilter != "" && !recordContainsKeyword(record, keywordFilter) {
			continue
		}
		filtered = append(filtered, record)
	}
	return filtered
}

func buildSummary(records []analyzedMessageRecord, keywordTable []keywordStat) analyticsSummary {
	authors := map[string]struct{}{}
	channels := map[string]struct{}{}
	urgent := 0

	for _, record := range records {
		authors[record.AuthorUserID] = struct{}{}
		channels[record.ChannelID] = struct{}{}
		if record.UrgencyScore >= 60 {
			urgent++
		}
	}

	return analyticsSummary{
		AnalyzedMessages: len(records),
		UniqueAuthors:    len(authors),
		UniqueChannels:   len(channels),
		UniqueKeywords:   len(keywordTable),
		UrgentMessages:   urgent,
	}
}

func buildTrendSeries(fromDate, toDate string, offsetMinutes int, records []analyzedMessageRecord) []trendPoint {
	values := map[string]*trendPoint{}

	for _, record := range records {
		dateKey := formatLocalDate(time.UnixMilli(record.CreatedAt), offsetMinutes)
		point := values[dateKey]
		if point == nil {
			point = &trendPoint{
				Date:  dateKey,
				Label: dateKey,
			}
			values[dateKey] = point
		}
		point.Messages++
		if record.UrgencyScore >= 60 {
			point.UrgentMessages++
		}
	}

	from, _ := time.Parse("2006-01-02", fromDate)
	to, _ := time.Parse("2006-01-02", toDate)
	points := []trendPoint{}
	for day := from; !day.After(to); day = day.Add(24 * time.Hour) {
		dateKey := day.Format("2006-01-02")
		if point, ok := values[dateKey]; ok {
			points = append(points, *point)
			continue
		}
		points = append(points, trendPoint{
			Date:  dateKey,
			Label: dateKey,
		})
	}

	return points
}

func buildKeywordStats(records []analyzedMessageRecord) []keywordStat {
	type aggregate struct {
		keywordStat
	}

	values := map[string]*aggregate{}
	for _, record := range records {
		for _, match := range record.KeywordMatches {
			key := match.MajorCategory + "|" + match.SubCategory + "|" + match.Keyword
			item := values[key]
			if item == nil {
				item = &aggregate{
					keywordStat: keywordStat{
						Keyword:       match.Keyword,
						MajorCategory: match.MajorCategory,
						SubCategory:   match.SubCategory,
						Purpose:       match.Purpose,
					},
				}
				values[key] = item
			}
			item.Count += match.Count
		}
	}

	rows := make([]keywordStat, 0, len(values))
	for _, item := range values {
		rows = append(rows, item.keywordStat)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count == rows[j].Count {
			return rows[i].Keyword < rows[j].Keyword
		}
		return rows[i].Count > rows[j].Count
	})

	return rows
}

func limitKeywordStats(rows []keywordStat, limit int) []keywordStat {
	if limit <= 0 || len(rows) <= limit {
		return rows
	}
	return append([]keywordStat{}, rows[:limit]...)
}

func buildCategoryStats(records []analyzedMessageRecord) []categoryStat {
	type subAggregate struct {
		name    string
		purpose string
		count   int
	}

	majorCounts := map[string]int{}
	subCounts := map[string]map[string]*subAggregate{}

	for _, record := range records {
		for _, match := range record.KeywordMatches {
			majorCounts[match.MajorCategory] += match.Count
			if subCounts[match.MajorCategory] == nil {
				subCounts[match.MajorCategory] = map[string]*subAggregate{}
			}
			item := subCounts[match.MajorCategory][match.SubCategory]
			if item == nil {
				item = &subAggregate{name: match.SubCategory, purpose: match.Purpose}
				subCounts[match.MajorCategory][match.SubCategory] = item
			}
			item.count += match.Count
		}
	}

	rows := make([]categoryStat, 0, len(majorCounts))
	for major, count := range majorCounts {
		subRows := make([]subcategoryStat, 0, len(subCounts[major]))
		for _, item := range subCounts[major] {
			subRows = append(subRows, subcategoryStat{
				SubCategory: item.name,
				Purpose:     item.purpose,
				Count:       item.count,
			})
		}
		sort.Slice(subRows, func(i, j int) bool {
			if subRows[i].Count == subRows[j].Count {
				return subRows[i].SubCategory < subRows[j].SubCategory
			}
			return subRows[i].Count > subRows[j].Count
		})
		rows = append(rows, categoryStat{
			MajorCategory: major,
			Count:         count,
			Subcategories: subRows,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count == rows[j].Count {
			return rows[i].MajorCategory < rows[j].MajorCategory
		}
		return rows[i].Count > rows[j].Count
	})

	return rows
}

func buildChannelStats(records []analyzedMessageRecord) []channelStat {
	type aggregate struct {
		channelStat
		categoryCounts map[string]int
		keywordCounts  map[string]int
	}

	values := map[string]*aggregate{}
	for _, record := range records {
		item := values[record.ChannelID]
		if item == nil {
			item = &aggregate{
				channelStat: channelStat{
					ChannelID:   record.ChannelID,
					ChannelName: firstNonEmpty(record.ChannelDisplayName, record.ChannelName),
					TeamName:    record.TeamName,
					ChannelType: formatChannelType(record.ChannelType),
				},
				categoryCounts: map[string]int{},
				keywordCounts:  map[string]int{},
			}
			values[record.ChannelID] = item
		}

		item.MessageCount++
		if record.UrgencyScore >= 60 {
			item.UrgentMessages++
		}
		for _, match := range record.KeywordMatches {
			item.categoryCounts[match.MajorCategory] += match.Count
			item.keywordCounts[match.Keyword] += match.Count
		}
	}

	rows := make([]channelStat, 0, len(values))
	for _, item := range values {
		item.TopCategory = topStringByCount(item.categoryCounts)
		item.TopKeywords = topStrings(item.keywordCounts, 3)
		rows = append(rows, item.channelStat)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].MessageCount == rows[j].MessageCount {
			return rows[i].ChannelName < rows[j].ChannelName
		}
		return rows[i].MessageCount > rows[j].MessageCount
	})

	return rows
}

func buildHotTopics(records []analyzedMessageRecord, fromUTC, toUTC time.Time, limit int) []keywordStat {
	if len(records) == 0 {
		return nil
	}

	window := toUTC.Sub(fromUTC) / 2
	if window < 24*time.Hour {
		window = 24 * time.Hour
	}
	currentStart := toUTC.Add(-window)
	previousStart := currentStart.Add(-window)

	currentCounts := keywordCountWindow(records, currentStart, toUTC)
	previousCounts := keywordCountWindow(records, previousStart, currentStart.Add(-time.Millisecond))

	rows := make([]keywordStat, 0, len(currentCounts))
	for key, current := range currentCounts {
		parts := strings.Split(key, "|")
		if len(parts) < 4 {
			continue
		}
		previous := previousCounts[key]
		delta := current - previous
		if current <= 0 || delta <= 0 {
			continue
		}
		changeRate := 100.0
		if previous > 0 {
			changeRate = (float64(delta) / float64(previous)) * 100
		}
		rows = append(rows, keywordStat{
			MajorCategory: parts[0],
			SubCategory:   parts[1],
			Keyword:       parts[2],
			Purpose:       parts[3],
			Count:         current,
			PreviousCount: previous,
			Delta:         delta,
			ChangeRate:    changeRate,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Delta == rows[j].Delta {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Delta > rows[j].Delta
	})

	if limit > 0 && len(rows) > limit {
		return rows[:limit]
	}
	return rows
}

func keywordCountWindow(records []analyzedMessageRecord, fromUTC, toUTC time.Time) map[string]int {
	values := map[string]int{}
	for _, record := range records {
		createdAt := time.UnixMilli(record.CreatedAt)
		if createdAt.Before(fromUTC) || createdAt.After(toUTC) {
			continue
		}
		for _, match := range record.KeywordMatches {
			key := match.MajorCategory + "|" + match.SubCategory + "|" + match.Keyword + "|" + match.Purpose
			values[key] += match.Count
		}
	}
	return values
}

func buildAlerts(records []analyzedMessageRecord, toUTC time.Time, runtimeCfg *runtimeConfiguration) []alertStatus {
	if runtimeCfg == nil {
		return nil
	}

	rows := make([]alertStatus, 0, len(runtimeCfg.AlertRules))
	for _, rule := range runtimeCfg.AlertRules {
		if !rule.Enabled {
			continue
		}

		windowStart := toUTC.Add(-time.Duration(rule.WindowHours) * time.Hour)
		count := 0
		for _, record := range records {
			createdAt := time.UnixMilli(record.CreatedAt)
			if createdAt.Before(windowStart) || createdAt.After(toUTC) {
				continue
			}
			if rule.MajorCategory != "" && !containsString(record.MajorCategories, rule.MajorCategory) {
				continue
			}
			if rule.SubCategory != "" && !containsString(record.SubCategories, rule.SubCategory) {
				continue
			}
			if rule.Keyword != "" && !recordContainsKeyword(record, rule.Keyword) {
				continue
			}
			count++
		}

		status := "watch"
		description := fmt.Sprintf("%d건 / 기준 %d건", count, rule.Threshold)
		if count >= rule.Threshold {
			status = "triggered"
			description = fmt.Sprintf("%s이(가) 최근 %d시간 동안 %d건 감지되었습니다.", rule.Name, rule.WindowHours, count)
		}

		rows = append(rows, alertStatus{
			ID:            rule.ID,
			Name:          rule.Name,
			Status:        status,
			Description:   description,
			Count:         count,
			Threshold:     rule.Threshold,
			WindowHours:   rule.WindowHours,
			Keyword:       rule.Keyword,
			MajorCategory: rule.MajorCategory,
			SubCategory:   rule.SubCategory,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Status == rows[j].Status {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Status == "triggered"
	})

	return rows
}

func buildMessageDetails(records []analyzedMessageRecord, runtimeCfg *runtimeConfiguration) []messageDetail {
	rows := make([]messageDetail, 0, len(records))
	for _, record := range records {
		if !record.IsBotRequest {
			continue
		}

		author := record.AuthorDisplayName
		if runtimeCfg != nil && runtimeCfg.Operations.AnonymizeAuthors {
			author = anonymizeAuthor(record.AuthorDisplayName, record.AuthorUserID)
		}
		rows = append(rows, messageDetail{
			MessageID:         record.MessageID,
			CreatedAt:         record.CreatedAt,
			ChannelID:         record.ChannelID,
			ChannelName:       firstNonEmpty(record.ChannelDisplayName, record.ChannelName),
			TeamName:          record.TeamName,
			AuthorDisplayName: author,
			Preview:           record.MessagePreview,
			UrgencyScore:      record.UrgencyScore,
			Sentiment:         record.Sentiment,
			MajorCategories:   record.MajorCategories,
			KeywordMatches:    record.KeywordMatches,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt == rows[j].CreatedAt {
			return rows[i].UrgencyScore > rows[j].UrgencyScore
		}
		return rows[i].CreatedAt > rows[j].CreatedAt
	})

	if len(rows) > 100 {
		return rows[:100]
	}
	return rows
}

func buildReportJSON(stats statsResponse) ([]byte, error) {
	return json.MarshalIndent(stats, "", "  ")
}

func buildReportCSV(stats statsResponse) ([]byte, error) {
	buffer := &bytes.Buffer{}
	writer := csv.NewWriter(buffer)

	writeRow := func(values ...string) error {
		return writer.Write(values)
	}

	if err := writeRow("section", "name", "value", "extra"); err != nil {
		return nil, err
	}
	if err := writeRow("summary", "analyzed_messages", fmt.Sprintf("%d", stats.Summary.AnalyzedMessages), ""); err != nil {
		return nil, err
	}
	if err := writeRow("summary", "unique_authors", fmt.Sprintf("%d", stats.Summary.UniqueAuthors), ""); err != nil {
		return nil, err
	}
	if err := writeRow("summary", "unique_channels", fmt.Sprintf("%d", stats.Summary.UniqueChannels), ""); err != nil {
		return nil, err
	}
	if err := writeRow("summary", "urgent_messages", fmt.Sprintf("%d", stats.Summary.UrgentMessages), ""); err != nil {
		return nil, err
	}

	for _, keyword := range stats.KeywordTable {
		if err := writeRow("keyword", keyword.Keyword, fmt.Sprintf("%d", keyword.Count), keyword.MajorCategory+"/"+keyword.SubCategory); err != nil {
			return nil, err
		}
	}
	for _, category := range stats.Categories {
		if err := writeRow("category", category.MajorCategory, fmt.Sprintf("%d", category.Count), ""); err != nil {
			return nil, err
		}
	}
	for _, channel := range stats.Channels {
		if err := writeRow("channel", channel.ChannelName, fmt.Sprintf("%d", channel.MessageCount), channel.TeamName); err != nil {
			return nil, err
		}
	}
	for _, alert := range stats.Alerts {
		if err := writeRow("alert", alert.Name, fmt.Sprintf("%d", alert.Count), alert.Status); err != nil {
			return nil, err
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func formatLocalDate(t time.Time, offsetMinutes int) string {
	local := t.UTC().Add(time.Duration(offsetMinutes) * time.Minute)
	return local.Format("2006-01-02")
}

func topStringByCount(values map[string]int) string {
	topKey := ""
	topCount := -1
	for key, count := range values {
		if count > topCount || (count == topCount && key < topKey) {
			topKey = key
			topCount = count
		}
	}
	return topKey
}

func topStrings(values map[string]int, limit int) []string {
	type item struct {
		key   string
		count int
	}

	rows := make([]item, 0, len(values))
	for key, count := range values {
		rows = append(rows, item{key: key, count: count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count == rows[j].count {
			return rows[i].key < rows[j].key
		}
		return rows[i].count > rows[j].count
	})

	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}

	result := make([]string, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.key)
	}
	return result
}

func mapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func recordContainsKeyword(record analyzedMessageRecord, keyword string) bool {
	for _, match := range record.KeywordMatches {
		if strings.EqualFold(match.Keyword, keyword) {
			return true
		}
	}
	return false
}

func anonymizeAuthor(displayName, userID string) string {
	base := firstNonEmpty(displayName, userID, "사용자")
	if len(base) <= 1 {
		return "익명"
	}
	return string([]rune(base)[0]) + "**"
}
