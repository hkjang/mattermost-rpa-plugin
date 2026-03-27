package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	analysisDayIndexKey   = "it_dialog_analysis_days"
	analysisMessagePrefix = "it_dialog_messages_"
	reindexStateKey       = "it_dialog_reindex_state"
)

type reindexState struct {
	Status        string `json:"status"`
	LastRunAt     int64  `json:"last_run_at"`
	LastRangeFrom string `json:"last_range_from,omitempty"`
	LastRangeTo   string `json:"last_range_to,omitempty"`
	LastChannels  int    `json:"last_channels"`
	LastPosts     int    `json:"last_posts"`
	LastIndexed   int    `json:"last_indexed"`
	LastError     string `json:"last_error,omitempty"`
}

func analysisMessagesKeyFor(t time.Time) string {
	return analysisMessagePrefix + t.UTC().Format("2006-01-02")
}

func (p *Plugin) kvGetJSON(key string, out any) error {
	data, appErr := p.API.KVGet(key)
	if appErr != nil {
		return fmt.Errorf("failed to get %s: %w", key, appErr)
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("failed to decode %s: %w", key, err)
	}
	return nil
}

func (p *Plugin) kvSetJSON(key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to encode %s: %w", key, err)
	}
	if appErr := p.API.KVSet(key, data); appErr != nil {
		return fmt.Errorf("failed to save %s: %w", key, appErr)
	}
	return nil
}

func (p *Plugin) loadDayIndexLocked() ([]string, error) {
	index := []string{}
	if err := p.kvGetJSON(analysisDayIndexKey, &index); err != nil {
		return nil, err
	}
	return normalizeIdentifierSlice(index), nil
}

func (p *Plugin) saveDayIndexLocked(index []string) error {
	sort.Strings(index)
	return p.kvSetJSON(analysisDayIndexKey, normalizeIdentifierSlice(index))
}

func (p *Plugin) ensureDayIndexedLocked(t time.Time) error {
	index, err := p.loadDayIndexLocked()
	if err != nil {
		return err
	}

	day := t.UTC().Format("2006-01-02")
	if containsIdentifier(index, day) {
		return nil
	}
	index = append(index, day)
	return p.saveDayIndexLocked(index)
}

func (p *Plugin) loadMessageBucketLocked(t time.Time) ([]analyzedMessageRecord, error) {
	records := []analyzedMessageRecord{}
	if err := p.kvGetJSON(analysisMessagesKeyFor(t), &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (p *Plugin) saveMessageBucketLocked(t time.Time, records []analyzedMessageRecord) error {
	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt == records[j].CreatedAt {
			return records[i].MessageID < records[j].MessageID
		}
		return records[i].CreatedAt < records[j].CreatedAt
	})

	if len(records) == 0 {
		if appErr := p.API.KVDelete(analysisMessagesKeyFor(t)); appErr != nil {
			return fmt.Errorf("failed to delete %s: %w", analysisMessagesKeyFor(t), appErr)
		}
		return nil
	}

	if err := p.ensureDayIndexedLocked(t); err != nil {
		return err
	}

	return p.kvSetJSON(analysisMessagesKeyFor(t), records)
}

func (p *Plugin) upsertMessageRecordLocked(record analyzedMessageRecord) error {
	bucketTime := time.UnixMilli(record.CreatedAt)
	records, err := p.loadMessageBucketLocked(bucketTime)
	if err != nil {
		return err
	}

	replaced := false
	for index, item := range records {
		if item.MessageID != record.MessageID {
			continue
		}
		records[index] = record
		replaced = true
		break
	}
	if !replaced {
		records = append(records, record)
	}

	return p.saveMessageBucketLocked(bucketTime, records)
}

func (p *Plugin) removeMessageRecordLocked(messageID string, createdAt time.Time) error {
	if strings.TrimSpace(messageID) == "" {
		return nil
	}

	records, err := p.loadMessageBucketLocked(createdAt)
	if err != nil {
		return err
	}

	next := make([]analyzedMessageRecord, 0, len(records))
	for _, item := range records {
		if item.MessageID == messageID {
			continue
		}
		next = append(next, item)
	}

	if err := p.saveMessageBucketLocked(createdAt, next); err != nil {
		return err
	}

	if len(next) > 0 {
		return nil
	}

	index, err := p.loadDayIndexLocked()
	if err != nil {
		return err
	}

	day := createdAt.UTC().Format("2006-01-02")
	filtered := make([]string, 0, len(index))
	for _, item := range index {
		if item != day {
			filtered = append(filtered, item)
		}
	}

	return p.saveDayIndexLocked(filtered)
}

func (p *Plugin) loadMessageRecords(fromUTC, toUTC time.Time) ([]analyzedMessageRecord, error) {
	records := []analyzedMessageRecord{}
	for day := truncateToUTCDay(fromUTC); !day.After(toUTC); day = day.Add(24 * time.Hour) {
		items := []analyzedMessageRecord{}
		if err := p.kvGetJSON(analysisMessagesKeyFor(day), &items); err != nil {
			return nil, err
		}
		for _, item := range items {
			createdAt := time.UnixMilli(item.CreatedAt)
			if createdAt.Before(fromUTC) || createdAt.After(toUTC) {
				continue
			}
			records = append(records, item)
		}
	}

	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt == records[j].CreatedAt {
			return records[i].MessageID < records[j].MessageID
		}
		return records[i].CreatedAt < records[j].CreatedAt
	})

	return records, nil
}

func (p *Plugin) cleanupExpiredAnalysisLocked(now time.Time, runtimeCfg *runtimeConfiguration) error {
	if runtimeCfg == nil {
		return nil
	}

	index, err := p.loadDayIndexLocked()
	if err != nil {
		return err
	}

	cutoff := truncateToUTCDay(now.UTC().AddDate(0, 0, -(runtimeCfg.Operations.RetentionDays - 1)))
	remaining := make([]string, 0, len(index))

	for _, day := range index {
		parsed, parseErr := time.Parse("2006-01-02", day)
		if parseErr != nil {
			continue
		}
		if parsed.Before(cutoff) {
			if appErr := p.API.KVDelete(analysisMessagesKeyFor(parsed)); appErr != nil {
				return fmt.Errorf("failed to delete %s: %w", analysisMessagesKeyFor(parsed), appErr)
			}
			continue
		}
		remaining = append(remaining, day)
	}

	return p.saveDayIndexLocked(remaining)
}

func (p *Plugin) loadReindexStateLocked() (reindexState, error) {
	state := reindexState{}
	if err := p.kvGetJSON(reindexStateKey, &state); err != nil {
		return reindexState{}, err
	}
	return state, nil
}

func (p *Plugin) saveReindexStateLocked(state reindexState) error {
	return p.kvSetJSON(reindexStateKey, state)
}

func truncateToUTCDay(t time.Time) time.Time {
	return time.Date(t.UTC().Year(), t.UTC().Month(), t.UTC().Day(), 0, 0, 0, 0, time.UTC)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
