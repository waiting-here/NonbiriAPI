package v1

const channelBindingPresetsSchema = `CREATE TABLE channel_charity_binding_presets (
 channel_id TEXT PRIMARY KEY REFERENCES mainstream_channels(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision>=1)
) STRICT;
CREATE TABLE channel_charity_binding_mappings (
 channel_id TEXT NOT NULL REFERENCES channel_charity_binding_presets(channel_id) ON DELETE CASCADE,
 ord INTEGER NOT NULL CHECK(ord BETWEEN 0 AND 15),
 upstream_model_id TEXT NOT NULL CHECK(length(upstream_model_id) BETWEEN 1 AND 512 AND instr(upstream_model_id,char(0))=0),
 charity_model_id INTEGER NOT NULL CHECK(charity_model_id>0),
 PRIMARY KEY(channel_id,ord),
 UNIQUE(channel_id,upstream_model_id,charity_model_id)
) STRICT;
`
