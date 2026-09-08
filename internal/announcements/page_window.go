package announcements

import "github.com/waiting-here/NonbiriAPI/internal/pagination"

func validListWindow(cursor string, limit int, page *pagination.Request) bool {
	if page != nil {
		return page.Valid() && cursor == "" && limit == 0
	}
	return validLimit(limit)
}
