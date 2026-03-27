package main

import (
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

var tokenPattern = regexp.MustCompile(`[\p{Hangul}\p{L}\p{N}][\p{Hangul}\p{L}\p{N}._/-]*`)
var mentionPattern = regexp.MustCompile(`(?i)(?:^|[^a-z0-9._-])@([a-z0-9][a-z0-9._-]*)`)

type keywordMatch struct {
	Keyword       string `json:"keyword"`
	MajorCategory string `json:"major_category"`
	SubCategory   string `json:"sub_category"`
	Purpose       string `json:"purpose"`
	Count         int    `json:"count"`
}

type analyzedMessageRecord struct {
	MessageID          string         `json:"message_id"`
	RootID             string         `json:"root_id,omitempty"`
	TeamID             string         `json:"team_id,omitempty"`
	TeamName           string         `json:"team_name,omitempty"`
	ChannelID          string         `json:"channel_id"`
	ChannelName        string         `json:"channel_name"`
	ChannelDisplayName string         `json:"channel_display_name"`
	ChannelType        string         `json:"channel_type"`
	AuthorUserID       string         `json:"author_user_id"`
	AuthorUsername     string         `json:"author_username"`
	AuthorDisplayName  string         `json:"author_display_name"`
	CreatedAt          int64          `json:"created_at"`
	UpdatedAt          int64          `json:"updated_at"`
	IsThreadReply      bool           `json:"is_thread_reply"`
	IsBotMessage       bool           `json:"is_bot_message"`
	IsBotRequest       bool           `json:"is_bot_request"`
	BotTargetName      string         `json:"bot_target_name,omitempty"`
	Message            string         `json:"message"`
	MessagePreview     string         `json:"message_preview"`
	Tokens             []string       `json:"tokens"`
	KeywordMatches     []keywordMatch `json:"keyword_matches"`
	MajorCategories    []string       `json:"major_categories"`
	SubCategories      []string       `json:"sub_categories"`
	UrgencyScore       float64        `json:"urgency_score"`
	Sentiment          string         `json:"sentiment"`
	Signals            []string       `json:"signals"`
}

func (p *Plugin) MessageHasBeenPosted(_ *plugin.Context, post *model.Post) {
	go p.processPostUpsert(post)
}

func (p *Plugin) MessageHasBeenUpdated(_ *plugin.Context, newPost, _ *model.Post) {
	go p.processPostUpsert(newPost)
}

func (p *Plugin) MessageHasBeenDeleted(_ *plugin.Context, post *model.Post) {
	go p.processPostDeletion(post)
}

func (p *Plugin) processPostUpsert(post *model.Post) {
	if post == nil || strings.TrimSpace(post.Id) == "" {
		return
	}

	runtimeCfg, err := p.getRuntimeConfiguration()
	if err != nil {
		return
	}

	channel, user, team, eligible, err := p.prepareAnalysisContext(post, runtimeCfg)
	if err != nil {
		return
	}

	p.analyticsLock.Lock()
	defer p.analyticsLock.Unlock()

	if !eligible {
		_ = p.removeMessageRecordLocked(post.Id, time.UnixMilli(post.CreateAt))
		return
	}

	isBotRequest, botTargetName := p.detectBotRequest(post, channel, user)
	record, ok := analyzePostRecord(post, channel, user, team, runtimeCfg, isBotRequest, botTargetName)
	if !ok {
		_ = p.removeMessageRecordLocked(post.Id, time.UnixMilli(post.CreateAt))
		return
	}

	_ = p.upsertMessageRecordLocked(record)
	_ = p.cleanupExpiredAnalysisLocked(time.Now(), runtimeCfg)
}

func (p *Plugin) processPostDeletion(post *model.Post) {
	if post == nil || strings.TrimSpace(post.Id) == "" {
		return
	}

	p.analyticsLock.Lock()
	defer p.analyticsLock.Unlock()

	_ = p.removeMessageRecordLocked(post.Id, time.UnixMilli(post.CreateAt))
}

func (p *Plugin) prepareAnalysisContext(post *model.Post, runtimeCfg *runtimeConfiguration) (*model.Channel, *model.User, *model.Team, bool, error) {
	channel, appErr := p.API.GetChannel(post.ChannelId)
	if appErr != nil || channel == nil {
		return nil, nil, nil, false, appErr
	}

	user, appErr := p.API.GetUser(post.UserId)
	if appErr != nil || user == nil {
		return nil, nil, nil, false, appErr
	}

	var team *model.Team
	if strings.TrimSpace(channel.TeamId) != "" {
		if loadedTeam, teamErr := p.API.GetTeam(channel.TeamId); teamErr == nil {
			team = loadedTeam
		}
	}

	return channel, user, team, isEligibleForAnalysis(post, channel, user, runtimeCfg), nil
}

func isEligibleForAnalysis(post *model.Post, channel *model.Channel, user *model.User, runtimeCfg *runtimeConfiguration) bool {
	if post == nil || channel == nil || user == nil || runtimeCfg == nil {
		return false
	}

	scope := runtimeCfg.Scope
	if strings.TrimSpace(post.Message) == "" {
		return false
	}
	if scope.ExcludeSystemMessage && strings.HasPrefix(post.Type, "system_") {
		return false
	}
	if scope.ExcludeBotMessages && user.IsBot {
		return false
	}
	if !scope.IncludeThreads && strings.TrimSpace(post.RootId) != "" {
		return false
	}

	if len(scope.IncludedTeamIDs) > 0 && !containsIdentifier(scope.IncludedTeamIDs, channel.TeamId) {
		return false
	}
	if len(scope.IncludedChannelIDs) > 0 && !containsIdentifier(scope.IncludedChannelIDs, post.ChannelId) {
		return false
	}
	if containsIdentifier(scope.ExcludedChannelIDs, post.ChannelId) {
		return false
	}

	switch channel.Type {
	case model.ChannelTypeOpen:
		return scope.IncludePublicChannel
	case model.ChannelTypePrivate:
		return scope.IncludePrivateChan
	case model.ChannelTypeDirect:
		return scope.IncludeDirectChan
	case model.ChannelTypeGroup:
		return scope.IncludeGroupChan
	default:
		return false
	}
}

func analyzePostRecord(post *model.Post, channel *model.Channel, user *model.User, team *model.Team, runtimeCfg *runtimeConfiguration, isBotRequest bool, botTargetName string) (analyzedMessageRecord, bool) {
	message := cleanMessageText(post.Message)
	if message == "" {
		return analyzedMessageRecord{}, false
	}

	tokens := tokenizeMessage(message, runtimeCfg.Stopwords)
	matches := findKeywordMatches(message, runtimeCfg.CompiledLookup)
	if len(matches) == 0 {
		return analyzedMessageRecord{}, false
	}

	majorCategories := make([]string, 0, len(matches))
	subCategories := make([]string, 0, len(matches))
	majorSeen := map[string]struct{}{}
	subSeen := map[string]struct{}{}
	for _, match := range matches {
		if _, ok := majorSeen[match.MajorCategory]; !ok {
			majorSeen[match.MajorCategory] = struct{}{}
			majorCategories = append(majorCategories, match.MajorCategory)
		}
		key := match.MajorCategory + ":" + match.SubCategory
		if _, ok := subSeen[key]; !ok {
			subSeen[key] = struct{}{}
			subCategories = append(subCategories, match.SubCategory)
		}
	}

	urgencyScore, sentiment, signals := scoreSignals(message, matches)

	return analyzedMessageRecord{
		MessageID:          post.Id,
		RootID:             firstNonEmpty(strings.TrimSpace(post.RootId), post.Id),
		TeamID:             channel.TeamId,
		TeamName:           teamDisplayName(team),
		ChannelID:          channel.Id,
		ChannelName:        channel.Name,
		ChannelDisplayName: firstNonEmpty(channel.DisplayName, channel.Name),
		ChannelType:        string(channel.Type),
		AuthorUserID:       user.Id,
		AuthorUsername:     user.Username,
		AuthorDisplayName:  userDisplayName(user),
		CreatedAt:          post.CreateAt,
		UpdatedAt:          maxInt64(post.UpdateAt, post.CreateAt),
		IsThreadReply:      strings.TrimSpace(post.RootId) != "",
		IsBotMessage:       user.IsBot,
		IsBotRequest:       isBotRequest,
		BotTargetName:      strings.TrimSpace(botTargetName),
		Message:            trimForStorage(message, 2000),
		MessagePreview:     trimForStorage(message, 240),
		Tokens:             tokens,
		KeywordMatches:     matches,
		MajorCategories:    majorCategories,
		SubCategories:      subCategories,
		UrgencyScore:       urgencyScore,
		Sentiment:          sentiment,
		Signals:            signals,
	}, true
}

func (p *Plugin) detectBotRequest(post *model.Post, channel *model.Channel, author *model.User) (bool, string) {
	if p == nil || post == nil || channel == nil || author == nil || author.IsBot {
		return false, ""
	}

	if botTargetName, ok := p.detectBotRequestDirectTarget(channel, author.Id); ok {
		return true, botTargetName
	}
	if botTargetName, ok := p.detectBotRequestMentionTarget(post.Message); ok {
		return true, botTargetName
	}

	return false, ""
}

func (p *Plugin) detectBotRequestDirectTarget(channel *model.Channel, authorID string) (string, bool) {
	if p == nil || channel == nil || channel.Type != model.ChannelTypeDirect {
		return "", false
	}

	otherUserID := directChannelOtherUserID(channel.Name, authorID)
	if otherUserID == "" {
		return "", false
	}

	targetUser, appErr := p.API.GetUser(otherUserID)
	if appErr != nil || targetUser == nil || !targetUser.IsBot {
		return "", false
	}

	return firstNonEmpty(targetUser.Username, targetUser.Nickname, targetUser.Id), true
}

func (p *Plugin) detectBotRequestMentionTarget(message string) (string, bool) {
	if p == nil || !strings.Contains(message, "@") {
		return "", false
	}

	for _, username := range extractMentionedUsernames(message) {
		targetUser, appErr := p.API.GetUserByUsername(username)
		if appErr != nil || targetUser == nil || !targetUser.IsBot {
			continue
		}
		return firstNonEmpty(targetUser.Username, targetUser.Nickname, targetUser.Id), true
	}

	return "", false
}

func extractMentionedUsernames(message string) []string {
	matches := mentionPattern.FindAllStringSubmatch(strings.ToLower(message), -1)
	if len(matches) == 0 {
		return nil
	}

	result := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		username := strings.TrimSpace(match[1])
		if username == "" || username == "channel" || username == "all" || username == "here" {
			continue
		}
		if _, ok := seen[username]; ok {
			continue
		}
		seen[username] = struct{}{}
		result = append(result, username)
	}

	return result
}

