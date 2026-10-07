package rules

import _ "embed"

// RulesID binds the original catalog, both algorithms, and numerical protocol.
const RulesID = "lake-notes-ad5825786cb2c4ac97648df2cfa865457de7dd141f51fdc95abcb99b2aec1d97"

//go:embed identity_payload.json
var identityJSON []byte

func RulesIdentityJSON() []byte { return append([]byte(nil), identityJSON...) }
