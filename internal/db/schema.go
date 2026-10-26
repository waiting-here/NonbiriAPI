// Package db owns the canonical SQLite schema and current storage validation.
package db

import _ "embed"

//go:embed generation_two.sql
var generationTwoBaseSchema string

//go:embed agent_workflow.sql
var AgentWorkflowSQL string

var generationTwoSchema = generationTwoBaseSchema + "\n" + AgentWorkflowSQL + "\n" + endpointTagRulesSchema
