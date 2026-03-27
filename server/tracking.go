package main

import (
	"sort"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

type teamOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

type channelOption struct {
	ID          string `json:"id"`
	TeamID      string `json:"team_id,omitempty"`
	TeamName    string `json:"team_name,omitempty"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
}

type adminLookupCatalog struct {
	Teams    []teamOption    `json:"teams"`
	Channels []channelOption `json:"channels"`
}

func (p *Plugin) buildAdminLookupCatalog(userID string) adminLookupCatalog {
	catalog := adminLookupCatalog{
		Teams:    []teamOption{},
		Channels: []channelOption{},
	}

	teams, appErr := p.API.GetTeams()
	if appErr != nil {
		return catalog
	}

	channelSeen := map[string]struct{}{}
	for _, team := range teams {
		if team == nil {
			continue
		}

		catalog.Teams = append(catalog.Teams, teamOption{
			ID:          team.Id,
			Name:        team.Name,
			DisplayName: firstNonEmpty(team.DisplayName, team.Name),
		})

		channels, channelErr := p.API.GetChannelsForTeamForUser(team.Id, userID, false)
		if channelErr != nil {
			continue
		}

		for _, channel := range channels {
			if channel == nil {
				continue
			}
			if channel.Type != model.ChannelTypeOpen && channel.Type != model.ChannelTypePrivate {
				continue
			}
			if _, ok := channelSeen[channel.Id]; ok {
				continue
			}
			channelSeen[channel.Id] = struct{}{}
			catalog.Channels = append(catalog.Channels, channelOption{
				ID:          channel.Id,
				TeamID:      team.Id,
				TeamName:    firstNonEmpty(team.DisplayName, team.Name),
				Name:        channel.Name,
				DisplayName: firstNonEmpty(channel.DisplayName, channel.Name),
				Type:        string(channel.Type),
			})
		}
	}

	sort.Slice(catalog.Teams, func(i, j int) bool {
		return catalog.Teams[i].DisplayName < catalog.Teams[j].DisplayName
	})
	sort.Slice(catalog.Channels, func(i, j int) bool {
		if catalog.Channels[i].TeamName == catalog.Channels[j].TeamName {
			return catalog.Channels[i].DisplayName < catalog.Channels[j].DisplayName
		}
		return catalog.Channels[i].TeamName < catalog.Channels[j].TeamName
	})

	return catalog
}

func teamNameByID(catalog adminLookupCatalog) map[string]string {
	result := make(map[string]string, len(catalog.Teams))
	for _, team := range catalog.Teams {
		result[team.ID] = firstNonEmpty(team.DisplayName, team.Name)
	}
	return result
}

func channelLabel(channel channelOption) string {
	return firstNonEmpty(channel.TeamName, channel.TeamID) + " / " + firstNonEmpty(channel.DisplayName, channel.Name)
}

func formatChannelType(channelType string) string {
	switch model.ChannelType(strings.TrimSpace(channelType)) {
	case model.ChannelTypeOpen:
		return "공개 채널"
	case model.ChannelTypePrivate:
		return "비공개 채널"
	case model.ChannelTypeDirect:
		return "DM"
	case model.ChannelTypeGroup:
		return "그룹 DM"
	default:
		return channelType
	}
}
