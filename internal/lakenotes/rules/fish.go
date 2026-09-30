package rules

import "math"

type Challenge struct {
	Difficulty float64 `json:"difficulty"`
	FishSpeed  float64 `json:"fishSpeed"`
	Tempo      float64 `json:"tempo"`
	Gain       float64 `json:"gain"`
	Loss       float64 `json:"loss"`
}
type FishState struct {
	Position float64 `json:"position"`
	Y        float64 `json:"y"`
	Speed    float64 `json:"speed"`
	Target   float64 `json:"target"`
	Drift    float64 `json:"drift"`
}

func MakeChallenge(f FishType, length int) Challenge {
	relative := clamp(float64(float64(length-f.Length[0])/float64(f.Length[1]-f.Length[0])), 0, 1)
	return Challenge{Difficulty: clamp(float64(f.Difficulty+round(float64(relative*8))), 5, 110), FishSpeed: 1, Tempo: 1, Gain: 1, Loss: 1}
}
func InitialFish(c Challenge) FishState {
	p := clamp(float64(float64(508.0/568.0)*568), 0, 532)
	return FishState{Position: p, Y: float64(p / 568), Target: clamp(float64(float64(float64(100-c.Difficulty)/100)*548), 0, 548)}
}
func (f *FishState) Step(r *MotionRandom, behavior string, c Challenge) {
	steps := float64(TickSeconds * 60)
	d := c.Difficulty
	tempo := c.Tempo
	weight := 1.0
	if behavior == "smooth" {
		weight = 20
	}
	threshold := float64(float64(float64(float64(d*weight)/4000)*tempo) * steps)
	if r.Next() < threshold && (behavior != "smooth" || f.Target < 0) {
		percent := math.Min(0.99, float64(float64(d+r.Range(10, 45))/100))
		f.Target = clamp(float64(f.Position+float64(r.Range(-f.Position, float64(548-f.Position))*percent)), 0, 548)
	}
	if behavior == "floater" {
		f.Drift = math.Max(-1.5, float64(f.Drift-float64(0.01*steps)))
	}
	if behavior == "sinker" {
		f.Drift = math.Min(1.5, float64(f.Drift+float64(0.01*steps)))
	}
	if f.Target >= 0 && math.Abs(float64(f.Position-f.Target)) > 3 {
		acc := float64(float64(f.Target-f.Position) / float64(float64(r.Range(10, 30)+100)-math.Min(100, d)))
		f.Speed = float64(f.Speed + float64(float64(float64(acc-f.Speed)/5)*steps))
	} else if behavior != "smooth" && r.Next() < float64(float64(float64(d/2000)*tempo)*steps) {
		direction := 1.0
		if r.Next() < 0.5 {
			direction = -1
		}
		f.Target = clamp(float64(f.Position+float64(direction*r.Range(50, 101))), 0, 548)
	} else {
		f.Target = -1
	}
	if behavior == "dart" && r.Next() < float64(float64(float64(d/1000)*tempo)*steps) {
		direction := 1.0
		if r.Next() < 0.5 {
			direction = -1
		}
		f.Target = clamp(float64(f.Position+float64(direction*r.Range(51, float64(101+float64(d*2))))), 0, 548)
	}
	f.Position = clamp(float64(f.Position+float64(float64(float64(f.Speed+f.Drift)*c.FishSpeed)*steps)), 0, 532)
	f.Y = float64(f.Position / 568)
}
