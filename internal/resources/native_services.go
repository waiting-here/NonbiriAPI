package resources

import (
	"context"
	"database/sql"
	"net/url"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func endpointServiceSelection(kind contract.Type, selected *[]contract.NativeService, format contract.RerankFormat) (contract.NativeServiceSet, error) {
	services, err := contract.LegacyNativeServices(kind)
	if selected != nil {
		services, err = contract.ParseNativeServices(*selected)
	}
	if err != nil || contract.ValidateNativeServices(kind, services, format) != nil {
		return contract.NativeServiceSet{}, ErrInvalidRequest
	}
	return services, nil
}

func (r *Repository) nativeServiceOptions() ([]NativeServiceOptions, error) {
	options := []NativeServiceOptions{}
	for _, kind := range r.connectors.Types() {
		supported, err := contract.SupportedNativeServices(kind)
		if err != nil {
			return nil, ErrUnavailable
		}
		legacy, err := contract.LegacyNativeServices(kind)
		if err != nil {
			return nil, ErrUnavailable
		}
		formats, err := contract.SupportedRerankFormats(kind)
		if err != nil {
			return nil, ErrUnavailable
		}
		options = append(options, NativeServiceOptions{
			ConnectorType: kind, ConfigurationType: kind.ConfigurationType(), NativeServices: supported.Services(),
			DefaultNativeServices: legacy.Services(), RerankFormats: formats,
		})
	}
	return options, nil
}

func bindingServiceProjection(kind contract.Type, services contract.NativeServiceSet, format contract.RerankFormat) ([]contract.Operation, []BindingServiceRoute) {
	operations, routes := []contract.Operation{}, []BindingServiceRoute{}
	for _, operation := range contract.AllOperations() {
		choices, err := connector.ResolveServiceRoutes(kind, services, format, operation)
		if err != nil {
			continue
		}
		operations = append(operations, operation)
		for _, choice := range choices {
			mode := "converted"
			if choice.NativeMatch {
				mode = "native"
			}
			routes = append(routes, BindingServiceRoute{Operation: operation, NativeService: choice.NativeService, Mode: mode})
		}
	}
	return operations, routes
}

func requireConnectionTx(ctx context.Context, tx *sql.Tx, userID, connectionID int64) error {
	if userID <= 0 || connectionID <= 0 {
		return ErrNotFound
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM service_connections WHERE id=? AND user_id=?)`, connectionID, userID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func defaultConnectionName(baseURL, channelName string) string {
	name := channelName
	if name == "" {
		if parsed, err := url.Parse(baseURL); err == nil {
			name = parsed.Hostname()
		}
	}
	runes := []rune(name)
	if len(runes) > 128 {
		name = string(runes[:128])
	}
	return name
}

func nativeServiceStrings(services []contract.NativeService) []string {
	result := make([]string, len(services))
	for i, service := range services {
		result[i] = string(service)
	}
	return result
}

func applyCandidateServiceProjection(item *BindingCandidate, mask int64, format string) error {
	services, err := contract.NativeServicesFromMask(mask)
	if err != nil || services.Empty() {
		return ErrUnavailable
	}
	kind := contract.Type(item.ConnectorType)
	if contract.ValidateNativeServices(kind, services, contract.RerankFormat(format)) != nil {
		return ErrUnavailable
	}
	item.NativeServices = services.Services()
	item.ModelTypes, item.ServiceRoutes = bindingServiceProjection(kind, services, contract.RerankFormat(format))
	item.Capabilities = BindingCapabilities{CanBind: true}
	return nil
}
