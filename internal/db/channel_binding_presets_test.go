package db

import "testing"

func TestChannelBindingPresetConstraintsAndDeletion(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	defer database.Close()
	id, err := GenerateOpaqueID("mch_")
	if err != nil {
		t.Fatal(err)
	}
	hostileMustExec(t, database, `INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at) VALUES(?,'test','subscription','openai-compatible','https://example.test/v1',1,'active',1,0,0)`, id)
	hostileMustExec(t, database, `INSERT INTO channel_charity_binding_presets(channel_id,revision) VALUES(?,1)`, id)
	hostileMustExec(t, database, `INSERT INTO channel_charity_binding_mappings(channel_id,ord,upstream_model_id,charity_model_id) VALUES(?,0,'exact',999)`, id)
	hostileMustFail(t, database, `INSERT INTO channel_charity_binding_mappings(channel_id,ord,upstream_model_id,charity_model_id) VALUES(?,16,'overflow',999)`, id)
	hostileMustFail(t, database, `INSERT INTO channel_charity_binding_mappings(channel_id,ord,upstream_model_id,charity_model_id) VALUES(?,1,'exact',999)`, id)
	hostileMustFail(t, database, `UPDATE channel_charity_binding_presets SET revision=0 WHERE channel_id=?`, id)
	hostileMustExec(t, database, `DELETE FROM mainstream_channels WHERE id=?`, id)
	var count int
	if err := database.QueryRow(`SELECT (SELECT COUNT(*) FROM channel_charity_binding_presets)+(SELECT COUNT(*) FROM channel_charity_binding_mappings)`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
