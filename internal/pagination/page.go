// Package pagination implements optional page-number windows alongside legacy
// cursors. Domain handlers retain responsibility for query and authorization scopes.
package pagination

import (
	"errors"
	"net/url"
	"strconv"
)

const MaxPage = int64(2147483647)

var ErrInvalid = errors.New("pagination: invalid page window")

type Request struct {
	Page int64
	Size int
}

type Metadata struct {
	Page       string `json:"page"`
	PageSize   int    `json:"page_size"`
	TotalItems string `json:"total_items"`
	TotalPages string `json:"total_pages"`
}

func Default() Request { return Request{Page: 1, Size: 20} }

// Parse reports whether page mode was requested, rejecting mixed protocols even
// when a supplied cursor or limit is empty. Other query fields belong to callers.
func Parse(values url.Values) (Request, bool, error) {
	_, pagePresent := values["page"]
	_, sizePresent := values["page_size"]
	if !pagePresent && !sizePresent {
		return Default(), false, nil
	}
	if _, exists := values["cursor"]; exists {
		return Request{}, true, ErrInvalid
	}
	if _, exists := values["limit"]; exists {
		return Request{}, true, ErrInvalid
	}
	request := Default()
	if pagePresent {
		entries := values["page"]
		if len(entries) != 1 || !canonicalPositive(entries[0]) {
			return Request{}, true, ErrInvalid
		}
		number, err := strconv.ParseInt(entries[0], 10, 64)
		if err != nil || number > MaxPage {
			return Request{}, true, ErrInvalid
		}
		request.Page = number
	}
	if sizePresent {
		entries := values["page_size"]
		if len(entries) != 1 {
			return Request{}, true, ErrInvalid
		}
		switch entries[0] {
		case "10":
			request.Size = 10
		case "20":
			request.Size = 20
		case "50":
			request.Size = 50
		case "100":
			request.Size = 100
		default:
			return Request{}, true, ErrInvalid
		}
	}
	return request, true, nil
}

func (request Request) Valid() bool {
	return request.Page >= 1 && request.Page <= MaxPage &&
		(request.Size == 10 || request.Size == 20 || request.Size == 50 || request.Size == 100)
}

// Window clamps to the last page in the same snapshot used for total and rows.
// MaxPage and the size allowlist bound the multiplication well below MaxInt64.
func (request Request) Window(total int64) (Metadata, int64, error) {
	if !request.Valid() || total < 0 {
		return Metadata{}, 0, ErrInvalid
	}
	pages := int64(1)
	if total > 0 {
		pages = (total-1)/int64(request.Size) + 1
	}
	page := min(request.Page, pages)
	return Metadata{
		Page: strconv.FormatInt(page, 10), PageSize: request.Size,
		TotalItems: strconv.FormatInt(total, 10), TotalPages: strconv.FormatInt(pages, 10),
	}, (page - 1) * int64(request.Size), nil
}

func canonicalPositive(value string) bool {
	if value == "" || value[0] < '1' || value[0] > '9' || len(value) > 10 {
		return false
	}
	for _, digit := range value[1:] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}
