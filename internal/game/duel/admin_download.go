package duel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/historyexport"
)

func (s *Service) downloadHistory(w http.ResponseWriter, r *http.Request, in AdminPageInput) {
	if in.Cursor != "" || in.Limit != 0 || s.validateSelection(in.Dataset, AdminSelection{}) != nil || s.validateSelection("recent", in.Selection) != nil {
		writeError(w, ErrInvalidRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Hour)
	defer cancel()
	type stage struct {
		input AdminExportInput
		page  AdminExportPage
	}
	stages := []stage{}
	undated := in.Dataset == "anonymous" && in.Selection.From == nil && in.Selection.To == nil
	if undated {
		stages = append(stages, stage{input: AdminExportInput{Dataset: "anonymous", Selection: in.Selection}})
	}
	stages = append(stages, stage{input: AdminExportInput{Dataset: "recent", Selection: in.Selection}})
	// Freeze the archive before the recent set, so retention cannot add a duplicate later.
	for i := range stages {
		page, err := s.AdminExport(ctx, stages[i].input)
		if err != nil {
			writeAdminValue(w, nil, err)
			return
		}
		stages[i].page = page
	}
	summary := historyexport.Summary{RecordsFormat: "duel-history-v1", Dataset: in.Dataset, IncludesUndated: undated}
	err := historyexport.Write(ctx, w, "duel-history-"+s.rules.ID()+"-"+in.Dataset+".zip", summary, func(out io.Writer, summary *historyexport.Summary) error {
		var alias string
		for _, stage := range stages {
			page := stage.page
			for {
				for _, raw := range page.Items {
					if err := ctx.Err(); err != nil {
						return err
					}
					if in.Dataset == "anonymous" && stage.input.Dataset == "recent" {
						var err error
						raw, err = s.anonymousDownloadRecord(raw, &alias)
						if err != nil {
							return err
						}
					}
					if _, err := out.Write(append(raw, '\n')); err != nil {
						return err
					}
					summary.Records++
				}
				summary.ExpiredSkipped += page.ExpiredSkipped
				if page.NextCursor == nil {
					break
				}
				stage.input.Cursor = page.NextCursor
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

func (s *Service) anonymousDownloadRecord(raw json.RawMessage, alias *string) (json.RawMessage, error) {
	var kind struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &kind); err != nil {
		return nil, err
	}
	if kind.Kind == "match" {
		var item AdminMatch
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		id, err := s.generate("dah_")
		if err != nil {
			return nil, err
		}
		*alias, item.MatchRef, item.Recent = id, id, nil
		return Encode(item)
	}
	var item AdminRound
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, err
	}
	item.MatchRef = *alias
	return Encode(item)
}
