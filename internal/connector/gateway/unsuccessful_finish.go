package gateway

import (
	"encoding/json"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

var errUnsuccessfulFinish = errors.New("gateway: upstream generation reported an unsuccessful finish")

const unsuccessfulFinishDiagnostic = "upstream generation reported an unsuccessful finish"

func unsuccessfulFinishDetail(raw []byte, context upstreamerror.Context) upstreamerror.Detail {
	value, err := parseObject(raw)
	if err != nil {
		return upstreamerror.Detail{}
	}
	reason, ok := text(value["raw"])
	if !ok || len(reason) > upstreamerror.MaxBodyBytes/2 {
		return upstreamerror.Detail{}
	}
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"message": reason, "code": reason}})
	defer clear(body)
	return context.Parse(body)
}
