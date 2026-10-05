package blackjack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/historyexport"
)

// AdminDownload streams all selected tables as one ZIP with bounded page reads.
func (s *Service) AdminDownload(w http.ResponseWriter, r *http.Request, in PageInput) {
	if in.Cursor != "" || in.Limit != 0 || in.Dataset != "recent" && in.Dataset != "anonymous" {
		writeError(w, ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Hour)
	defer cancel()
	type stage struct {
		input PageInput
		page  AdminExportPage
	}
	stages := []stage{}
	if in.Dataset == "anonymous" {
		stages = append(stages, stage{input: PageInput{Dataset: "anonymous"}})
	}
	stages = append(stages, stage{input: PageInput{Dataset: "recent"}})
	for i := range stages {
		page, err := s.AdminExport(ctx, stages[i].input)
		if err != nil {
			writeError(w, err)
			return
		}
		stages[i].page = page
	}
	summary := historyexport.Summary{RecordsFormat: "blackjack-history/v1", Dataset: in.Dataset, IncludesUndated: in.Dataset == "anonymous"}
	err := historyexport.Write(ctx, w, "blackjack-history-"+in.Dataset+".zip", summary, func(out io.Writer, summary *historyexport.Summary) error {
		encoder := json.NewEncoder(out)
		for _, stage := range stages {
			page := stage.page
			for {
				for _, item := range page.Items {
					if err := ctx.Err(); err != nil {
						return err
					}
					if in.Dataset == "anonymous" && stage.input.Dataset == "recent" {
						id, err := s.generate("bja_")
						if err != nil {
							return err
						}
						item.ID, item.Dataset, item.Recent = id, "anonymous", nil
					}
					if err := encoder.Encode(item); err != nil {
						return err
					}
					summary.Records++
				}
				if page.NextCursor == nil {
					break
				}
				stage.input.Cursor = *page.NextCursor
				var err error
				page, err = s.AdminExport(ctx, stage.input)
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		panic(http.ErrAbortHandler)
	}
}
