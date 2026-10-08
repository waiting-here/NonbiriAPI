-- Preserve personal models, bindings, and the monotonically increasing model ID.
CREATE TEMP TABLE personal_routing_model_sequence AS SELECT seq FROM sqlite_sequence WHERE name='models';
CREATE TEMP TABLE personal_routing_models AS SELECT * FROM models;
DROP TABLE models;
CREATE TABLE models (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 provider TEXT NOT NULL,
 model TEXT NOT NULL,
 full_name TEXT NOT NULL,
 route_strategy TEXT NOT NULL DEFAULT 'ordered' CHECK(route_strategy IN ('ordered','random','cache_balanced')),
 silent_retry INTEGER NOT NULL DEFAULT 0 CHECK(silent_retry IN (0,1)),
 flatten_tool_calls INTEGER NOT NULL DEFAULT 0 CHECK(flatten_tool_calls IN (0,1)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9223372036854775807),
 binding_revision INTEGER NOT NULL DEFAULT 0 CHECK(binding_revision BETWEEN 0 AND 9223372036854775807),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL, role_policy TEXT NOT NULL DEFAULT '{"default_action":"native","rules":{}}' CHECK(typeof(role_policy)='text' AND length(CAST(role_policy AS BLOB))<=8192 AND json_valid(role_policy) AND json_type(role_policy)='object' AND COALESCE(json_type(role_policy,'$.default_action'),'')='text' AND json_extract(role_policy,'$.default_action') IN ('native','passthrough','system','user','assistant','reject') AND COALESCE(json_type(role_policy,'$.rules'),'')='object'), transport_rule TEXT NOT NULL DEFAULT 'passthrough' CHECK(transport_rule IN ('passthrough','force_non_stream','force_stream')), model_types INTEGER NOT NULL DEFAULT 1 CHECK(typeof(model_types)='integer' AND model_types BETWEEN 1 AND 7),
 UNIQUE(user_id,full_name), UNIQUE(id,user_id), CHECK(full_name=provider||'/'||model)
);
INSERT INTO models(id,user_id,provider,model,full_name,route_strategy,silent_retry,flatten_tool_calls,revision,binding_revision,created_at,updated_at,role_policy,transport_rule,model_types) SELECT id,user_id,provider,model,full_name,route_strategy,silent_retry,flatten_tool_calls,revision,binding_revision,created_at,updated_at,role_policy,transport_rule,model_types FROM personal_routing_models;
DROP TABLE personal_routing_models;
DELETE FROM sqlite_sequence WHERE name='models';
INSERT INTO sqlite_sequence(name,seq) SELECT 'models',seq FROM personal_routing_model_sequence;
DROP TABLE personal_routing_model_sequence;
CREATE INDEX idx_models_user ON models(user_id);
CREATE INDEX idx_models_user_fullname ON models(user_id,full_name);
CREATE INDEX idx_models_user_updated ON models(user_id,updated_at,id);
CREATE TRIGGER generation_two_integer_type_models_insert_guard BEFORE INSERT ON models
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.silent_retry IS NOT NULL AND typeof(NEW.silent_retry)<>'integer')
 OR (NEW.flatten_tool_calls IS NOT NULL AND typeof(NEW.flatten_tool_calls)<>'integer')
 OR (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.binding_revision IS NOT NULL AND typeof(NEW.binding_revision)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_models_update_guard BEFORE UPDATE ON models
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.silent_retry IS NOT NULL AND typeof(NEW.silent_retry)<>'integer')
 OR (NEW.flatten_tool_calls IS NOT NULL AND typeof(NEW.flatten_tool_calls)<>'integer')
 OR (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.binding_revision IS NOT NULL AND typeof(NEW.binding_revision)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_models_time_guard BEFORE INSERT ON models
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'model timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_models_time_update_guard BEFORE UPDATE ON models
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'model timestamp is outside UTC range'); END;
CREATE TRIGGER models_role_policy_insert_guard BEFORE INSERT ON models
WHEN (SELECT count(*) FROM json_each(NEW.role_policy))<>2
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy) WHERE key NOT IN ('default_action','rules'))
 OR (SELECT count(*) FROM json_each(NEW.role_policy,'$.rules'))>32
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy,'$.rules') WHERE type<>'text'
  OR value NOT IN ('native','passthrough','system','user','assistant','reject')
  OR length(key) NOT BETWEEN 1 AND 64 OR length(CAST(key AS BLOB))>256
  OR key IN ('system','assistant','user','tool','function') OR key<>trim(key)
  OR instr(key,char(0))>0 OR key GLOB '*['||char(1)||'-'||char(31)||char(127)||']*')
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy) GROUP BY key HAVING count(*)>1)
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy,'$.rules') GROUP BY key HAVING count(*)>1)
BEGIN SELECT RAISE(ABORT,'invalid model role policy'); END;
CREATE TRIGGER models_role_policy_update_guard BEFORE UPDATE OF role_policy ON models
WHEN (SELECT count(*) FROM json_each(NEW.role_policy))<>2
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy) WHERE key NOT IN ('default_action','rules'))
 OR (SELECT count(*) FROM json_each(NEW.role_policy,'$.rules'))>32
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy,'$.rules') WHERE type<>'text'
  OR value NOT IN ('native','passthrough','system','user','assistant','reject')
  OR length(key) NOT BETWEEN 1 AND 64 OR length(CAST(key AS BLOB))>256
  OR key IN ('system','assistant','user','tool','function') OR key<>trim(key)
  OR instr(key,char(0))>0 OR key GLOB '*['||char(1)||'-'||char(31)||char(127)||']*')
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy) GROUP BY key HAVING count(*)>1)
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy,'$.rules') GROUP BY key HAVING count(*)>1)
BEGIN SELECT RAISE(ABORT,'invalid model role policy'); END;

