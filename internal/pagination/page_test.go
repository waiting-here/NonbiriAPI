package pagination

import (
	"math"
	"net/url"
	"testing"
)

func TestPageProtocolRejectsAmbiguity(t *testing.T) {
	for _, query := range []string{
		"page=", "page=0", "page=-1", "page=01", "page=%2B1", "page=1e2", "page=1.0", "page=１",
		"page=2147483648", "page=9999999999999999999999", "page=1&page=2", "page_size=20&page_size=20",
		"page_size=020", "page_size=0", "page_size=30", "page=1&cursor=", "page=1&limit=20", "page_size=10&cursor=x",
	} {
		t.Run(query, func(t *testing.T) {
			values, err := url.ParseQuery(query)
			if err != nil {
				t.Fatal(err)
			}
			if _, selected, err := Parse(values); err == nil || !selected {
				t.Fatalf("ambiguous page accepted: selected=%v err=%v", selected, err)
			}
		})
	}
	for _, query := range []string{"", "cursor=x&limit=20"} {
		values, _ := url.ParseQuery(query)
		if _, selected, err := Parse(values); err != nil || selected {
			t.Fatalf("legacy rejected: %v %v", selected, err)
		}
	}
}

func TestPageWindowsClampAndPreserveExactCounts(t *testing.T) {
	for _, test := range []struct {
		query       string
		total       int64
		page, pages string
		offset      int64
		size        int
	}{
		{"page=2147483647", 0, "1", "1", 0, 20},
		{"page=3&page_size=20", 42, "3", "3", 40, 20},
		{"page=99&page_size=10", 20, "2", "2", 10, 10},
		{"page_size=50", 51, "1", "2", 0, 50},
		{"page=2147483647&page_size=100", math.MaxInt64, "2147483647", "92233720368547759", 214748364600, 100},
	} {
		values, _ := url.ParseQuery(test.query)
		request, selected, err := Parse(values)
		if err != nil || !selected {
			t.Fatalf("parse %s: %v", test.query, err)
		}
		meta, offset, err := request.Window(test.total)
		if err != nil || meta.Page != test.page || meta.TotalPages != test.pages || offset != test.offset || meta.PageSize != test.size {
			t.Fatalf("window %s = %+v offset=%d err=%v", test.query, meta, offset, err)
		}
	}
	if _, _, err := Default().Window(-1); err == nil {
		t.Fatal("negative total accepted")
	}
}
