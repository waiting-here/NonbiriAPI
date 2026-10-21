package db

// GlobalPressureSchema stores only site-wide, sampled counters.
const GlobalPressureSchema = `
CREATE TABLE global_pressure_state (
 id INTEGER PRIMARY KEY CHECK(id=1),
 started_at INTEGER NOT NULL CHECK(started_at>0),
 rpm_peak INTEGER NOT NULL CHECK(rpm_peak>=0),
 concurrency_peak INTEGER NOT NULL CHECK(concurrency_peak>=0)
) STRICT;
CREATE TABLE global_pressure_samples (
 observed_at INTEGER PRIMARY KEY CHECK(observed_at>0),
 rpm INTEGER NOT NULL CHECK(rpm>=0),
 concurrency INTEGER NOT NULL CHECK(concurrency>=0)
) STRICT;
`
