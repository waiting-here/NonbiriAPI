DROP TABLE personal_key_affinities;
DROP TRIGGER charity_dispatch_receipt_identity;
ALTER TABLE charity_dispatch_receipts DROP COLUMN personal_model_id;
CREATE TRIGGER charity_dispatch_receipt_identity BEFORE UPDATE ON charity_dispatch_receipts
WHEN NEW.attempt_id<>OLD.attempt_id OR NEW.physical_id<>OLD.physical_id OR NEW.endpoint_key_id<>OLD.endpoint_key_id
 OR NEW.routing_revision<>OLD.routing_revision OR NEW.reserved_at<>OLD.reserved_at
 OR (OLD.state='dispatched' AND (NEW.state<>OLD.state OR NEW.dispatched_at<>OLD.dispatched_at))
 OR (NEW.user_id IS NOT OLD.user_id AND NEW.user_id IS NOT NULL)
 OR (NEW.model_id IS NOT OLD.model_id AND NEW.model_id IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'dispatch receipt is immutable'); END;
DROP TABLE models;
CREATE TABLE models (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 provider TEXT NOT NULL,
 model TEXT NOT NULL,
 full_name TEXT NOT NULL,
 route_strategy TEXT NOT NULL DEFAULT 'ordered' CHECK(route_strategy IN ('ordered','random')),
 silent_retry INTEGER NOT NULL DEFAULT 0 CHECK(silent_retry IN (0,1)),
 flatten_tool_calls INTEGER NOT NULL DEFAULT 0 CHECK(flatten_tool_calls IN (0,1)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9223372036854775807),
 binding_revision INTEGER NOT NULL DEFAULT 0 CHECK(binding_revision BETWEEN 0 AND 9223372036854775807),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL, role_policy TEXT NOT NULL DEFAULT '{"default_action":"native","rules":{}}' CHECK(typeof(role_policy)='text' AND length(CAST(role_policy AS BLOB))<=8192 AND json_valid(role_policy) AND json_type(role_policy)='object' AND COALESCE(json_type(role_policy,'$.default_action'),'')='text' AND json_extract(role_policy,'$.default_action') IN ('native','passthrough','system','user','assistant','reject') AND COALESCE(json_type(role_policy,'$.rules'),'')='object'), transport_rule TEXT NOT NULL DEFAULT 'passthrough' CHECK(transport_rule IN ('passthrough','force_non_stream','force_stream')), model_types INTEGER NOT NULL DEFAULT 1 CHECK(typeof(model_types)='integer' AND model_types BETWEEN 1 AND 7),
 UNIQUE(user_id,full_name), UNIQUE(id,user_id), CHECK(full_name=provider||'/'||model)
);
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
UPDATE schema_state SET version=7 WHERE id=1;
