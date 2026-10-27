package stewardautomation

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func (s *Service) ConfigureManagementHistory(history *managementlog.Service) {
	s.managementHistory = history
}
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route := automationRoute{}
	if r.URL != nil {
		route = matchAutomationRoute(r.URL.Path)
	}
	if s.managementHistory == nil || route.personal || r.Method == "GET" || (route.kind != "donations" && route.kind != "charity_write" && route.kind != "failure_policy") {
		s.serveHTTP(w, r)
		return
	}
	pattern := DonationsPath
	switch route.kind {
	case "charity_write":
		pattern = BindingsPath
	case "failure_policy":
		pattern = FailurePolicyPath
	}
	s.managementHistory.Wrap(r.Method, pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if identity, ok := authz.StewardCallerFromContext(r.Context()); ok {
			if err := managementlog.SetIdentity(r.Context(), s.db, identity.UserID, managementlog.SourceCallerKey); err != nil {
				writeError(w, err)
				return
			}
		}
		if meta, ok := managementlog.MetadataFromContext(r.Context()); ok {
			managementlog.SetCorrelation(r.Context(), "automation_batch", meta.EventID)
		}
		s.serveHTTP(w, r)
	})).ServeHTTP(w, r)
}
func (s *Service) finishBindingHistory(ctx context.Context, modelID int64, result bindingsResult) error {
	if _, ok := managementlog.MetadataFromContext(ctx); !ok {
		return nil
	}
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(work, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range result.Results {
		if item.Status == "success" {
			continue
		}
		outcome := managementlog.Failed
		if item.Status == "incomplete" {
			outcome = managementlog.Unknown
		}
		if err = managementlog.AppendContextTx(work, tx, managementlog.Item{Action: "bind", TargetKind: "donation_key_model", TargetID: item.DonationKeyID + "/" + fmt.Sprint(modelID), Outcome: outcome, Code: item.Code}, false); err != nil {
			return err
		}
	}
	if err = managementlog.FinalizeContextTx(work, tx); err != nil {
		return err
	}
	return tx.Commit()
}
