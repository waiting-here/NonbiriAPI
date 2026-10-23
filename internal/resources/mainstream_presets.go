package resources

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type MainstreamEntry struct {
	ID             string                   `json:"id,omitempty"`
	Name           string                   `json:"name"`
	ConnectorType  string                   `json:"connector_type"`
	BaseURL        string                   `json:"base_url"`
	NativeServices []contract.NativeService `json:"native_services"`
	RerankFormat   contract.RerankFormat    `json:"rerank_format"`
	Enabled        bool                     `json:"enabled"`
}

type MainstreamDiscoveryPreset struct {
	BaseURL    string `json:"base_url"`
	ListFormat string `json:"list_format"`
}

type MainstreamModelEntry struct {
	EntryID         string                   `json:"entry_id"`
	UpstreamModelID string                   `json:"upstream_model_id"`
	NativeServices  []contract.NativeService `json:"native_services"`
}

type MainstreamModelMapping struct {
	ModelID string                 `json:"model_id"`
	Entries []MainstreamModelEntry `json:"entries"`
}

type MainstreamPreset struct {
	Entries         []MainstreamEntry          `json:"entries"`
	DiscoverySource *MainstreamDiscoveryPreset `json:"discovery_source"`
	ModelMappings   []MainstreamModelMapping   `json:"model_mappings"`
}

func scanMainstreamEntry(scanner pageScanner) (MainstreamEntry, error) {
	var item MainstreamEntry
	var id, mask int64
	var enabled int
	if err := scanner.Scan(&id, &item.Name, &item.ConnectorType, &item.BaseURL, &mask, &item.RerankFormat, &enabled); err != nil {
		return MainstreamEntry{}, err
	}
	services, err := contract.NativeServicesFromMask(mask)
	if id <= 0 || !validateFreeText(item.Name, 0, 128) || enabled < 0 || enabled > 1 || err != nil ||
		contract.ValidateNativeServices(contract.Type(item.ConnectorType), services, item.RerankFormat) != nil {
		return MainstreamEntry{}, ErrUnavailable
	}
	item.ID, item.Enabled, item.NativeServices = strconv.FormatInt(id, 10), enabled == 1, services.Services()
	return item, nil
}

func getMainstreamEntryTx(ctx context.Context, tx *sql.Tx, channelID string, entryID int64) (MainstreamEntry, error) {
	item, err := scanMainstreamEntry(tx.QueryRowContext(ctx, `SELECT id,name,connector_type,canonical_base_url,native_service_mask,rerank_format,enabled FROM mainstream_channel_entries WHERE channel_id=? AND id=?`, channelID, entryID))
	if errors.Is(err, sql.ErrNoRows) {
		return MainstreamEntry{}, ErrNotFound
	}
	return item, err
}

