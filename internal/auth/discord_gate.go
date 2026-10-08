package auth

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// Membership is fetched outside the session transaction. The transaction
// checks the current policy and guild against this verified response.
type discordMembership struct {
	guild  string
	member *GuildMember
	err    error
}

func (r *Runtime) existingLoginMembership(ctx context.Context, login DiscordLogin) *discordMembership {
	proof := &discordMembership{}
	proof.guild, proof.err = r.registrationGuild(ctx)
	if proof.err != nil {
		return proof
	}
	member, err := memberFor(ctx, login, proof.guild)
	proof.err = err
	if err == nil {
		member.Avatar = dereference(discordGuildAvatarURL(proof.guild, login.Identity.ID, member.Avatar))
		proof.member = &member
	}
	return proof
}

func requireDiscordGateTx(ctx context.Context, tx *sql.Tx, policy string, proof *discordMembership) error {
	switch policy {
	case db.DiscordGatePolicyExempt:
		return nil
	case db.DiscordGatePolicyInherit:
		value, err := configTx(ctx, tx, "discord_registered_user_gate_exempt")
		if err != nil || value != "0" && value != "1" {
			return ErrProviderUnavailable
		}
		if value == "1" {
			return nil
		}
	case db.DiscordGatePolicyRequire:
	default:
		return ErrProviderUnavailable
	}
	guild, err := configTx(ctx, tx, "discord_guild_id")
	if err != nil || !validateBoundedText(guild, 128, false) || proof == nil || guild != proof.guild {
		return ErrProviderUnavailable
	}
	role, err := configTx(ctx, tx, "discord_role_id")
	if err != nil || !validateBoundedText(role, 128, false) {
		return ErrProviderUnavailable
	}
	if proof.err != nil {
		return proof.err
	}
	if proof.member == nil || !hasRole(proof.member.Roles, role) {
		return ErrGuildRoleMismatch
	}
	return nil
}
