package db

const (
	DiscordGatePolicyInherit = "inherit"
	DiscordGatePolicyRequire = "require"
	DiscordGatePolicyExempt  = "exempt"
)

func ValidDiscordGatePolicy(policy string) bool {
	return policy == DiscordGatePolicyInherit || policy == DiscordGatePolicyRequire || policy == DiscordGatePolicyExempt
}
