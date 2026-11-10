package db

// CreditOperationAssetsSQL indexes the assets actually represented by retained
// entries. It contains no balances or user identities and follows entry retention.
const CreditOperationAssetsSQL = `CREATE TABLE credit_operation_assets (
 operation_id TEXT NOT NULL REFERENCES credit_operations(id) ON DELETE CASCADE,
 asset_type TEXT NOT NULL CHECK(asset_type IN ('general','game','sketch_paper','sketch_brush')),
 ledger_seq INTEGER NOT NULL CHECK(ledger_seq>0),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 kind TEXT NOT NULL,
 PRIMARY KEY(operation_id,asset_type)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_credit_operation_assets_seq ON credit_operation_assets(asset_type,ledger_seq);
CREATE INDEX idx_credit_operation_assets_time ON credit_operation_assets(asset_type,created_at,ledger_seq);
CREATE INDEX idx_credit_operation_assets_kind_time ON credit_operation_assets(asset_type,kind,created_at,ledger_seq);
INSERT INTO credit_operation_assets(operation_id,asset_type,ledger_seq,created_at,kind)
 SELECT o.id,e.asset_type,o.ledger_seq,o.created_at,o.kind
 FROM credit_operations o JOIN credit_entries e ON e.operation_id=o.id
 GROUP BY o.id,e.asset_type;
CREATE TRIGGER credit_operation_assets_insert AFTER INSERT ON credit_entries
BEGIN
 INSERT INTO credit_operation_assets(operation_id,asset_type,ledger_seq,created_at,kind)
 SELECT id,NEW.asset_type,ledger_seq,created_at,kind FROM credit_operations WHERE id=NEW.operation_id
 ON CONFLICT(operation_id,asset_type) DO NOTHING;
END;
CREATE TRIGGER credit_operation_assets_delete AFTER DELETE ON credit_entries
WHEN NOT EXISTS(SELECT 1 FROM credit_entries WHERE operation_id=OLD.operation_id AND asset_type=OLD.asset_type)
BEGIN
 DELETE FROM credit_operation_assets WHERE operation_id=OLD.operation_id AND asset_type=OLD.asset_type;
END;
`
