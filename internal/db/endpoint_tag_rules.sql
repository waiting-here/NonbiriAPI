CREATE TABLE endpoint_tag_rules (
 domain TEXT PRIMARY KEY CHECK(length(domain) BETWEEN 1 AND 253 AND domain=lower(domain)),
 action TEXT NOT NULL CHECK(action IN ('add','remove'))
) STRICT, WITHOUT ROWID;
CREATE TABLE endpoint_tag_rule_tags (
 domain TEXT NOT NULL REFERENCES endpoint_tag_rules(domain) ON DELETE CASCADE,
 tag TEXT NOT NULL CHECK(tag IN ('abusive_third_party','community_charity','noncommercial_gateway','user_gateway','steward_gateway','invalid_entry','mainstream','mysterious','other_third_party')),
 PRIMARY KEY(domain,tag)
) STRICT, WITHOUT ROWID;
CREATE TRIGGER endpoint_tag_rule_remove_guard BEFORE INSERT ON endpoint_tag_rule_tags
WHEN NOT EXISTS(SELECT 1 FROM endpoint_tag_rules WHERE domain=NEW.domain AND action='add')
BEGIN SELECT RAISE(ABORT,'remove rule cannot add tags'); END;
CREATE TRIGGER endpoint_tag_rule_remove_update_guard BEFORE UPDATE OF action ON endpoint_tag_rules
WHEN NEW.action='remove' AND EXISTS(SELECT 1 FROM endpoint_tag_rule_tags WHERE domain=NEW.domain)
BEGIN SELECT RAISE(ABORT,'remove rule cannot add tags'); END;

-- This derived index has the same lifetime as its existing source addresses.
-- It stores only DNS ancestors; literals cannot match wildcard domain rules.
CREATE TABLE endpoint_tag_domains (
 base_url TEXT NOT NULL,
 domain TEXT NOT NULL,
 PRIMARY KEY(base_url,domain)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_endpoint_tag_domains_domain ON endpoint_tag_domains(domain,base_url);
CREATE INDEX idx_donation_keys_base_url ON donation_keys(canonical_base_url);
INSERT INTO endpoint_tag_domains(base_url,domain)
SELECT address.base_url,j.value FROM (
 SELECT base_url FROM endpoints UNION SELECT canonical_base_url FROM donation_keys UNION SELECT base_url FROM admin_endpoint_tags
) address, json_each(nbi_endpoint_domains(address.base_url)) j;

CREATE TRIGGER endpoint_tag_domains_endpoint_insert AFTER INSERT ON endpoints
WHEN NOT EXISTS(SELECT 1 FROM endpoint_tag_domains WHERE base_url=NEW.base_url)
BEGIN INSERT INTO endpoint_tag_domains SELECT NEW.base_url,value FROM json_each(nbi_endpoint_domains(NEW.base_url)); END;
CREATE TRIGGER endpoint_tag_domains_key_insert AFTER INSERT ON donation_keys
WHEN NOT EXISTS(SELECT 1 FROM endpoint_tag_domains WHERE base_url=NEW.canonical_base_url)
BEGIN INSERT INTO endpoint_tag_domains SELECT NEW.canonical_base_url,value FROM json_each(nbi_endpoint_domains(NEW.canonical_base_url)); END;
CREATE TRIGGER endpoint_tag_domains_manual_insert AFTER INSERT ON admin_endpoint_tags
WHEN NOT EXISTS(SELECT 1 FROM endpoint_tag_domains WHERE base_url=NEW.base_url)
BEGIN INSERT INTO endpoint_tag_domains SELECT NEW.base_url,value FROM json_each(nbi_endpoint_domains(NEW.base_url)); END;
CREATE TRIGGER endpoint_tag_domains_endpoint_delete AFTER DELETE ON endpoints
WHEN NOT EXISTS(SELECT 1 FROM endpoints WHERE base_url=OLD.base_url)
 AND NOT EXISTS(SELECT 1 FROM donation_keys WHERE canonical_base_url=OLD.base_url)
 AND NOT EXISTS(SELECT 1 FROM admin_endpoint_tags WHERE base_url=OLD.base_url)
BEGIN DELETE FROM endpoint_tag_domains WHERE base_url=OLD.base_url; END;
CREATE TRIGGER endpoint_tag_domains_key_delete AFTER DELETE ON donation_keys
WHEN NOT EXISTS(SELECT 1 FROM endpoints WHERE base_url=OLD.canonical_base_url)
 AND NOT EXISTS(SELECT 1 FROM donation_keys WHERE canonical_base_url=OLD.canonical_base_url)
 AND NOT EXISTS(SELECT 1 FROM admin_endpoint_tags WHERE base_url=OLD.canonical_base_url)
BEGIN DELETE FROM endpoint_tag_domains WHERE base_url=OLD.canonical_base_url; END;
CREATE TRIGGER endpoint_tag_domains_manual_delete AFTER DELETE ON admin_endpoint_tags
WHEN NOT EXISTS(SELECT 1 FROM endpoints WHERE base_url=OLD.base_url)
 AND NOT EXISTS(SELECT 1 FROM donation_keys WHERE canonical_base_url=OLD.base_url)
 AND NOT EXISTS(SELECT 1 FROM admin_endpoint_tags WHERE base_url=OLD.base_url)
BEGIN DELETE FROM endpoint_tag_domains WHERE base_url=OLD.base_url; END;

CREATE VIEW endpoint_tag_rule_matches AS
SELECT d.base_url,r.domain,r.action FROM endpoint_tag_domains d JOIN endpoint_tag_rules r ON r.domain=d.domain;
CREATE VIEW effective_endpoint_tags AS
SELECT m.base_url,m.tag FROM admin_endpoint_tags m
WHERE NOT EXISTS(SELECT 1 FROM endpoint_tag_rule_matches r WHERE r.base_url=m.base_url AND r.action='remove')
 AND NOT EXISTS(SELECT 1 FROM endpoint_tag_rule_matches r JOIN endpoint_tag_rule_tags t ON t.domain=r.domain
 WHERE r.base_url=m.base_url AND t.tag=m.tag)
UNION ALL
SELECT r.base_url,t.tag FROM endpoint_tag_rule_matches r JOIN endpoint_tag_rule_tags t ON t.domain=r.domain
WHERE r.action='add' AND NOT EXISTS(SELECT 1 FROM endpoint_tag_rule_matches narrower
 WHERE narrower.base_url=r.base_url AND narrower.action='remove' AND length(narrower.domain)>length(r.domain))
 AND NOT EXISTS(SELECT 1 FROM endpoint_tag_rule_matches narrower JOIN endpoint_tag_rule_tags nt ON nt.domain=narrower.domain
 WHERE narrower.base_url=r.base_url AND nt.tag=t.tag AND length(narrower.domain)>length(r.domain));
