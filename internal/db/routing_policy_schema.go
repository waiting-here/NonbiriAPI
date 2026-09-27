package db

const routingPolicySchema = `
CREATE TABLE charity_routing_settings (
 model_id INTEGER PRIMARY KEY REFERENCES charity_models(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 affinity_ttl_seconds INTEGER NOT NULL DEFAULT 300 CHECK(affinity_ttl_seconds BETWEEN 1 AND 86400)
) STRICT;
CREATE TABLE charity_routing_capacity (
 id INTEGER PRIMARY KEY CHECK(id=1),
 affinities INTEGER NOT NULL CHECK(affinities BETWEEN 0 AND 200000),
 buckets INTEGER NOT NULL CHECK(buckets BETWEEN 0 AND 1000000),
 revision INTEGER NOT NULL CHECK(revision>0)
) STRICT;
CREATE TABLE charity_key_affinities (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 model_id INTEGER NOT NULL REFERENCES charity_models(id) ON DELETE CASCADE,
 endpoint_key_id INTEGER NOT NULL REFERENCES endpoint_keys(id) ON DELETE CASCADE,
 physical_id BLOB NOT NULL CHECK(length(physical_id)=32),
 attempt_id TEXT NOT NULL CHECK(length(attempt_id)=26 AND substr(attempt_id,1,4)='clm_' AND substr(attempt_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(attempt_id,-1,1) IN ('A','Q','g','w')),
 routing_revision INTEGER NOT NULL CHECK(routing_revision>0),
 dispatched_at INTEGER NOT NULL CHECK(dispatched_at BETWEEN 0 AND 253402214399),
 expires_at INTEGER NOT NULL CHECK(expires_at>dispatched_at AND expires_at<=dispatched_at+86400),
 PRIMARY KEY(user_id,model_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_charity_affinities_expiry ON charity_key_affinities(expires_at,user_id,model_id);
CREATE INDEX idx_charity_affinities_key ON charity_key_affinities(endpoint_key_id,physical_id);
CREATE TRIGGER charity_affinity_capacity BEFORE INSERT ON charity_key_affinities
WHEN NOT EXISTS(SELECT 1 FROM charity_key_affinities WHERE user_id=NEW.user_id AND model_id=NEW.model_id)
 AND ((SELECT affinities FROM charity_routing_capacity WHERE id=1)>=200000
 OR (SELECT count(*) FROM charity_key_affinities WHERE user_id=NEW.user_id)>=1000)
BEGIN SELECT RAISE(ABORT,'charity association capacity exhausted'); END;
CREATE TRIGGER charity_affinity_count_insert AFTER INSERT ON charity_key_affinities
BEGIN UPDATE charity_routing_capacity SET affinities=affinities+1,revision=revision+1 WHERE id=1; END;
CREATE TRIGGER charity_affinity_count_delete AFTER DELETE ON charity_key_affinities
BEGIN UPDATE charity_routing_capacity SET affinities=affinities-1,revision=revision+1 WHERE id=1; END;
CREATE TRIGGER charity_affinity_identity BEFORE UPDATE ON charity_key_affinities
WHEN NEW.user_id<>OLD.user_id OR NEW.model_id<>OLD.model_id
BEGIN SELECT RAISE(ABORT,'charity association identity is immutable'); END;
CREATE TABLE charity_dispatch_buckets (
 physical_id BLOB NOT NULL CHECK(length(physical_id)=32),
 dispatched_at INTEGER NOT NULL CHECK(dispatched_at BETWEEN 0 AND 253402300499),
 dispatch_count INTEGER NOT NULL CHECK(dispatch_count BETWEEN 1 AND 9223372036854775807),
 PRIMARY KEY(physical_id,dispatched_at)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_charity_dispatch_buckets_expiry ON charity_dispatch_buckets(dispatched_at,physical_id);
CREATE TRIGGER charity_bucket_capacity BEFORE INSERT ON charity_dispatch_buckets
WHEN NOT EXISTS(SELECT 1 FROM charity_dispatch_buckets WHERE physical_id=NEW.physical_id AND dispatched_at=NEW.dispatched_at)
 AND (SELECT buckets FROM charity_routing_capacity WHERE id=1)>=1000000
BEGIN SELECT RAISE(ABORT,'charity dispatch capacity exhausted'); END;
CREATE TRIGGER charity_bucket_count_insert AFTER INSERT ON charity_dispatch_buckets
BEGIN UPDATE charity_routing_capacity SET buckets=buckets+1,revision=revision+1 WHERE id=1; END;
CREATE TRIGGER charity_bucket_count_delete AFTER DELETE ON charity_dispatch_buckets
BEGIN UPDATE charity_routing_capacity SET buckets=buckets-1,revision=revision+1 WHERE id=1; END;
CREATE TABLE charity_dispatch_receipts (
 attempt_id TEXT PRIMARY KEY CHECK(length(attempt_id)=26 AND substr(attempt_id,1,4)='clm_' AND substr(attempt_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(attempt_id,-1,1) IN ('A','Q','g','w')),
 physical_id BLOB NOT NULL CHECK(length(physical_id)=32),
 endpoint_key_id INTEGER NOT NULL CHECK(endpoint_key_id>0),
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 model_id INTEGER REFERENCES charity_models(id) ON DELETE SET NULL,
 routing_revision INTEGER NOT NULL CHECK(routing_revision>0),
 state TEXT NOT NULL CHECK(state IN ('reserved','dispatched')),
 reserved_at INTEGER NOT NULL CHECK(reserved_at BETWEEN 0 AND 253402300499),
 dispatched_at INTEGER CHECK(dispatched_at BETWEEN reserved_at AND 253402300499),
 expires_at INTEGER NOT NULL CHECK(expires_at BETWEEN reserved_at AND 253402300799),
 previous_attempt_id TEXT CHECK(previous_attempt_id IS NULL OR (length(previous_attempt_id)=26 AND substr(previous_attempt_id,1,4)='clm_' AND substr(previous_attempt_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(previous_attempt_id,-1,1) IN ('A','Q','g','w'))),
 previous_endpoint_key_id INTEGER CHECK(previous_endpoint_key_id>0),
 previous_physical_id BLOB CHECK(previous_physical_id IS NULL OR length(previous_physical_id)=32),
 previous_routing_revision INTEGER CHECK(previous_routing_revision>0),
 previous_dispatched_at INTEGER CHECK(previous_dispatched_at BETWEEN 0 AND 253402214399),
 previous_expires_at INTEGER CHECK(previous_expires_at>previous_dispatched_at AND previous_expires_at<=previous_dispatched_at+86400),
 CHECK((state='reserved' AND dispatched_at IS NULL) OR (state='dispatched' AND dispatched_at IS NOT NULL AND expires_at>=dispatched_at+300)),
 CHECK((previous_attempt_id IS NULL AND previous_endpoint_key_id IS NULL AND previous_physical_id IS NULL AND previous_routing_revision IS NULL AND previous_dispatched_at IS NULL AND previous_expires_at IS NULL)
 OR (state='dispatched' AND previous_attempt_id IS NOT NULL AND previous_attempt_id<>attempt_id AND previous_endpoint_key_id IS NOT NULL AND previous_physical_id IS NOT NULL AND previous_routing_revision IS NOT NULL AND previous_dispatched_at IS NOT NULL AND previous_expires_at IS NOT NULL AND previous_dispatched_at<=dispatched_at))
) STRICT;
CREATE INDEX idx_charity_dispatch_receipts_expiry ON charity_dispatch_receipts(expires_at,attempt_id);
CREATE INDEX idx_charity_dispatch_receipts_pending ON charity_dispatch_receipts(physical_id,state,reserved_at);
CREATE UNIQUE INDEX idx_charity_dispatch_receipts_previous ON charity_dispatch_receipts(previous_attempt_id) WHERE previous_attempt_id IS NOT NULL;
CREATE TRIGGER charity_dispatch_receipt_identity BEFORE UPDATE ON charity_dispatch_receipts
WHEN NEW.attempt_id<>OLD.attempt_id OR NEW.physical_id<>OLD.physical_id OR NEW.endpoint_key_id<>OLD.endpoint_key_id
 OR NEW.routing_revision<>OLD.routing_revision OR NEW.reserved_at<>OLD.reserved_at
 OR (OLD.state='dispatched' AND (NEW.state<>OLD.state OR NEW.dispatched_at<>OLD.dispatched_at))
 OR (NEW.user_id IS NOT OLD.user_id AND NEW.user_id IS NOT NULL)
 OR (NEW.model_id IS NOT OLD.model_id AND NEW.model_id IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'dispatch receipt is immutable'); END;
CREATE TABLE request_adaptations (
 id INTEGER PRIMARY KEY,
 scope TEXT NOT NULL CHECK(scope IN ('endpoint','charity_model','binding')),
 endpoint_id INTEGER UNIQUE REFERENCES endpoints(id) ON DELETE CASCADE,
 model_id INTEGER UNIQUE REFERENCES charity_models(id) ON DELETE CASCADE,
 binding_id INTEGER UNIQUE REFERENCES charity_model_bindings(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 secret_context BLOB NOT NULL CHECK(length(secret_context)=16),
 secret_ciphertext TEXT NOT NULL CHECK(length(CAST(secret_ciphertext AS BLOB)) BETWEEN 1 AND 131072),
 structure_json TEXT NOT NULL CHECK(json_valid(structure_json) AND json_type(structure_json)='object' AND length(CAST(structure_json AS BLOB))<=65536),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799),
 CHECK((scope='endpoint' AND endpoint_id IS NOT NULL AND model_id IS NULL AND binding_id IS NULL)
 OR (scope='charity_model' AND endpoint_id IS NULL AND model_id IS NOT NULL AND binding_id IS NULL)
 OR (scope='binding' AND endpoint_id IS NULL AND model_id IS NULL AND binding_id IS NOT NULL))
) STRICT;
CREATE TRIGGER request_adaptation_identity BEFORE UPDATE ON request_adaptations
WHEN NEW.id<>OLD.id OR NEW.scope<>OLD.scope OR NEW.endpoint_id IS NOT OLD.endpoint_id
 OR NEW.model_id IS NOT OLD.model_id OR NEW.binding_id IS NOT OLD.binding_id
 OR NEW.secret_context<>OLD.secret_context OR NEW.revision<OLD.revision
 OR (NEW.revision=OLD.revision AND (NEW.secret_ciphertext<>OLD.secret_ciphertext OR NEW.structure_json<>OLD.structure_json OR NEW.updated_at<>OLD.updated_at))
BEGIN SELECT RAISE(ABORT,'invalid request adaptation revision'); END;
CREATE TABLE request_adaptation_audits (
 id INTEGER PRIMARY KEY,
 scope TEXT NOT NULL CHECK(scope IN ('endpoint','charity_model','binding')),
 resource_id INTEGER NOT NULL CHECK(resource_id>0),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 actor_role TEXT NOT NULL CHECK(actor_role IN ('owner','admin')),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 changed_partitions TEXT NOT NULL CHECK(json_valid(changed_partitions) AND json_type(changed_partitions)='array' AND json_array_length(changed_partitions)<=5 AND length(CAST(changed_partitions AS BLOB))<=256),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 UNIQUE(scope,resource_id,revision),
 CHECK((scope='endpoint')=(actor_role='owner'))
) STRICT;
CREATE INDEX idx_request_adaptation_audits_expiry ON request_adaptation_audits(created_at,id);
CREATE INDEX idx_request_adaptation_audits_actor ON request_adaptation_audits(actor_user_id,id);
CREATE TRIGGER request_adaptation_audit_partitions BEFORE INSERT ON request_adaptation_audits
WHEN EXISTS(SELECT 1 FROM json_each(NEW.changed_partitions) WHERE type<>'text' OR value NOT IN ('forward_headers','fixed_headers','body_defaults','body_forced','native_extension_paths'))
 OR json_array_length(NEW.changed_partitions)<>(SELECT count(DISTINCT value) FROM json_each(NEW.changed_partitions))
BEGIN SELECT RAISE(ABORT,'invalid request adaptation audit partitions'); END;
CREATE TRIGGER request_adaptation_audit_immutable BEFORE UPDATE ON request_adaptation_audits
WHEN NOT (OLD.actor_user_id IS NOT NULL AND NEW.actor_user_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM users WHERE id=OLD.actor_user_id)
 AND NEW.id=OLD.id AND NEW.scope=OLD.scope AND NEW.resource_id=OLD.resource_id
 AND NEW.actor_role=OLD.actor_role AND NEW.revision=OLD.revision
 AND NEW.changed_partitions=OLD.changed_partitions AND NEW.created_at=OLD.created_at)
BEGIN SELECT RAISE(ABORT,'request adaptation audit is immutable'); END;
`
