package rules

import _ "embed"

// RulesID binds the original catalog, both algorithms, and numerical protocol.
const RulesID = "lake-notes-69248b547d87b06e1d6279bfbfb27937c0015d514cbdd6040d11ad796977dba3"

//go:embed identity_payload.json
var identityJSON []byte

func RulesIdentityJSON() []byte { return append([]byte(nil), identityJSON...) }
