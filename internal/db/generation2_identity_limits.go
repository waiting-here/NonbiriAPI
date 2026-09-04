package db

// MaxBanDurationSeconds is the schema-independent upper bound shared by the
// typed site-config catalog and the anti-abuse configuration reader. Runtime
// authorization and mutation behavior belongs to the control-plane owner.
const MaxBanDurationSeconds int64 = 10 * 366 * 24 * 60 * 60
