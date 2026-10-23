package charityscope

import (
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

// Route predicates are derived from the same structural selector as dispatch.
// They do not assert that an arbitrary request body passes wire preparation.
var serviceOperationCases = buildServiceOperationCases()

func buildServiceOperationCases() string {
	var out strings.Builder
	for _, kind := range connector.NewDefaultRegistry().Types() {
		services, err := contract.SupportedNativeServices(kind)
		if err != nil {
			continue
		}
		out.WriteString(" WHEN '")
		out.WriteString(string(kind))
		out.WriteString("' THEN (CASE SERVICE_BIT")
		format := contract.RerankFormat("")
		if kind.OpenAIServiceFamily() {
			format = contract.RerankFormatGeneric
		}
		for _, service := range services.Services() {
			set, _ := contract.ParseNativeServices([]contract.NativeService{service})
			var mask int64
			for i, op := range contract.AllOperations() {
				if routes, err := connector.ResolveServiceRoutes(kind, set, format, op); err == nil && len(routes) > 0 {
					mask |= 1 << i
				}
			}
			if mask != 0 {
				out.WriteString(" WHEN ")
				out.WriteString(strconv.FormatInt(set.Mask(), 10))
				out.WriteString(" THEN ")
				out.WriteString(strconv.FormatInt(mask, 10))
			}
		}
		out.WriteString(" ELSE 0 END)")
	}
	return out.String()
}

// AvailableUseSQL accepts repository-owned expressions only. Domain quotas,
// donor eligibility and legacy failure gates remain in the caller's query.
func AvailableUseSQL(memberID, targetMask, modelTypes, now string) string {
	operations := `(CASE ag.configured_type` + strings.ReplaceAll(serviceOperationCases, "SERVICE_BIT", "av.native_service_bit") + ` ELSE 0 END)`
	return `EXISTS(SELECT 1 FROM charity_use_availability av
JOIN endpoint_grant_authorizations ag ON ag.grant_revision_id=av.grant_revision_id
LEFT JOIN donation_service_health service_health ON service_health.use_id=av.use_id AND service_health.endpoint_key_id=ag.endpoint_key_id AND service_health.native_service_bit=av.native_service_bit AND service_health.credential_version_id=av.credential_version_id
WHERE av.donation_key_id=` + memberID + ` AND (av.native_service_bit&(` + targetMask + `))<>0
AND (` + operations + `&(` + modelTypes + `))<>0 AND COALESCE(service_health.failure_disabled,0)=0
AND ` + smartbackoff.ServiceUntilSQL("service_health") + `<=` + now + `)`
}
