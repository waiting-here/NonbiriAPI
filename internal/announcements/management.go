package announcements

import "strings"

type managementRole uint8

const (
	roleAdmin managementRole = iota
	roleSteward
)

func (role managementRole) actorKind() string {
	if role == roleSteward {
		return "steward"
	}
	return "admin"
}

func (role managementRole) route(adminRoute string) string {
	if role == roleSteward {
		return "/api/steward" + strings.TrimPrefix(adminRoute, "/admin/api")
	}
	return adminRoute
}
