# Embedded time zone data

`zoneinfo.zip` is copied unchanged from `lib/time/zoneinfo.zip` in the Go
1.26.6 toolchain. Its SHA-256 is
`8f55634d05f8bca1f7bc7c69c5933428c69357e0bdf565e5ba224e3f88ff12e8`.
The API reports this archive as `go1.26.6-zoneinfo`.

The Go distribution's `lib/time/README` identifies the archive as compiled
code and data from the IANA Time Zone Database, which IANA places in the
public domain. See <https://www.iana.org/time-zones> and
<https://www.iana.org/time-zones/repository/tz-link.html>.

Time zone updates must replace the pinned archive and repeat the transition,
calendar boundary, continuation-rule and retention-bound checks. Persisted
period endpoints must retain their original instant.
