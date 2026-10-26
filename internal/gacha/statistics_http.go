package gacha

import (
	"io"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const statisticsPoolsRoute = "/admin/api/gacha/statistics/pools"
const statisticsCardsRoute = "/admin/api/gacha/statistics/pools/{id}/cards"
const statisticsExportRoute = "/admin/api/gacha/statistics/pools/{id}/export"

func statisticsQuery(r *http.Request) (StatisticsQuery, error) {
	if !empty(r, true) {
		return StatisticsQuery{}, ErrInvalid
	}
	values := r.URL.Query()
	for key, entries := range values {
		if len(entries) != 1 || key != "page" && key != "page_size" && key != "q" && key != "sort" {
			return StatisticsQuery{}, ErrInvalid
		}
	}
	page, _, err := pagination.Parse(values)
	if err != nil {
		return StatisticsQuery{}, ErrInvalid
	}
	return StatisticsQuery{Page: page, Search: values.Get("q"), Sort: values.Get("sort")}, nil
}

type statisticsExportWriter struct {
	io.Writer
	started bool
}

func (w *statisticsExportWriter) Write(body []byte) (int, error) {
	w.started = true
	return w.Writer.Write(body)
}

func registerStatisticsRoutes(admins limitedactivities.AdminRouteRegistrar, s *Service) error {
	if err := admins.RegisterAdminRoute(http.MethodGet, statisticsPoolsRoute, func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
		query, err := statisticsQuery(r)
		if err != nil {
			writeError(w, err)
			return
		}
		out, err := s.AdminPoolStatistics(r.Context(), p.UserID, query)
		respond(w, out, err)
	}); err != nil {
		return err
	}
	if err := admins.RegisterAdminRoute(http.MethodGet, statisticsCardsRoute, func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
		query, err := statisticsQuery(r)
		if err != nil {
			writeError(w, err)
			return
		}
		out, err := s.AdminCardStatistics(r.Context(), p.UserID, r.PathValue("id"), query)
		respond(w, out, err)
	}); err != nil {
		return err
	}
	return admins.RegisterAdminRoute(http.MethodGet, statisticsExportRoute, func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
		values := r.URL.Query()
		if !empty(r, true) || len(values) != 1 || len(values["format"]) != 1 || values.Get("format") != "json" && values.Get("format") != "csv" {
			writeError(w, ErrInvalid)
			return
		}
		format := values.Get("format")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", `attachment; filename="nonbiriapi-pool-statistics.`+format+`"`)
		if format == "csv" {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		} else {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
		}
		writer := &statisticsExportWriter{Writer: w}
		if err := s.ExportStatistics(r.Context(), p.UserID, r.PathValue("id"), format, writer); err != nil {
			if writer.started {
				panic(http.ErrAbortHandler)
			}
			w.Header().Del("Content-Disposition")
			writeError(w, err)
		}
	})
}