func directChannelOtherUserID(channelName, authorID string) string {
	parts := strings.Split(strings.TrimSpace(channelName), "__")
	if len(parts) != 2 {
		return ""
	}
	if parts[0] == authorID {
		return parts[1]
	}
	if parts[1] == authorID {
		return parts[0]
	}
	return ""
}

func cleanMessageText(message string) string {
	message = strings.TrimSpace(message)
	message = strings.ReplaceAll(message, "\n", " ")
	message = strings.ReplaceAll(message, "\t", " ")
	message = strings.Join(strings.Fields(message), " ")
	return message
}

func tokenizeMessage(message string, stopwords map[string]struct{}) []string {
	items := tokenPattern.FindAllString(strings.ToLower(message), -1)
	result := make([]string, 0, len(items))
	seen := map[string]struct{}{}

	for _, item := range items {
		item = strings.TrimSpace(item)
		if len(item) <= 1 {
			continue
		}
		if _, ok := stopwords[item]; ok {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}

	sort.Strings(result)
	return result
}

func findKeywordMatches(message string, lookup []compiledKeyword) []keywordMatch {
	normalizedMessage := normalizeKeywordKey(message)
	if normalizedMessage == "" {
		return nil
	}

	result := make([]keywordMatch, 0, 8)
	seen := map[string]*keywordMatch{}

	for _, item := range lookup {
		count := countKeywordOccurrences(normalizedMessage, item.Normalized)
		if count == 0 {
			continue
		}

		key := item.MajorCategory + "|" + item.SubCategory + "|" + item.Keyword
		if existing, ok := seen[key]; ok {
			existing.Count += count
			continue
		}

		match := &keywordMatch{
			Keyword:       item.Keyword,
			MajorCategory: item.MajorCategory,
			SubCategory:   item.SubCategory,
			Purpose:       item.Purpose,
			Count:         count,
		}
		seen[key] = match
		result = append(result, *match)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Count == result[j].Count {
			return result[i].Keyword < result[j].Keyword
		}
		return result[i].Count > result[j].Count
	})

	return result
}