// Load all three small preset collections for the requested page in three
// queries, rather than multiplying queries by the number of channels.
func loadMainstreamPresetsTx(ctx context.Context, tx *sql.Tx, channelIDs []string) (map[string]MainstreamPreset, error) {
	result := make(map[string]MainstreamPreset, len(channelIDs))
	if len(channelIDs) == 0 {
		return result, nil
	}
	if len(channelIDs) > maxPageLimit+1 {
		return nil, ErrInvalidRequest
	}
	args := make([]any, len(channelIDs))
	for i, id := range channelIDs {
		args[i] = id
		result[id] = MainstreamPreset{Entries: []MainstreamEntry{}, ModelMappings: []MainstreamModelMapping{}}
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
	rows, err := tx.QueryContext(ctx, `SELECT channel_id,id,name,connector_type,canonical_base_url,native_service_mask,rerank_format,enabled FROM mainstream_channel_entries WHERE channel_id IN (`+placeholders+`) ORDER BY channel_id,ord,id`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var channelID string
		var item MainstreamEntry
		var id, mask int64
		var enabled int
		if err := rows.Scan(&channelID, &id, &item.Name, &item.ConnectorType, &item.BaseURL, &mask, &item.RerankFormat, &enabled); err != nil {
			rows.Close()
			return nil, err
		}
		services, err := contract.NativeServicesFromMask(mask)
		if id <= 0 || err != nil || contract.ValidateNativeServices(contract.Type(item.ConnectorType), services, item.RerankFormat) != nil || enabled < 0 || enabled > 1 {
			rows.Close()
			return nil, ErrUnavailable
		}
		item.ID, item.Enabled, item.NativeServices = strconv.FormatInt(id, 10), enabled == 1, services.Services()
		preset := result[channelID]
		preset.Entries = append(preset.Entries, item)
		result[channelID] = preset
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT channel_id,canonical_base_url,list_format FROM mainstream_channel_discovery_presets WHERE channel_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var channelID string
		var source MainstreamDiscoveryPreset
		if err := rows.Scan(&channelID, &source.BaseURL, &source.ListFormat); err != nil {
			rows.Close()
			return nil, err
		}
		preset := result[channelID]
		preset.DiscoverySource = &source
		result[channelID] = preset
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT channel_id,model_id,entry_id,upstream_model_id,native_service_mask FROM mainstream_channel_model_mappings WHERE channel_id IN (`+placeholders+`) ORDER BY channel_id,model_id COLLATE BINARY,ord,entry_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var channelID, modelID string
		var entry MainstreamModelEntry
		var entryID, mask int64
		if err := rows.Scan(&channelID, &modelID, &entryID, &entry.UpstreamModelID, &mask); err != nil {
			return nil, err
		}
		services, err := contract.NativeServicesFromMask(mask)
		if entryID <= 0 || err != nil || services.Empty() {
			return nil, ErrUnavailable
		}
		entry.EntryID, entry.NativeServices = strconv.FormatInt(entryID, 10), services.Services()
		preset := result[channelID]
		last := len(preset.ModelMappings) - 1
		if last < 0 || preset.ModelMappings[last].ModelID != modelID {
			preset.ModelMappings = append(preset.ModelMappings, MainstreamModelMapping{ModelID: modelID, Entries: []MainstreamModelEntry{}})
			last++
		}
		preset.ModelMappings[last].Entries = append(preset.ModelMappings[last].Entries, entry)
		result[channelID] = preset
	}
	return result, rows.Err()
}

func attachMainstreamPresetsTx(ctx context.Context, tx *sql.Tx, channels []MainstreamChannel) error {
	ids := make([]string, len(channels))
	for i, channel := range channels {
		ids[i] = channel.ID
	}
	presets, err := loadMainstreamPresetsTx(ctx, tx, ids)
	if err != nil {
		return err
	}
	for i := range channels {
		channels[i].MainstreamPreset = presets[channels[i].ID]
		if len(channels[i].Entries) == 0 {
			return ErrUnavailable
		}
	}
	return nil
}

func validDiscoveryListFormat(value string) bool {
	switch value {
	case "openai", "anthropic", "gemini", "ai-sdk-gateway-v3", "ai-sdk-gateway-v4":
		return true
	default:
		return false
	}
}

func (r *Repository) validateMainstreamPreset(preset MainstreamPreset) error {
	if len(preset.Entries) < 1 || len(preset.Entries) > 32 || len(preset.ModelMappings) > maxCatalogPairRows {
		return ErrInvalidRequest
	}
	entries := map[string]MainstreamEntry{}
	for _, entry := range preset.Entries {
		if !validateFreeText(entry.Name, 0, 128) || !validMainstreamConnectorType(r.connectors, entry.ConnectorType) {
			return ErrInvalidRequest
		}
		if entry.ID != "" {
			if _, err := parseDecimalID(entry.ID); err != nil {
				return ErrInvalidRequest
			}
			if _, duplicate := entries[entry.ID]; duplicate {
				return ErrInvalidRequest
			}
			entries[entry.ID] = entry
		}
		if _, err := endpointServiceSelection(contract.Type(entry.ConnectorType), &entry.NativeServices, entry.RerankFormat); err != nil {
			return err
		}
		if _, err := r.baseURLs.ValidateBaseURL(entry.BaseURL); err != nil {
			return ErrInvalidRequest
		}
	}
	if source := preset.DiscoverySource; source != nil {
		if !validDiscoveryListFormat(source.ListFormat) {
			return ErrInvalidRequest
		}
		if _, err := r.baseURLs.ValidateBaseURL(source.BaseURL); err != nil {
			return ErrInvalidRequest
		}
	}
	models := map[string]bool{}
	for _, mapping := range preset.ModelMappings {
		if !validateExactText(mapping.ModelID, 1, 512) || models[mapping.ModelID] || len(mapping.Entries) < 1 || len(mapping.Entries) > 32 {
			return ErrInvalidRequest
		}
		models[mapping.ModelID] = true
		seen := map[string]bool{}
		for _, target := range mapping.Entries {
			entry, exists := entries[target.EntryID]
			services, err := contract.ParseNativeServices(target.NativeServices)
			allowed, allowedErr := contract.ParseNativeServices(entry.NativeServices)
			if !exists || seen[target.EntryID] || !validateExactText(target.UpstreamModelID, 1, 512) || err != nil || allowedErr != nil || services.Empty() || !services.IsSubsetOf(allowed) {
				return ErrInvalidRequest
			}
			seen[target.EntryID] = true
		}
	}
	return nil
}

func (r *Repository) writeMainstreamPresetTx(ctx context.Context, tx *sql.Tx, channelID string, preset MainstreamPreset) error {
	if err := r.validateMainstreamPreset(preset); err != nil {
		return err
	}
	for _, entry := range preset.Entries {
		if entry.ID == "" {
			continue
		}
		id, _ := parseDecimalID(entry.ID)
		if _, err := getMainstreamEntryTx(ctx, tx, channelID, id); err != nil {
			return ErrConflict
		}
	}
	// Replacing an ordered preset in one transaction permits swaps under the
	// unique order constraint. Existing entry IDs and their suggestions survive.
	if _, err := tx.ExecContext(ctx, `DELETE FROM mainstream_channel_entries WHERE channel_id=?`, channelID); err != nil {
		return err
	}
	for ord, entry := range preset.Entries {
		var id any
		if entry.ID != "" {
			id, _ = parseDecimalID(entry.ID)
		}
		baseURL, err := r.baseURLs.ValidateBaseURL(entry.BaseURL)
		if err != nil {
			return ErrInvalidRequest
		}
		services, _ := contract.ParseNativeServices(entry.NativeServices)
		if _, err := tx.ExecContext(ctx, `INSERT INTO mainstream_channel_entries(id,channel_id,ord,name,connector_type,canonical_base_url,native_service_mask,rerank_format,enabled) VALUES(?,?,?,?,?,?,?,?,?)`, id, channelID, ord, entry.Name, entry.ConnectorType, baseURL, services.Mask(), entry.RerankFormat, boolInt(entry.Enabled)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM mainstream_channel_discovery_presets WHERE channel_id=?`, channelID); err != nil {
		return err
	}
	if source := preset.DiscoverySource; source != nil {
		baseURL, err := r.baseURLs.ValidateBaseURL(source.BaseURL)
		if err != nil {
			return ErrInvalidRequest
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO mainstream_channel_discovery_presets(channel_id,canonical_base_url,list_format) VALUES(?,?,?)`, channelID, baseURL, source.ListFormat); err != nil {
			return err
		}
	}
	for _, mapping := range preset.ModelMappings {
		for ord, target := range mapping.Entries {
			id, _ := parseDecimalID(target.EntryID)
			services, _ := contract.ParseNativeServices(target.NativeServices)
			if _, err := tx.ExecContext(ctx, `INSERT INTO mainstream_channel_model_mappings(channel_id,model_id,entry_id,upstream_model_id,native_service_mask,ord) VALUES(?,?,?,?,?,?)`, channelID, mapping.ModelID, id, target.UpstreamModelID, services.Mask(), ord); err != nil {
				return err
			}
		}
	}
	return nil
}
