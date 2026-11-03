package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"

	"github.com/waiting-here/NonbiriAPI/docs"
	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/aiagent"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func managementRequestSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"route":{"type":"string","description":"Exact METHOD /registered/path/{id} from management_catalog."},"path_params":{"type":"object","additionalProperties":{"type":"string"}},"query":{"type":"object","additionalProperties":{"type":"string"}},"body":{"description":"Exact JSON request body from the shipped contract. Omit for bodyless requests."},"body_base64":{"type":"string","description":"Only for binary upload APIs, instead of body."},"content_type":{"type":"string","description":"Required only with body_base64."}},"required":["route"],"additionalProperties":false}`)
}

func (o *agentOperations) managementTools(automatic bool) []aiagent.Tool {
	if o.runtime == nil {
		return nil
	}
	out := []aiagent.Tool{
		{Name: "management_catalog", Description: "Find the live administrator/steward API catalog and its shipped request contracts. Search route names, select a route to inspect its effect, or read a named documentation file in pages. Permissions and resource restrictions remain enforced by the real handler.", Parameters: json.RawMessage(`{"type":"object","properties":{"search":{"type":"string"},"route":{"type":"string"},"document":{"type":"string","description":"A documentation file returned by this tool; api-contract.md is the main wire contract."},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`)},
		{Name: "management_read", Description: "Execute a registered read operation immediately. Includes read-only POST queries. First look up its exact route and body/query contract in management_catalog. Never use for mutations or model discovery.", Parameters: managementRequestSchema()},
		{Name: "refresh_models", Description: "Re-fetch upstream model lists immediately through a registered model_discovery operation. Does not approve donations, select models or change bindings. Read the response; asynchronous discovery may require polling its status with management_read.", Parameters: managementRequestSchema()},
	}
	if !automatic {
		out = append(out, aiagent.Tool{Name: "propose_management_operation", ChatOnly: true, Description: "Propose ONE registered write operation for the current human's approval. First read the current resource and request contract. Include exact revisions/selection and request body. Never executes before approval. Do not combine with other write tool calls in the same turn; separate approved API operations are not one atomic batch.", Parameters: managementRequestSchema()})
	}
	return out
}

func decodeManagement(raw json.RawMessage, value any) error {
	if len(raw) > 12<<20 {
		return aiagent.ErrInvalid
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return aiagent.ErrInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return aiagent.ErrInvalid
	}
	return nil
}

func (o *agentOperations) readManagement(ctx context.Context, actor agentplan.Actor, name string, raw json.RawMessage) (json.RawMessage, error) {
	if o.runtime == nil {
		return nil, aiagent.ErrUnavailable
	}
	if name == "management_catalog" {
		return o.managementCatalog(ctx, actor, raw)
	}
	var input auth.ManagementRequest
	if err := decodeManagement(raw, &input); err != nil {
		return nil, err
	}
	effect := auth.ManagementRead
	if name == "refresh_models" {
		effect = auth.ManagementDiscovery
	}
	var key [24]byte
	if _, err := rand.Read(key[:]); err != nil {
		return nil, err
	}
	return o.runtime.ExecuteManagement(ctx, sessionAgentActor(actor), actor.Automatic, input, effect, "agent-"+hex.EncodeToString(key[:]))
}

func (o *agentOperations) managementCatalog(ctx context.Context, actor agentplan.Actor, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Search   string `json:"search"`
		Route    string `json:"route"`
		Document string `json:"document"`
		Offset   int    `json:"offset"`
	}
	if err := decodeManagement(raw, &input); err != nil || input.Offset < 0 || len(input.Search) > 256 {
		return nil, aiagent.ErrInvalid
	}
	routes, err := o.runtime.ManagementCatalog(ctx, sessionAgentActor(actor), actor.Automatic)
	if err != nil {
		return nil, err
	}
	if input.Document != "" {
		if strings.ContainsAny(input.Document, "/\\") || !strings.HasSuffix(input.Document, ".md") {
			return nil, aiagent.ErrInvalid
		}
		b, err := docs.Files.ReadFile(input.Document)
		if err != nil {
			return nil, aiagent.ErrNotFound
		}
		lines := strings.Split(string(b), "\n")
		if input.Offset > len(lines) {
			return nil, aiagent.ErrInvalid
		}
		start := input.Offset
		if input.Search != "" {
			for start < len(lines) && !strings.Contains(strings.ToLower(lines[start]), strings.ToLower(input.Search)) {
				start++
			}
		}
		end := start
		size := 0
		for end < len(lines) && end-start < 100 && size+len(lines[end]) < 14000 {
			size += len(lines[end]) + 1
			end++
		}
		return json.Marshal(map[string]any{"document": input.Document, "offset": start, "next_offset": end, "total_lines": len(lines), "text": strings.Join(lines[start:end], "\n")})
	}
	matched := []auth.ManagementRoute{}
	for _, route := range routes {
		if input.Route != "" && input.Route != route.Route || !strings.Contains(strings.ToLower(route.Route), strings.ToLower(input.Search)) {
			continue
		}
		matched = append(matched, route)
	}
	start := min(input.Offset, len(matched))
	end := min(start+40, len(matched))
	files, _ := docs.Files.ReadDir(".")
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, file.Name())
	}
	return json.Marshal(map[string]any{"routes": matched[start:end], "total": len(matched), "next_offset": end, "documents": names, "instructions": "Read api-contract.md (document + search path/domain + offset) and relevant domain docs before calling. IDs/revisions are strings. Read current values before proposing writes. Request keys, authentication and elevation come from the host, never the model. A 202 response means accepted, not finished."})
}

func (o *agentOperations) PreviewManagement(ctx context.Context, actor agentplan.Actor, op agentplan.Operation) (agentplan.Preview, error) {
	if actor.Automatic || o.runtime == nil || op.Kind != "management.api" {
		return agentplan.Preview{}, aiagent.ErrForbidden
	}
	var input auth.ManagementRequest
	if err := decodeManagement(op.Input, &input); err != nil {
		return agentplan.Preview{}, err
	}
	if _, err := o.runtime.ResolveManagement(ctx, sessionAgentActor(actor), false, input, auth.ManagementWrite); err != nil {
		return agentplan.Preview{}, err
	}
	raw, _ := json.MarshalIndent(input, "", "  ")
	method, path, _ := strings.Cut(input.Route, " ")
	meta, _ := managementlog.DescribeRoute(method, path)
	title := meta.Summary
	if title == "" {
		title = "管理操作 · " + input.Route
	}
	return agentplan.Preview{Title: title, Description: "批准后按当前权限执行此请求。资源版本、操作范围和业务规则由原管理接口再次校验。", After: string(raw), Dangerous: true, RequiresElevation: managementNeedsElevation(input.Route)}, nil
}

func managementNeedsElevation(route string) bool {
	// The domain remains the final authority. These known sensitive operations
	// request the browser's existing one-use elevation flow before execution.
	return strings.Contains(route, "/legal-hold") || strings.Contains(route, "/mfa/reset") || route == "DELETE /admin/api/users/{id}"
}

func (o *agentOperations) ExecuteManagement(ctx context.Context, actor agentplan.Actor, op agentplan.Operation, requestKey string) (json.RawMessage, error) {
	if actor.Automatic || o.runtime == nil || op.Kind != "management.api" {
		return nil, aiagent.ErrForbidden
	}
	var input auth.ManagementRequest
	if err := decodeManagement(op.Input, &input); err != nil {
		return nil, err
	}
	return o.runtime.ExecuteManagement(ctx, sessionAgentActor(actor), false, input, auth.ManagementWrite, requestKey)
}
