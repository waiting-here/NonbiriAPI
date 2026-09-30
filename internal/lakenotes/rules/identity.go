package rules

import _ "embed"

// RulesID binds the original catalog, both algorithms, and numerical protocol.
const RulesID = "lake-notes-d1be1134679327c43a38766d857cb0d3131659d2049cf7ea667034bcf727255e"

//go:embed identity_payload.json
var identityJSON []byte

func RulesIdentityJSON() []byte { return append([]byte(nil), identityJSON...) }
