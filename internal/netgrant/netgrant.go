// Package netgrant persists project-scoped, time-bounded egress grants
// (ADR-0139). A grant is an operator-approved URL base: scheme + exact host +
// path prefix, with an optional expiry. It is the project half of the network
// policy; the global allow/deny lists remain the ceiling.
package netgrant

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/ids"
	"github.com/tcs76321/athanor/internal/store"
)

// Status values persisted in `network_grants.status`.
const (
	StatusActive  = "active"
	StatusRevoked = "revoked"
)

// Grant is one stored URL-base grant.
type Grant struct {
	ID         string
	ProjectID  string
	Scheme     string
	Host       string
	PathPrefix string
	Reason     string
	Status     string
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
}

// CreateInput is one new grant. ExpiresAt nil means no expiry.
type CreateInput struct {
	ProjectID  string
	Scheme     string
	Host       string
	PathPrefix string
	Reason     string
	ExpiresAt  *time.Time
}

// Repo persists grants.
type Repo struct{ store *store.Store }

// NewRepo returns a repository over s.
func NewRepo(s *store.Store) *Repo { return &Repo{store: s} }

// Create validates and inserts a grant.
func (r *Repo) Create(ctx context.Context, in CreateInput) (Grant, error) {
	in.ProjectID = strings.TrimSpace(in.ProjectID)
	if in.ProjectID == "" {
		return Grant{}, fmt.Errorf("netgrant: project id is required")
	}
	in.Scheme = strings.ToLower(strings.TrimSpace(in.Scheme))
	if in.Scheme == "" {
		in.Scheme = "https"
	}
	if in.Scheme != "http" && in.Scheme != "https" {
		return Grant{}, fmt.Errorf("netgrant: scheme must be http or https, got %q", in.Scheme)
	}
	in.Host = strings.ToLower(strings.TrimSpace(in.Host))
	if in.Host == "" {
		return Grant{}, fmt.Errorf("netgrant: host is required")
	}
	if !isASCII(in.Host) {
		return Grant{}, fmt.Errorf("netgrant: host %q contains non-ASCII characters", in.Host)
	}
	in.PathPrefix = cleanPath(in.PathPrefix)
	id := ids.New()
	var expires any
	if in.ExpiresAt != nil {
		expires = in.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if _, err := r.store.DB().ExecContext(ctx,
		`INSERT INTO network_grants (id, project_id, scheme, host, path_prefix, reason, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, in.ProjectID, in.Scheme, in.Host, in.PathPrefix, strings.TrimSpace(in.Reason), expires,
	); err != nil {
		return Grant{}, fmt.Errorf("netgrant: insert: %w", err)
	}
	return r.Get(ctx, id)
}

// Get loads one grant.
func (r *Repo) Get(ctx context.Context, id string) (Grant, error) {
	row := r.store.DB().QueryRowContext(ctx, `SELECT `+columns+` FROM network_grants WHERE id = ?`, id)
	g, err := scan(row)
	if err != nil {
		return Grant{}, fmt.Errorf("netgrant: load %s: %w", id, err)
	}
	return g, nil
}

// ActiveForProject returns the project's unrevoked, unexpired grants.
func (r *Repo) ActiveForProject(ctx context.Context, projectID string, now time.Time) ([]Grant, error) {
	rows, err := r.store.DB().QueryContext(ctx,
		`SELECT `+columns+` FROM network_grants
		 WHERE project_id = ? AND status = ?
		   AND (expires_at IS NULL OR expires_at > ?)
		 ORDER BY host, path_prefix, id`,
		projectID, StatusActive, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("netgrant: active: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return collect(rows)
}

// ListByProject returns every grant for the project, newest first.
func (r *Repo) ListByProject(ctx context.Context, projectID string) ([]Grant, error) {
	rows, err := r.store.DB().QueryContext(ctx,
		`SELECT `+columns+` FROM network_grants WHERE project_id = ? ORDER BY created_at DESC, id DESC`,
		projectID)
	if err != nil {
		return nil, fmt.Errorf("netgrant: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return collect(rows)
}

// Revoke marks a grant revoked. It is idempotent.
func (r *Repo) Revoke(ctx context.Context, id string) error {
	res, err := r.store.DB().ExecContext(ctx,
		`UPDATE network_grants SET status = ?, revoked_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		 WHERE id = ? AND status = ?`, StatusRevoked, id, StatusActive)
	if err != nil {
		return fmt.Errorf("netgrant: revoke: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Already revoked is fine; a missing id is not.
		var exists int
		if err := r.store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM network_grants WHERE id = ?`, id).Scan(&exists); err != nil {
			return fmt.Errorf("netgrant: revoke lookup: %w", err)
		}
		if exists == 0 {
			return fmt.Errorf("netgrant: grant %s not found", id)
		}
	}
	return nil
}

const columns = `id, project_id, scheme, host, path_prefix, reason, status,
	created_at, COALESCE(expires_at, ''), COALESCE(revoked_at, '')`

func collect(rows *sql.Rows) ([]Grant, error) {
	var out []Grant
	for rows.Next() {
		g, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func scan(row interface{ Scan(...any) error }) (Grant, error) {
	var g Grant
	var created, expires, revoked string
	if err := row.Scan(&g.ID, &g.ProjectID, &g.Scheme, &g.Host, &g.PathPrefix,
		&g.Reason, &g.Status, &created, &expires, &revoked); err != nil {
		return Grant{}, err
	}
	g.CreatedAt, _ = parseTime(created)
	if g.ExpiresAt, _ = parseTimePtr(expires); expires != "" && g.ExpiresAt == nil {
		return Grant{}, fmt.Errorf("netgrant: bad expires_at %q", expires)
	}
	if g.RevokedAt, _ = parseTimePtr(revoked); revoked != "" && g.RevokedAt == nil {
		return Grant{}, fmt.Errorf("netgrant: bad revoked_at %q", revoked)
	}
	return g, nil
}

func parseTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

func parseTimePtr(s string) (*time.Time, bool) {
	if strings.TrimSpace(s) == "" {
		return nil, true
	}
	t, ok := parseTime(s)
	if !ok {
		return nil, false
	}
	return &t, true
}

func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
