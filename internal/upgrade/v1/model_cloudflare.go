package v1

const modelCloudflareSchema = `ALTER TABLE models ADD COLUMN cloudflare_proxy_enabled INTEGER NOT NULL DEFAULT 1 CHECK(cloudflare_proxy_enabled IN (0,1));
ALTER TABLE charity_models ADD COLUMN cloudflare_proxy_enabled INTEGER NOT NULL DEFAULT 0 CHECK(cloudflare_proxy_enabled IN (0,1));
`
