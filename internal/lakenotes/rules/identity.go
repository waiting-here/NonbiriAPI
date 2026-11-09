package rules

import _ "embed"

// RulesID binds the original catalog, both algorithms, and numerical protocol.
const RulesID = "lake-notes-b01b7204c3eec33dfe679523a2855ede5f782cc7ab967c3809fd06319bfe7601"

//go:embed identity_payload.json
var identityJSON []byte

func RulesIdentityJSON() []byte { return append([]byte(nil), identityJSON...) }
