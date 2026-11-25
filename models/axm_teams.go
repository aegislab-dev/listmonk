package models

import (
	"strings"

	"github.com/lib/pq"
)

// TeamTagPrefix starts the tag of every team ("팀:마케팅2팀"). A team role holds exactly the
// lists carrying its tag (see migration v6.2.1-axm.8).
const TeamTagPrefix = "팀:"

// IsTeamTag tells whether a list tag names a team.
func IsTeamTag(tag string) bool {
	return strings.HasPrefix(tag, TeamTagPrefix) && len(tag) > len(TeamTagPrefix)
}

// TeamTags returns the team tags among the tags, in order.
func TeamTags(tags []string) []string {
	out := []string{}
	for _, t := range tags {
		if IsTeamTag(t) {
			out = append(out, t)
		}
	}
	return out
}

// WithoutTeamTags returns the tags that do not name a team.
func WithoutTeamTags(tags []string) []string {
	out := []string{}
	for _, t := range tags {
		if !IsTeamTag(t) {
			out = append(out, t)
		}
	}
	return out
}

// TeamTagFromName makes the tag of a team name: the name's whitespace turns into dashes,
// as list tags do.
func TeamTagFromName(name string) string {
	return TeamTagPrefix + strings.Join(strings.Fields(name), "-")
}

// TeamName returns the team name of a team tag.
func TeamName(tag string) string {
	return strings.TrimPrefix(tag, TeamTagPrefix)
}

// Team is a team role seen by the admin: its tag, its lists and its members.
type Team struct {
	ID         int          `db:"id" json:"id"`
	Name       string       `db:"name" json:"name"`
	Tag        string       `db:"tag" json:"tag"`
	UserRoleID *int         `db:"user_role_id" json:"user_role_id"`
	Lists      int          `db:"lists" json:"lists"`
	Members    []TeamMember `db:"-" json:"members"`
	NumMembers int          `db:"members" json:"-"`
	// Wide lists the team-wide permissions (LST-68) the default user role has, WideMembers
	// counts the members whose user role has one (or is the super admin role): they read other
	// teams' data. The teams screen warns about both.
	Wide        pq.StringArray `db:"wide" json:"wide"`
	WideMembers int            `db:"wide_members" json:"wide_members"`
}

// TeamMember is a user who belongs to a team.
type TeamMember struct {
	ID         int    `db:"id" json:"id"`
	Username   string `db:"username" json:"username"`
	Name       string `db:"name" json:"name"`
	Type       string `db:"type" json:"type"`
	Status     string `db:"status" json:"status"`
	UserRoleID int    `db:"user_role_id" json:"user_role_id"`
	TeamID     int    `db:"team_id" json:"-"`
}

// TeamCount is a team tag with the number of lists a user may see under it. RoleID and
// RoleName are only given to users who manage roles.
type TeamCount struct {
	Tag    string `db:"tag" json:"tag"`
	Name   string `db:"-" json:"name"`
	Lists  int    `db:"lists" json:"lists"`
	RoleID *int   `db:"role_id" json:"role_id,omitempty"`
	// HasRole tells that a team role exists for the tag, to everybody (the screens that make
	// lists offer only the teams that exist; role_id is for those who manage roles).
	HasRole  bool    `db:"-" json:"has_role"`
	RoleName *string `db:"role_name" json:"role_name,omitempty"`
}

// OtherTeam says that a subscriber is on lists of another team than the viewer's lists.
// Team is the team name ("" = lists without a team). It never carries a list name or ID.
type OtherTeam struct {
	SubscriberID int    `db:"subscriber_id" json:"-"`
	Team         string `db:"team" json:"team"`
	Lists        int    `db:"lists" json:"lists"`
}
