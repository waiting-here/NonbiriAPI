package db

var StewardResourcePermissionSQL = permissionCaptureSQL(append(append([]string{}, schema51ConfigurationPermissions...), "users.resources"))
