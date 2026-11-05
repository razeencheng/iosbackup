package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"time"
)

const backupJobPageSize = 5

type backupJobKey struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

func jobKey(job backupJobSummary) backupJobKey { return backupJobKey{job.StartedAt, job.ID} }
func (k backupJobKey) newer(other backupJobKey) bool {
	return k.At.After(other.At) || k.At.Equal(other.At) && k.ID > other.ID
}
func (k backupJobKey) valid() bool { return !k.At.IsZero() && backupJobIDPattern.MatchString(k.ID) }

type backupJobPageCursor struct {
	Version int           `json:"v"`
	UDID    string        `json:"udid"`
	Type    string        `json:"type"`
	Result  string        `json:"result"`
	Upper   backupJobKey  `json:"upper"`
	Before  *backupJobKey `json:"before,omitempty"`
}

type backupJobPage struct {
	Jobs       []backupJobSummary `json:"jobs"`
	Cursor     string             `json:"page_cursor"`
	NextCursor string             `json:"next_cursor"`
	HasMore    bool               `json:"has_more"`
	HasNew     bool               `json:"has_new"`
}

func parseBackupJobPage(udid string, query url.Values) (backupJobPageCursor, bool, error) {
	c := backupJobPageCursor{Version: 1, UDID: udid, Type: query.Get("type"), Result: query.Get("result")}
	if c.Type == "" {
		c.Type = "all"
	}
	if c.Result == "" {
		c.Result = "all"
	}
	invalid := errors.New("invalid task page")
	switch c.Type {
	case "all", "backup", "check":
	default:
		return c, false, invalid
	}
	switch c.Result {
	case "all", "attention", "succeeded", "failed", "interrupted", "stopped", "running":
	default:
		return c, false, invalid
	}
	raw := query.Get("cursor")
	if raw == "" {
		return c, false, nil
	}
	if len(raw) > 2048 {
		return c, false, invalid
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	var saved backupJobPageCursor
	if err != nil || json.Unmarshal(data, &saved) != nil || saved.Version != 1 || saved.UDID != udid || saved.Type != c.Type || saved.Result != c.Result {
		return c, false, invalid
	}
	// 空列表也保留上界，新增记录只显示提示，不自动插入当前页。
	emptyUpper := saved.Upper.At.IsZero() && saved.Upper.ID == ""
	if !saved.Upper.valid() && !emptyUpper || saved.Before != nil && (!saved.Before.valid() || emptyUpper || saved.Before.newer(saved.Upper)) {
		return c, false, invalid
	}
	return saved, true, nil
}

func (c backupJobPageCursor) encode() string {
	data, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(data)
}

func (c backupJobPageCursor) matches(job backupJobSummary) bool {
	operation := job.Operation
	if operation == "" {
		operation = "backup"
	}
	if job.UDID != c.UDID || c.Type != "all" && c.Type != operation {
		return false
	}
	switch c.Result {
	case "all":
		return true
	case "running":
		return job.State == "starting" || job.State == "running" || job.State == "stopping"
	case "attention":
		return job.State == "failed" || job.State == "interrupted" || job.HistoryState == "failed" || job.HistoryState == "uncertain" || job.HistoryState == "pending" || job.Check != nil && job.Check.Reason != "cancelled" && (job.Check.State == "issues" || job.Check.State == "unavailable")
	default:
		return job.State == c.Result
	}
}

func (s *backupJobStore) page(c backupJobPageCursor, anchored bool) backupJobPage {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := backupJobPage{Jobs: make([]backupJobSummary, 0, backupJobPageSize+1)}
	// 只保留本页和一个探测项，不复制全部摘要或为全部记录读取历史关联。
	for _, record := range s.records {
		job := record.Summary
		if !c.matches(job) {
			continue
		}
		key := jobKey(job)
		if anchored && key.newer(c.Upper) {
			p.HasNew = true
			continue
		}
		if c.Before != nil && !c.Before.newer(key) {
			continue
		}
		index := sort.Search(len(p.Jobs), func(i int) bool { return key.newer(jobKey(p.Jobs[i])) })
		if index > backupJobPageSize {
			continue
		}
		p.Jobs = append(p.Jobs, backupJobSummary{})
		copy(p.Jobs[index+1:], p.Jobs[index:])
		p.Jobs[index] = job
		if len(p.Jobs) > backupJobPageSize+1 {
			p.Jobs = p.Jobs[:backupJobPageSize+1]
		}
	}
	if !anchored && len(p.Jobs) > 0 {
		c.Upper = jobKey(p.Jobs[0])
	}
	p.Cursor = c.encode()
	if len(p.Jobs) > backupJobPageSize {
		p.HasMore = true
		p.Jobs = p.Jobs[:backupJobPageSize]
		last := jobKey(p.Jobs[len(p.Jobs)-1])
		c.Before = &last
		p.NextCursor = c.encode()
	}
	return p
}
