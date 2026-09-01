// Package adminusers owns the Generation 2 administrator user and
// observability projections. Authentication at the admin-station boundary is
// supplied by the root registrar; every operation is re-authorized in its
// database transaction before any state is observed or changed.
package adminusers
