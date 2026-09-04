package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/game/rps"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const (
	accountEventsRoute    = "/api/events"
	maxAccountEventAccept = 1024
	maxAccountEventCursor = 256
)

type accountEventSessionVerifier interface {
	VerifyUserSessionBinding(context.Context, int64, string) (auth.UserSessionBindingState, error)
}

type accountEventRPSReader interface {
	Read(context.Context, rps.ReadInput) (rps.HomeState, error)
}

type accountEventMaintenanceState interface {
	State() (maintenance.State, bool)
}

type accountEventsHTTP struct {
	verifier    accountEventSessionVerifier
	maintenance accountEventMaintenanceState
	rps         accountEventRPSReader
	hub         *accountstream.Hub
	connections *accountEventConnections
}

func registerAccountEventRoute(runtime *auth.Runtime, gate *maintenance.Gate, rpsService *rps.Service, hub *accountstream.Hub, connections *accountEventConnections) error {
	if runtime == nil || gate == nil || rpsService == nil || hub == nil || connections == nil {
		return errors.New("account event route dependencies are required")
	}
	handler := &accountEventsHTTP{
		verifier: runtime, maintenance: gate, rps: rpsService, hub: hub, connections: connections,
	}
	return runtime.RegisterContinuationUserRoute(http.MethodGet, accountEventsRoute, handler.serve)
}

func (handler *accountEventsHTTP) serve(writer http.ResponseWriter, request *http.Request, principal resources.ContinuationUserPrincipal) {
	if handler == nil || handler.verifier == nil || handler.maintenance == nil || handler.rps == nil || handler.hub == nil || handler.connections == nil {
		writeAccountEventError(writer, httperr.CodeServiceUnavailable, "service unavailable")
		return
	}
	if request == nil || principal.UserID <= 0 || principal.SessionBinding == "" {
		writeAccountEventError(writer, httperr.CodeUnauthorized, "authentication required")
		return
	}
	if requestHasQuery(request) || requestCarriesBody(request) || !acceptsAccountEventStream(request.Header.Values("Accept")) {
		writeAccountEventError(writer, httperr.CodeInvalidRequest, "invalid request")
		return
	}
	cursor, ok := accountEventCursor(request.Header.Values("Last-Event-ID"))
	if !ok {
		writeAccountEventError(writer, httperr.CodeInvalidRequest, "invalid request")
		return
	}
	state, ready := handler.maintenance.State()
	if !ready {
		writeAccountEventError(writer, httperr.CodeServiceUnavailable, "service unavailable")
		return
	}
	channels := []accountstream.Channel{accountstream.ChannelActivities, accountstream.ChannelRPS}
	if state.Enabled {
		if _, err := handler.rps.Read(request.Context(), rps.ReadInput{UserID: principal.UserID, SessionBinding: principal.SessionBinding}); err != nil {
			handler.writeRPSError(writer, request, err)
			return
		}
		channels = []accountstream.Channel{accountstream.ChannelRPS}
	}
	streamContext, unregister, err := handler.connections.Register(request.Context(), principal.UserID)
	if err != nil {
		writeAccountEventError(writer, httperr.CodeServiceUnavailable, "service unavailable")
		return
	}
	defer unregister()
	if !handler.verifyBinding(writer, streamContext, principal) {
		return
	}
	subscription, err := handler.hub.Subscribe(streamContext, accountstream.SubscribeRequest{
		AccountID: principal.UserID, Channels: channels, LastEventID: cursor,
	})
	if err != nil {
		switch {
		case errors.Is(err, accountstream.ErrCapacity):
			writeAccountEventError(writer, httperr.CodeResourceLimitExceeded, "resource limit exceeded")
		default:
			writeAccountEventError(writer, httperr.CodeServiceUnavailable, "service unavailable")
		}
		return
	}
	if err := subscription.Stream(streamContext, writer); err != nil && streamContext.Err() == nil && request.Context().Err() == nil {
		slog.Warn("account event stream stopped", "err", err)
	}
}

func (handler *accountEventsHTTP) verifyBinding(writer http.ResponseWriter, ctx context.Context, principal resources.ContinuationUserPrincipal) bool {
	state, err := handler.verifier.VerifyUserSessionBinding(ctx, principal.UserID, principal.SessionBinding)
	if err != nil || state == auth.UserSessionBindingUncertain {
		if ctx.Err() == nil {
			writeAccountEventError(writer, httperr.CodeServiceUnavailable, "service unavailable")
		}
		return false
	}
	switch state {
	case auth.UserSessionBindingActive:
		return true
	case auth.UserSessionBindingBanned:
		writeAccountEventError(writer, httperr.CodeForbidden, "access forbidden")
	default:
		writeAccountEventError(writer, httperr.CodeUnauthorized, "authentication required")
	}
	return false
}

func (handler *accountEventsHTTP) writeRPSError(writer http.ResponseWriter, request *http.Request, err error) {
	if request != nil && request.Context().Err() != nil {
		return
	}
	switch {
	case errors.Is(err, rps.ErrMaintenance):
		writeAccountEventError(writer, httperr.CodeMaintenance, "maintenance in progress")
	case errors.Is(err, rps.ErrUnauthorized), errors.Is(err, rps.ErrNotFound):
		writeAccountEventError(writer, httperr.CodeUnauthorized, "authentication required")
	case errors.Is(err, rps.ErrForbidden):
		writeAccountEventError(writer, httperr.CodeForbidden, "access forbidden")
	default:
		writeAccountEventError(writer, httperr.CodeServiceUnavailable, "service unavailable")
	}
}

func acceptsAccountEventStream(values []string) bool {
	if len(values) == 0 {
		return false
	}
	total := 0
	for _, value := range values {
		total += len(value)
		if total > maxAccountEventAccept {
			return false
		}
		for _, token := range strings.Split(value, ",") {
			media := strings.TrimSpace(strings.SplitN(token, ";", 2)[0])
			if strings.EqualFold(media, "text/event-stream") {
				return true
			}
		}
	}
	return false
}

func accountEventCursor(values []string) (string, bool) {
	if len(values) == 0 {
		return "", true
	}
	if len(values) != 1 || len(values[0]) > maxAccountEventCursor || strings.ContainsAny(values[0], "\x00\r\n") {
		return "", false
	}
	return values[0], true
}

func writeAccountEventError(writer http.ResponseWriter, code, message string) {
	if writer == nil {
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	httperr.WriteError(writer, httperr.New(code, message))
}
