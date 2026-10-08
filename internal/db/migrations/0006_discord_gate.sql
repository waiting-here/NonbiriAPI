ALTER TABLE users ADD COLUMN discord_gate_policy TEXT NOT NULL DEFAULT 'inherit' CHECK(discord_gate_policy IN ('inherit','require','exempt') AND (is_admin=0 OR discord_gate_policy='inherit'));
INSERT INTO site_config(key,value,updated_at) VALUES('discord_registered_user_gate_exempt','0',0) ON CONFLICT(key) DO NOTHING;
