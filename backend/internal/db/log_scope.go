package db

import "fmt"

// LogScope is the caller's authorization boundary. Its zero value denies all
// rows. Only root callers may set All; historical logs with no owner are root-only.
// Team membership is resolved from the current user record: api_logs does not
// have a historical team snapshot.
type LogScope struct {
	All    bool
	UserID int
	TeamID int
}

func (s LogScope) appendSQL(query string, args []any) (string, []any) {
	switch {
	case s.All && s.UserID == 0 && s.TeamID == 0:
		return query, args
	case !s.All && s.UserID > 0 && s.TeamID == 0:
		args = append(args, s.UserID)
		return query + fmt.Sprintf(" AND api_logs.user_id = $%d", len(args)), args
	case !s.All && s.TeamID > 0 && s.UserID == 0:
		args = append(args, s.TeamID)
		return query + fmt.Sprintf(" AND api_logs.user_id IN (SELECT id FROM users WHERE team_id = $%d)", len(args)), args
	default:
		return query + " AND FALSE", args
	}
}
