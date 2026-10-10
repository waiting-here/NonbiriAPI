ALTER TABLE request_adaptations RENAME TO request_adaptations_previous;
CREATE TABLE request_adaptations (
 id INTEGER PRIMARY KEY,
 scope TEXT NOT NULL CHECK(scope IN ('endpoint','personal_model','charity_model','binding')),
 endpoint_id INTEGER UNIQUE REFERENCES endpoints(id) ON DELETE CASCADE,
 personal_model_id INTEGER UNIQUE REFERENCES models(id) ON DELETE CASCADE,
 model_id INTEGER UNIQUE REFERENCES charity_models(id) ON DELETE CASCADE,
 binding_id INTEGER UNIQUE REFERENCES charity_model_bindings(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 secret_context BLOB NOT NULL CHECK(length(secret_context)=16),
 secret_ciphertext TEXT NOT NULL CHECK(length(CAST(secret_ciphertext AS BLOB)) BETWEEN 1 AND 131072),
 structure_json TEXT NOT NULL CHECK(json_valid(structure_json) AND json_type(structure_json)='object' AND length(CAST(structure_json AS BLOB))<=65536),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799),
 CHECK((scope='endpoint' AND personal_model_id IS NULL AND endpoint_id IS NOT NULL AND model_id IS NULL AND binding_id IS NULL)
 OR (scope='charity_model' AND personal_model_id IS NULL AND endpoint_id IS NULL AND model_id IS NOT NULL AND binding_id IS NULL)
 OR (scope='binding' AND personal_model_id IS NULL AND endpoint_id IS NULL AND model_id IS NULL AND binding_id IS NOT NULL)
 OR (scope='personal_model' AND personal_model_id IS NOT NULL AND endpoint_id IS NULL AND model_id IS NULL AND binding_id IS NULL))
) STRICT;
INSERT INTO request_adaptations(id,scope,endpoint_id,model_id,binding_id,revision,secret_context,secret_ciphertext,structure_json,actor_user_id,updated_at) SELECT id,scope,endpoint_id,model_id,binding_id,revision,secret_context,secret_ciphertext,structure_json,actor_user_id,updated_at FROM request_adaptations_previous;
DROP TABLE request_adaptations_previous;
CREATE TRIGGER request_adaptation_identity BEFORE UPDATE ON request_adaptations
WHEN NEW.personal_model_id IS NOT OLD.personal_model_id OR NEW.id<>OLD.id OR NEW.scope<>OLD.scope OR NEW.endpoint_id IS NOT OLD.endpoint_id
 OR NEW.model_id IS NOT OLD.model_id OR NEW.binding_id IS NOT OLD.binding_id
 OR NEW.secret_context<>OLD.secret_context OR NEW.revision<OLD.revision
 OR (NEW.revision=OLD.revision AND (NEW.secret_ciphertext<>OLD.secret_ciphertext OR NEW.structure_json<>OLD.structure_json OR NEW.updated_at<>OLD.updated_at))
BEGIN SELECT RAISE(ABORT,'invalid request adaptation revision'); END;
ALTER TABLE request_adaptation_audits RENAME TO request_adaptation_audits_previous;
CREATE TABLE request_adaptation_audits (
 id INTEGER PRIMARY KEY,
 scope TEXT NOT NULL CHECK(scope IN ('endpoint','personal_model','charity_model','binding')),
 resource_id INTEGER NOT NULL CHECK(resource_id>0),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 actor_role TEXT NOT NULL CHECK(actor_role IN ('owner','admin')),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 changed_partitions TEXT NOT NULL CHECK(json_valid(changed_partitions) AND json_type(changed_partitions)='array' AND json_array_length(changed_partitions)<=6 AND length(CAST(changed_partitions AS BLOB))<=256),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 UNIQUE(scope,resource_id,revision),
 CHECK((scope IN ('endpoint','personal_model'))=(actor_role='owner'))
) STRICT;
INSERT INTO request_adaptation_audits(id,scope,resource_id,actor_user_id,actor_role,revision,changed_partitions,created_at) SELECT id,scope,resource_id,actor_user_id,actor_role,revision,changed_partitions,created_at FROM request_adaptation_audits_previous;
DROP TABLE request_adaptation_audits_previous;
CREATE INDEX idx_request_adaptation_audits_expiry ON request_adaptation_audits(created_at,id);
CREATE INDEX idx_request_adaptation_audits_actor ON request_adaptation_audits(actor_user_id,id);
CREATE TRIGGER request_adaptation_audit_partitions BEFORE INSERT ON request_adaptation_audits
WHEN EXISTS(SELECT 1 FROM json_each(NEW.changed_partitions) WHERE type<>'text' OR value NOT IN ('forward_headers','fixed_headers','body_defaults','body_forced','native_extension_paths','excluded_request_fields'))
 OR json_array_length(NEW.changed_partitions)<>(SELECT count(DISTINCT value) FROM json_each(NEW.changed_partitions))
BEGIN SELECT RAISE(ABORT,'invalid request adaptation audit partitions'); END;
CREATE TRIGGER request_adaptation_audit_immutable BEFORE UPDATE ON request_adaptation_audits
WHEN NOT (OLD.actor_user_id IS NOT NULL AND NEW.actor_user_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM users WHERE id=OLD.actor_user_id)
 AND NEW.id=OLD.id AND NEW.scope=OLD.scope AND NEW.resource_id=OLD.resource_id
 AND NEW.actor_role=OLD.actor_role AND NEW.revision=OLD.revision
 AND NEW.changed_partitions=OLD.changed_partitions AND NEW.created_at=OLD.created_at)
BEGIN SELECT RAISE(ABORT,'request adaptation audit is immutable'); END;
