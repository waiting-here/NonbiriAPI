package charityrouting

// Ordinary bans pause donated supply; permanent inactivity protection does not.
// decisionNow is a repository-owned SQL expression, never request input.
func donorAvailableSQL(decisionNow string) string {
	return "EXISTS(SELECT 1 FROM users donor WHERE donor.id=d.user_id AND (donor.is_banned=0 OR donor.banned_until<=" + decisionNow + " OR (donor.banned_until IS NULL AND donor.ban_kind='protective_inactivity')))"
}
