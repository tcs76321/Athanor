package ui

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
)

// notificationCounts is the header badge and the notifications page summary.
type notificationCounts struct {
	Pending  int `json:"pending"`
	Reviews  int `json:"reviews"`
	Alarms   int `json:"alarms"`
	Failures int `json:"failures"`
	Total    int `json:"total"`
}

type failedJobRow struct {
	ID, ProjectID, UpdatedAt string
}

func (u *UI) notificationCounts(r *http.Request) notificationCounts {
	ctx := r.Context()
	var c notificationCounts
	if u.deps.HITL != nil {
		if p, err := u.deps.HITL.Pending(ctx); err == nil {
			c.Pending = len(p)
		}
	}
	if u.deps.Reviews != nil {
		if q, err := u.deps.Reviews.Queue(ctx, 100); err == nil {
			c.Reviews = len(q)
		}
	}
	_ = u.deps.Store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM alarms WHERE status = 'active'`).Scan(&c.Alarms)
	_ = u.deps.Store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs
		  WHERE state = 'failed'
		    AND updated_at >= strftime('%Y-%m-%dT%H:%M:%fZ','now','-24 hours')`).Scan(&c.Failures)
	c.Total = c.Pending + c.Reviews + c.Alarms + c.Failures
	return c
}

func (u *UI) handleNotificationCount(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(u.notificationCounts(r))
}

func (u *UI) handleNotifications(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c := u.notificationCounts(r)
	var pending []string
	if u.deps.HITL != nil {
		if ps, err := u.deps.HITL.Pending(ctx); err == nil {
			for _, p := range ps {
				pending = append(pending, string(p.Type)+" · "+string(p.Severity)+" · job "+shortID(p.JobID))
			}
		}
	}
	var reviews []string
	if u.deps.Reviews != nil {
		if ids, err := u.deps.Reviews.Queue(ctx, 20); err == nil {
			for _, id := range ids {
				if a, err := u.deps.Artifacts.Get(ctx, id); err == nil {
					reviews = append(reviews, string(a.Kind)+" v"+strconv.Itoa(a.Version)+" · "+shortID(id))
				}
			}
		}
	}
	alarms := queryRows(ctx, u.deps.Store.DB(), `
		SELECT id, category, level, message, status, created_at
		  FROM alarms WHERE status = 'active'
		 ORDER BY created_at DESC LIMIT 20`, func(rows *sql.Rows) (alarmRow, error) {
		var a alarmRow
		return a, rows.Scan(&a.ID, &a.Category, &a.Level, &a.Message, &a.Status, &a.CreatedAt)
	})
	failures := queryRows(ctx, u.deps.Store.DB(), `
		SELECT id, COALESCE(project_id,''), updated_at
		  FROM jobs WHERE state = 'failed'
		    AND updated_at >= strftime('%Y-%m-%dT%H:%M:%fZ','now','-24 hours')
		 ORDER BY updated_at DESC LIMIT 20`, func(rows *sql.Rows) (failedJobRow, error) {
		var f failedJobRow
		return f, rows.Scan(&f.ID, &f.ProjectID, &f.UpdatedAt)
	})
	u.render(w, "Notifications", notificationsBody, map[string]any{
		"Counts": c, "Pending": pending, "Reviews": reviews,
		"Alarms": alarms, "Failures": failures,
	})
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
