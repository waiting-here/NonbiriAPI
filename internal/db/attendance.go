package db

// AttendanceSQL stores immutable dates, daily facts, and exactly-once awards.
const AttendanceSQL = `
CREATE TABLE attendance_periods (
 id TEXT PRIMARY KEY NOT NULL CHECK(length(id)=26 AND substr(id,1,4)='att_'),
 title TEXT NOT NULL CHECK(length(title) BETWEEN 1 AND 80),
 description TEXT NOT NULL CHECK(length(description)<=2000),
 start_date TEXT NOT NULL CHECK(length(start_date)=10),
 end_date TEXT NOT NULL CHECK(length(end_date)=10 AND end_date>=start_date),
 timezone_offset_minutes INTEGER NOT NULL CHECK(timezone_offset_minutes BETWEEN -720 AND 840 AND timezone_offset_minutes%30=0),
 starts_at INTEGER NOT NULL CHECK(starts_at BETWEEN 0 AND 253402300799),
 ends_at INTEGER NOT NULL CHECK(ends_at>starts_at AND ends_at<=253402300800 AND ends_at-starts_at<=316224000),
 mode TEXT NOT NULL CHECK(mode IN ('cumulative','consecutive')),
 published INTEGER NOT NULL CHECK(published IN (0,1)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253402300799)
) STRICT;
CREATE INDEX idx_attendance_periods_published ON attendance_periods(published,start_date DESC,id DESC);
CREATE INDEX idx_attendance_periods_dates ON attendance_periods(start_date DESC,id DESC);
CREATE INDEX idx_attendance_periods_actor ON attendance_periods(actor_user_id);
CREATE TABLE attendance_tiers (
 period_id TEXT NOT NULL REFERENCES attendance_periods(id) ON DELETE CASCADE,
 days INTEGER NOT NULL CHECK(days BETWEEN 1 AND 3660),
 rewards TEXT NOT NULL CHECK(length(rewards)<=8192 AND json_valid(rewards) AND json_type(rewards)='array' AND json_array_length(rewards) BETWEEN 1 AND 32),
 PRIMARY KEY(period_id,days)
) STRICT;
CREATE TABLE attendance_progress (
 period_id TEXT NOT NULL REFERENCES attendance_periods(id) ON DELETE CASCADE,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 total_days INTEGER NOT NULL CHECK(total_days BETWEEN 1 AND 3660),
 current_streak INTEGER NOT NULL CHECK(current_streak BETWEEN 1 AND total_days),
 best_streak INTEGER NOT NULL CHECK(best_streak BETWEEN current_streak AND total_days),
 last_date TEXT NOT NULL CHECK(length(last_date)=10),
 PRIMARY KEY(period_id,user_id)
) STRICT;
CREATE INDEX idx_attendance_progress_user ON attendance_progress(user_id,period_id);
CREATE TABLE attendance_sign_ins (
 period_id TEXT NOT NULL,
 user_id INTEGER NOT NULL,
 site_date TEXT NOT NULL CHECK(length(site_date)=10),
 signed_at INTEGER NOT NULL CHECK(signed_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(period_id,user_id,site_date),
 FOREIGN KEY(period_id,user_id) REFERENCES attendance_progress(period_id,user_id) ON DELETE CASCADE
) STRICT;
CREATE INDEX idx_attendance_sign_ins_user ON attendance_sign_ins(user_id,period_id,site_date);
CREATE TABLE attendance_awards (
 period_id TEXT NOT NULL,
 user_id INTEGER NOT NULL,
 days INTEGER NOT NULL,
 site_date TEXT NOT NULL,
 results TEXT NOT NULL CHECK(length(results)<=32768 AND json_valid(results) AND json_type(results)='array' AND json_array_length(results) BETWEEN 1 AND 32),
 awarded_at INTEGER NOT NULL CHECK(awarded_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(period_id,user_id,days),
 FOREIGN KEY(period_id,days) REFERENCES attendance_tiers(period_id,days) ON DELETE CASCADE,
 FOREIGN KEY(period_id,user_id,site_date) REFERENCES attendance_sign_ins(period_id,user_id,site_date) ON DELETE CASCADE
) STRICT;
CREATE INDEX idx_attendance_awards_day ON attendance_awards(period_id,user_id,site_date,days);
CREATE INDEX idx_attendance_awards_user ON attendance_awards(user_id,period_id,days);
`
