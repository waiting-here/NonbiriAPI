# Attendance activities

Administrators configure attendance periods from activity management. Users
sign in independently to each period from complete cards on the main activity
page. There is no separate activity route, admission ticket or claim button.
Multiple published periods can overlap and accept sign-ins on the same day.

Each period uses cumulative or consecutive signed-in days. A missed site date
restarts the current consecutive count; the best count and already awarded
milestones remain. Only configured milestones have rewards. Every milestone
bundle uses the same asset, quantity, duplicate-conversion and capacity rules
as site-wide rewards and redemption codes.

The sign-in reaching a milestone grants its reward in the same transaction.
Any failed item rolls back the complete bundle, progress and daily sign-in.
The unique period/account/day and period/account/milestone facts prevent a
double award from simultaneous requests, repeated clicks or a rebuilt streak.
An uncertain request retains its original date and idempotency key. Replaying
an existing daily record returns that day's receipt and current progress without
signing in on a later day. An ended period accepts no new daily records.

## Calendar and editing

Dates use the site's configured fixed UTC offset, captured by the period.
The first instant is the start date at 00:00; the exclusive end is midnight
after the end date. The range cannot include the site date of creation or a
rules edit, and must contain at least as many days as its largest milestone.
Creating a period freezes the existing site time-zone configuration.

Before its original start, a period's configuration can be changed with its
current revision. Once started, only its title and description are editable;
dates, mode, publication state and rewards remain fixed. This also preserves
earned rights after the end date. Deletion of launched periods is not exposed.

Limits are 80 title characters, 2,000 description characters, 3,660 calendar
days, 366 distinct milestones, 32 reward items per milestone and 1,024 items
across a period. Milestones are sorted by day by the server. Activity admission
continues to enforce current account authorization, maintenance and the global
activities switch. There is no background sign-in or reward-grant timer.

## API and storage

`GET /api/attendance` and `GET /admin/api/attendance` accept bounded `page` and
`page_size` parameters. User results contain published periods, the authoritative
site date, progress, reward receipts and current sign-in availability.

`POST /api/attendance/{id}/sign-in` requires an `Idempotency-Key` and the server
date as `{ "site_date": "YYYY-MM-DD" }`. Its receipt includes the original
date, automatic `awards` and a fresh `period` projection. Administrator creation
uses `POST /admin/api/attendance`; updates use `PUT /admin/api/attendance/{id}`
with an `expected_revision`. Both use idempotency and management audit records.

Schema 49 adds `attendance_periods`, `attendance_tiers`, `attendance_progress`,
`attendance_sign_ins` and `attendance_awards`. Daily facts and awards remain
for the lifetime of the account, including ended periods. Export format 34
includes the account's period descriptions, progress, sign-in dates and reward
receipts, bounded by the existing export size and collection limits. Deleting
the account removes its progress, dates, awards and retry records atomically;
the creator link on shared period definitions becomes null. Internal retry
hashes and other participants are excluded from export.
