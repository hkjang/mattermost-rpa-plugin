package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

func (p *Plugin) runReindex(userID string, request reindexRequest) (reindexResponse, error) {
	runtimeCfg, err := p.getRuntimeConfiguration()
	if err != nil {
		return reindexResponse{}, err
	}

	rangeValue, fromUTC, toUTC, err := buildStatsRange(request.FromDate, request.ToDate, request.TimezoneOffsetMinutes)
	if err != nil {
		return reindexResponse{}, err
	}

	channels, err := p.resolveReindexChannels(userID, runtimeCfg, request.ChannelIDs)
	if err != nil {
		return reindexResponse{}, err
	}
	if len(channels) == 0 {
		return reindexResponse{}, fmt.Errorf("no channels matched the configured analysis scope")
	}

	p.analyticsLock.Lock()
	_ = p.saveReindexStateLocked(reindexState{
		Status:        "running",
		LastRunAt:     time.Now().UnixMilli(),
		LastRangeFrom: rangeValue.FromDate,
		LastRangeTo:   rangeValue.ToDate,
	})
	p.analyticsLock.Unlock()

	userCache := map[string]*model.User{}
	channelCache := map[string]*model.Channel{}
	teamCache := map[string]*model.Team{}
	processedPosts := 0
	indexedMessages := 0

	for _, channelID := range channels {
		channel, appErr := p.API.GetChannel(channelID)
		if appErr != nil || channel == nil {
			continue
		}
		channelCache[channelID] = channel

		postList, appErr := p.API.GetPostsSince(channelID, fromUTC.UnixMilli()-1)
		if appErr != nil || postList == nil {
			continue
		}

		for _, post := range postList.ToSlice() {
			if post == nil {
				continue
			}
			createdAt := time.UnixMilli(post.CreateAt)
			if createdAt.Before(fromUTC) || createdAt.After(toUTC) {
				continue
			}
			processedPosts++

			user := userCache[post.UserId]
			if user == nil {
				loadedUser, userErr := p.API.GetUser(post.UserId)
				if userErr != nil || loadedUser == nil {
					continue
				}
				user = loadedUser
				userCache[post.UserId] = user
			}

			var team *model.Team
			if channel.TeamId != "" {
				team = teamCache[channel.TeamId]
				if team == nil {
					if loadedTeam, teamErr := p.API.GetTeam(channel.TeamId); teamErr == nil {
						team = loadedTeam
						teamCache[channel.TeamId] = team
					}
				}
			}

			if !isEligibleForAnalysis(post, channel, user, runtimeCfg) {
				p.analyticsLock.Lock()
				_ = p.removeMessageRecordLocked(post.Id, createdAt)
				p.analyticsLock.Unlock()
				continue
			}

			isBotRequest, botTargetName := p.detectBotRequest(post, channel, user)
			record, ok := analyzePostRecord(post, channel, user, team, runtimeCfg, isBotRequest, botTargetName)
			p.analyticsLock.Lock()
			if ok {
				_ = p.upsertMessageRecordLocked(record)
				indexedMessages++
			} else {
				_ = p.removeMessageRecordLocked(post.Id, createdAt)
			}
			p.analyticsLock.Unlock()
		}
	}

	p.analyticsLock.Lock()
	_ = p.cleanupExpiredAnalysisLocked(time.Now(), runtimeCfg)
	state := reindexState{
		Status:        "completed",
		LastRunAt:     time.Now().UnixMilli(),
		LastRangeFrom: rangeValue.FromDate,
		LastRangeTo:   rangeValue.ToDate,
		LastChannels:  len(channels),
		LastPosts:     processedPosts,
		LastIndexed:   indexedMessages,
	}
	_ = p.saveReindexStateLocked(state)
	p.analyticsLock.Unlock()

	return reindexResponse{
		Status:            "completed",
		ProcessedChannels: len(channels),
		ProcessedPosts:    processedPosts,
		IndexedMessages:   indexedMessages,
		FromDate:          rangeValue.FromDate,
		ToDate:            rangeValue.ToDate,
		CompletedAt:       time.Now().UnixMilli(),
	}, nil
}

func (p *Plugin) resolveReindexChannels(userID string, runtimeCfg *runtimeConfiguration, requestedChannelIDs []string) ([]string, error) {
	if runtimeCfg == nil {
		return nil, nil
	}

	requestedChannelIDs = normalizeIdentifierSlice(requestedChannelIDs)
	if len(requestedChannelIDs) > 0 {
		return requestedChannelIDs, nil
	}
	if len(runtimeCfg.Scope.IncludedChannelIDs) > 0 {
		return runtimeCfg.Scope.IncludedChannelIDs, nil
	}

	teamIDs := runtimeCfg.Scope.IncludedTeamIDs
	if len(teamIDs) == 0 {
		teams, appErr := p.API.GetTeams()
		if appErr != nil {
			return nil, appErr
		}
		for _, team := range teams {
			if team != nil {
				teamIDs = append(teamIDs, team.Id)
			}
		}
	}

	channelIDs := map[string]struct{}{}
	for _, teamID := range teamIDs {
		channels, appErr := p.API.GetChannelsForTeamForUser(teamID, userID, false)
		if appErr != nil {
			continue
		}
		for _, channel := range channels {
			if channel == nil {
				continue
			}
			if containsIdentifier(runtimeCfg.Scope.ExcludedChannelIDs, channel.Id) {
				continue
			}
			if !isAllowedChannelType(channel.Type, runtimeCfg.Scope) {
				continue
			}
			channelIDs[channel.Id] = struct{}{}
		}
	}

	result := make([]string, 0, len(channelIDs))
	for channelID := range channelIDs {
		result = append(result, channelID)
	}
	sort.Strings(result)
	return result, nil
}

func isAllowedChannelType(channelType model.ChannelType, scope AnalysisScope) bool {
	switch channelType {
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