CREATE TABLE personal_key_affinities (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 model_id INTEGER NOT NULL,
 endpoint_key_id INTEGER NOT NULL REFERENCES endpoint_keys(id) ON DELETE CASCADE,
 physical_id BLOB NOT NULL CHECK(length(physical_id)=32),
 attempt_id TEXT NOT NULL CHECK(length(attempt_id)=26 AND substr(attempt_id,1,4)='clm_' AND substr(attempt_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(attempt_id,-1,1) IN ('A','Q','g','w')),
 routing_revision INTEGER NOT NULL CHECK(routing_revision>0),
 dispatched_at INTEGER NOT NULL CHECK(dispatched_at BETWEEN 0 AND 253402214399),
 expires_at INTEGER NOT NULL CHECK(expires_at=dispatched_at+300),
 PRIMARY KEY(user_id,model_id),
 FOREIGN KEY(model_id,user_id) REFERENCES models(id,user_id) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_personal_affinities_expiry ON personal_key_affinities(expires_at,user_id,model_id);
CREATE INDEX idx_personal_affinities_key ON personal_key_affinities(endpoint_key_id,physical_id);
CREATE TRIGGER personal_affinity_capacity BEFORE INSERT ON personal_key_affinities
WHEN NOT EXISTS(SELECT 1 FROM personal_key_affinities WHERE user_id=NEW.user_id AND model_id=NEW.model_id)
 AND ((SELECT affinities FROM charity_routing_capacity WHERE id=1)>=200000
 OR (SELECT count(*) FROM personal_key_affinities WHERE user_id=NEW.user_id)>=1000)
BEGIN SELECT RAISE(ABORT,'personal association capacity exhausted'); END;
CREATE TRIGGER personal_affinity_count_insert AFTER INSERT ON personal_key_affinities
BEGIN UPDATE charity_routing_capacity SET affinities=affinities+1,revision=revision+1 WHERE id=1; END;
CREATE TRIGGER personal_affinity_count_delete AFTER DELETE ON personal_key_affinities
BEGIN UPDATE charity_routing_capacity SET affinities=affinities-1,revision=revision+1 WHERE id=1; END;
CREATE TRIGGER personal_affinity_identity BEFORE UPDATE ON personal_key_affinities
WHEN NEW.user_id<>OLD.user_id OR NEW.model_id<>OLD.model_id
BEGIN SELECT RAISE(ABORT,'personal association identity is immutable'); END;
CREATE TRIGGER personal_affinity_owner_insert BEFORE INSERT ON personal_key_affinities
WHEN NOT EXISTS(SELECT 1 FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE k.id=NEW.endpoint_key_id AND e.user_id=NEW.user_id)
BEGIN SELECT RAISE(ABORT,'personal association owner mismatch'); END;
CREATE TRIGGER personal_affinity_owner_update BEFORE UPDATE OF endpoint_key_id ON personal_key_affinities
WHEN NOT EXISTS(SELECT 1 FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE k.id=NEW.endpoint_key_id AND e.user_id=NEW.user_id)
BEGIN SELECT RAISE(ABORT,'personal association owner mismatch'); END;

DROP TRIGGER charity_dispatch_receipt_identity;
ALTER TABLE charity_dispatch_receipts ADD COLUMN personal_model_id INTEGER REFERENCES models(id) ON DELETE SET NULL CHECK(personal_model_id IS NULL OR model_id IS NULL);
CREATE TRIGGER charity_dispatch_receipt_identity BEFORE UPDATE ON charity_dispatch_receipts
WHEN NEW.attempt_id<>OLD.attempt_id OR NEW.physical_id<>OLD.physical_id OR NEW.endpoint_key_id<>OLD.endpoint_key_id
 OR NEW.routing_revision<>OLD.routing_revision OR NEW.reserved_at<>OLD.reserved_at
 OR (OLD.state='dispatched' AND (NEW.state<>OLD.state OR NEW.dispatched_at<>OLD.dispatched_at))
 OR (NEW.user_id IS NOT OLD.user_id AND NEW.user_id IS NOT NULL)
 OR (NEW.model_id IS NOT OLD.model_id AND NEW.model_id IS NOT NULL)
 OR (NEW.personal_model_id IS NOT OLD.personal_model_id AND NEW.personal_model_id IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'dispatch receipt is immutable'); END;
