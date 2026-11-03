package rules

import _ "embed"

// RulesID binds the original catalog, both algorithms, and numerical protocol.
const RulesID = "lake-notes-33f47677b8f2d324619d6fa55bcfee0704a7fe5734c90870e0e33911894fd3bd"

//go:embed identity_payload.json
var identityJSON []byte

func RulesIdentityJSON() []byte { return append([]byte(nil), identityJSON...) }
