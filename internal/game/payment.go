package game

// Payment preserves the assets used for a game entry.
type Payment struct {
	General string `json:"general"`
	Game    string `json:"game"`
}

// PaymentFromMilli formats already validated entry amounts.
func PaymentFromMilli(total, gamePaid int64) Payment {
	return Payment{General: FormatAmount(total - gamePaid), Game: FormatAmount(gamePaid)}
}
