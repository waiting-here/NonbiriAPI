package db

import "strings"

const imageCapabilitySchema = `
CREATE TABLE image_capability_profiles (
 control_id TEXT PRIMARY KEY REFERENCES image_upstream_control(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision>0),
 profile_json TEXT NOT NULL CHECK(json_valid(profile_json) AND json_type(profile_json)='object' AND length(CAST(profile_json AS BLOB))<=262144),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TABLE image_capability_snapshots (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='ics_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 control_id TEXT NOT NULL REFERENCES image_upstream_control(id) ON DELETE CASCADE,
 upstream_revision INTEGER NOT NULL REFERENCES image_upstream_revisions(revision),
 profile_revision INTEGER NOT NULL CHECK(profile_revision>=0),
 candidate_hash BLOB NOT NULL CHECK(length(candidate_hash)=32),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402214399),
 expires_at INTEGER NOT NULL CHECK(expires_at=created_at+86400)
) STRICT;
CREATE INDEX idx_image_capability_snapshots_latest ON image_capability_snapshots(control_id,created_at DESC,id);
CREATE INDEX idx_image_capability_snapshots_expiry ON image_capability_snapshots(expires_at,id);
CREATE TRIGGER image_capability_snapshot_capacity BEFORE INSERT ON image_capability_snapshots
WHEN (SELECT count(*) FROM image_capability_snapshots WHERE control_id=NEW.control_id)>=2
BEGIN SELECT RAISE(ABORT,'image capability snapshot capacity exhausted'); END;
CREATE TABLE image_capability_candidates (
 snapshot_id TEXT NOT NULL REFERENCES image_capability_snapshots(id) ON DELETE CASCADE,
 model_id TEXT NOT NULL REFERENCES image_activity_models(id) ON DELETE CASCADE,
 source_json TEXT NOT NULL CHECK(json_valid(source_json) AND json_type(source_json)='object' AND length(CAST(source_json AS BLOB))<=262144),
 metadata_json TEXT NOT NULL CHECK(json_valid(metadata_json) AND json_type(metadata_json)='object' AND length(CAST(metadata_json AS BLOB))<=32768),
 candidate_hash BLOB NOT NULL CHECK(length(candidate_hash)=32),
 PRIMARY KEY(snapshot_id,model_id)
) STRICT, WITHOUT ROWID;
CREATE TABLE image_model_capability_revisions (
 model_id TEXT NOT NULL REFERENCES image_activity_models(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision>0),
 schema_version INTEGER NOT NULL CHECK(schema_version IN (1,2)),
 readiness TEXT NOT NULL CHECK(readiness IN ('legacy','pending','ready')),
 source_json TEXT NOT NULL CHECK(json_valid(source_json) AND json_type(source_json)='object' AND length(CAST(source_json AS BLOB))<=262144),
 manual_json TEXT NOT NULL CHECK(json_valid(manual_json) AND json_type(manual_json)='object' AND length(CAST(manual_json AS BLOB))<=262144),
 effective_json TEXT NOT NULL CHECK(json_valid(effective_json) AND json_type(effective_json)='object' AND length(CAST(effective_json AS BLOB))<=262144),
 candidate_hash BLOB CHECK(candidate_hash IS NULL OR length(candidate_hash)=32),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(model_id,revision),
 CHECK((schema_version=1)=(readiness='legacy'))
) STRICT;
CREATE TRIGGER image_model_capability_immutable BEFORE UPDATE ON image_model_capability_revisions
BEGIN SELECT RAISE(ABORT,'image capability revision is immutable'); END;
CREATE TABLE image_model_pricing_revisions (
 model_id TEXT NOT NULL REFERENCES image_activity_models(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision>0),
 default_paper_mag BLOB NOT NULL CHECK(length(default_paper_mag)=16 AND default_paper_mag<X'80000000000000000000000000000000'),
 default_brush_mag BLOB NOT NULL CHECK(length(default_brush_mag)=16 AND default_brush_mag<X'80000000000000000000000000000000'),
 fallback TEXT NOT NULL CHECK(fallback IN ('default','unavailable')),
 tiers_json TEXT NOT NULL CHECK(json_valid(tiers_json) AND json_type(tiers_json)='array' AND json_array_length(tiers_json)<=64 AND length(CAST(tiers_json AS BLOB))<=16384),
 sizes_json TEXT NOT NULL CHECK(json_valid(sizes_json) AND json_type(sizes_json)='array' AND json_array_length(sizes_json)<=2048 AND length(CAST(sizes_json AS BLOB))<=262144),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(model_id,revision),
 CHECK(default_paper_mag<>zeroblob(16) OR default_brush_mag<>zeroblob(16))
) STRICT;
CREATE TRIGGER image_model_pricing_immutable BEFORE UPDATE ON image_model_pricing_revisions
BEGIN SELECT RAISE(ABORT,'image pricing revision is immutable'); END;
CREATE TABLE image_model_revision_policies (
 model_id TEXT NOT NULL,
 model_revision INTEGER NOT NULL,
 capability_revision INTEGER NOT NULL,
 pricing_revision INTEGER NOT NULL,
 PRIMARY KEY(model_id,model_revision),
 FOREIGN KEY(model_id,model_revision) REFERENCES image_model_revisions(model_id,revision) ON DELETE CASCADE,
 FOREIGN KEY(model_id,capability_revision) REFERENCES image_model_capability_revisions(model_id,revision),
 FOREIGN KEY(model_id,pricing_revision) REFERENCES image_model_pricing_revisions(model_id,revision)
) STRICT;
CREATE TRIGGER image_model_policy_immutable BEFORE UPDATE ON image_model_revision_policies
BEGIN SELECT RAISE(ABORT,'image revision policy is immutable'); END;
CREATE TABLE image_task_price_receipts (
 task_id TEXT PRIMARY KEY REFERENCES image_activity_tasks(id) ON DELETE CASCADE,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 model_revision INTEGER NOT NULL CHECK(model_revision>0),
 pricing_revision INTEGER NOT NULL CHECK(pricing_revision>0),
 n INTEGER NOT NULL CHECK(n BETWEEN 1 AND 16),
 unit_paper_mag BLOB NOT NULL CHECK(length(unit_paper_mag)=16 AND unit_paper_mag<X'80000000000000000000000000000000'),
 unit_brush_mag BLOB NOT NULL CHECK(length(unit_brush_mag)=16 AND unit_brush_mag<X'80000000000000000000000000000000'),
 total_paper_mag BLOB NOT NULL CHECK(length(total_paper_mag)=16 AND total_paper_mag<X'80000000000000000000000000000000'),
 total_brush_mag BLOB NOT NULL CHECK(length(total_brush_mag)=16 AND total_brush_mag<X'80000000000000000000000000000000'),
 basis TEXT NOT NULL CHECK(basis IN ('legacy','default','tier','size','auto')),
 price_key TEXT NOT NULL CHECK(length(CAST(price_key AS BLOB))<=128),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TRIGGER image_task_price_receipt_immutable BEFORE UPDATE ON image_task_price_receipts
BEGIN SELECT RAISE(ABORT,'image accepted price is immutable'); END;
`

func imageCapabilityGuardsSchema() string {
	var b strings.Builder
	for _, table := range []struct {
		name    string
		columns []string
	}{
		{"image_model_pricing_revisions", []string{"default_paper_mag", "default_brush_mag"}},
		{"image_task_price_receipts", []string{"unit_paper_mag", "unit_brush_mag", "total_paper_mag", "total_brush_mag"}},
	} {
		b.WriteString("CREATE TRIGGER " + table.name + "_whole_insert BEFORE INSERT ON " + table.name + " WHEN ")
		for i, column := range table.columns {
			if i > 0 {
				b.WriteString(" OR ")
			}
			b.WriteString("NOT " + governanceWholeAsset("NEW."+column))
		}
		b.WriteString(" BEGIN SELECT RAISE(ABORT,'image price is not an integer'); END;\n")
	}
	return b.String()
}