func countKeywordOccurrences(message, keyword string) int {
	if keyword == "" || message == "" {
		return 0
	}

	if requiresWordBoundary(keyword) {
		return countASCIIKeyword(message, keyword)
	}

	return strings.Count(message, keyword)
}

func requiresWordBoundary(keyword string) bool {
	for _, r := range keyword {
		if unicode.Is(unicode.Hangul, r) {
			return false
		}
	}
	return !strings.ContainsRune(keyword, ' ')
}

func countASCIIKeyword(message, keyword string) int {
	count := 0
	start := 0

	for {
		index := strings.Index(message[start:], keyword)
		if index < 0 {
			return count
		}

		index += start
		beforeOK := index == 0 || !isKeywordRune(rune(message[index-1]))
		afterIndex := index + len(keyword)
		afterOK := afterIndex >= len(message) || !isKeywordRune(rune(message[afterIndex]))
		if beforeOK && afterOK {
			count++
		}

		start = index + len(keyword)
		if start >= len(message) {
			return count
		}
	}
}

func normalizeKeywordKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Join(strings.Fields(value), " ")
	return value
}

func isKeywordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '/'
}

func scoreSignals(message string, matches []keywordMatch) (float64, string, []string) {
	lower := strings.ToLower(message)
	score := 0.0
	signals := []string{}

	addSignal := func(label string, delta float64) {
		for _, existing := range signals {
			if existing == label {
				score += delta
				return
			}
		}
		signals = append(signals, label)
		score += delta
	}

	for _, item := range matches {
		switch item.MajorCategory {
		case "장애":
			addSignal("장애", 20)
		case "보안", "보안운영":
			addSignal("보안", 18)
		case "배포":
			addSignal("배포", 12)
		}
	}

	for _, keyword := range []string{"긴급", "urgent", "asap", "즉시"} {
		if strings.Contains(lower, keyword) {
			addSignal("긴급", 35)
		}
	}
	for _, keyword := range []string{"에러", "error", "실패", "timeout", "다운", "응답없음"} {
		if strings.Contains(lower, keyword) {
			addSignal("부정", 15)
		}
	}
	if strings.Contains(lower, "warning") || strings.Contains(lower, "이상징후") {
		addSignal("징후", 10)
	}

	switch {
	case score >= 60:
		return minFloat(score, 100), "negative", signals
	case score >= 25:
		return minFloat(score, 100), "watch", signals
	default:
		return minFloat(score, 100), "neutral", signals
	}
}

func containsIdentifier(values []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func userDisplayName(user *model.User) string {
	if user == nil {
		return ""
	}
	if name := strings.TrimSpace(user.Nickname); name != "" {
		return name
	}
	fullName := strings.TrimSpace(strings.TrimSpace(user.FirstName) + " " + strings.TrimSpace(user.LastName))
	if fullName != "" {
		return fullName
	}
	return user.Username
}

func teamDisplayName(team *model.Team) string {
	if team == nil {
		return ""
	}
	return firstNonEmpty(team.DisplayName, team.Name)
}

func trimForStorage(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
