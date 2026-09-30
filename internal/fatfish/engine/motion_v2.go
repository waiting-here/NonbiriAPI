package engine

const (
	turnDenominatorV3    int64 = 5_000_000_000_000
	sideTurnNumeratorV3  int64 = 97_784_797_035
	frontTurnNumeratorV3 int64 = 117_341_756_442
)

func (engine *Engine) probeBlocked(fish *FishState, forward, right int64) bool {
	sine, cosine := sinCos(fish.Heading)
	point := Point{
		fish.X + 64*(forward*cosine-right*sine)/trigScale,
		fish.Y + 64*(forward*sine+right*cosine)/trigScale,
	}
	return point.X < 0 || point.X > FieldWidth || point.Y < 0 || point.Y > FieldHeight || engine.centerShielded(point)
}

func clearMotionTurn(fish *FishState) {
	fish.TurnDir, fish.TurnDistance = 0, 0
	*fish.Motion = MotionState{}
}

func turnFactor(word uint32) int64 {
	return 950 + int64(uint64(word)*101/(1<<32))
}

func (engine *Engine) moveSubstepV2(fish *FishState, start Point, distance int64) {
	fish.TurnDistance = 0
	next, xRema, yRema, canMove := engine.canMove(fish, fish.Heading, distance)
	if canMove {
		// A legal decrease of existing overlap takes precedence over probes.
		// Sensors inside the covering shape must not trap a fish escaping it.
		for _, area := range engine.overlapAt(fish.X, fish.Y).areas {
			if area.Sign() > 0 {
				clearMotionTurn(fish)
				fish.X, fish.Y, fish.XRema, fish.YRema = next.X, next.Y, xRema, yRema
				engine.resolveContact(fish, start, next)
				return
			}
		}
	}
	front := engine.probeBlocked(fish, 15, 0)
	right := engine.probeBlocked(fish, 9, 6)
	left := engine.probeBlocked(fish, 9, -6)
	if !front && !right && !left && !canMove {
		front = true
	}
	base := int64(115)
	switch {
	case front || right && left:
		base = 138
		if fish.Motion.AmbiguousTurnDir == 0 {
			fish.Motion.AmbiguousTurnDir = -1
			if NextTurnWord(&fish.RNG)&1 != 0 {
				fish.Motion.AmbiguousTurnDir = 1
			}
		}
		fish.TurnDir = fish.Motion.AmbiguousTurnDir
	case right:
		fish.Motion.AmbiguousTurnDir, fish.TurnDir = 0, -1
	case left:
		fish.Motion.AmbiguousTurnDir, fish.TurnDir = 0, 1
	default:
		clearMotionTurn(fish)
		fish.X, fish.Y, fish.XRema, fish.YRema = next.X, next.Y, xRema, yRema
		engine.resolveContact(fish, start, next)
		return
	}
	denominator := int64(5000)
	if engine.level.EngineVersion == EngineVersion {
		denominator = turnDenominatorV3
		if base == 138 {
			base = frontTurnNumeratorV3
		} else {
			base = sideTurnNumeratorV3
		}
	}
	accumulated := fish.Motion.TurnRemainder + base*turnFactor(NextTurnWord(&fish.RNG))
	fish.Motion.TurnRemainder = accumulated % denominator
	fish.Heading = positiveMod(fish.Heading+fish.TurnDir*int(accumulated/denominator), 4096)
}
