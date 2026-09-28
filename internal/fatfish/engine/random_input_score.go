package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"strconv"
)

func InitialFishRNG(seed [32]byte, fishID int) [4]uint32 {
	var id [4]byte
	binary.LittleEndian.PutUint32(id[:], uint32(fishID))
	payload := make([]byte, 0, len("nonbiri-fatfish-turn-v1")+1+32+4)
	payload = append(payload, "nonbiri-fatfish-turn-v1"...)
	payload = append(payload, 0)
	payload = append(payload, seed[:]...)
	payload = append(payload, id[:]...)
	digest := sha256.Sum256(payload)
	var state [4]uint32
	for index := range state {
		state[index] = binary.LittleEndian.Uint32(digest[index*4 : index*4+4])
	}
	if state == [4]uint32{} {
		state[3] = 1
	}
	return state
}

// NextTurnBit advances only this fish's xoshiro128** stream.
// Algorithm: Blackman and Vigna, xoshiro128** 1.1 (public-domain dedication).
func NextTurnBit(state *[4]uint32) uint32 {
	return NextTurnWord(state) & 1
}

// NextTurnWord advances the same per-fish stream and returns its full word.
func NextTurnWord(state *[4]uint32) uint32 {
	result := bits.RotateLeft32(state[1]*5, 7) * 9
	t := state[1] << 9
	state[2] ^= state[0]
	state[3] ^= state[1]
	state[1] ^= state[2]
	state[0] ^= state[3]
	state[2] ^= t
	state[3] = bits.RotateLeft32(state[3], 11)
	return result
}

func ScoreUnits(total, fed, durationSeconds, terminalTick int) (int64, error) {
	if total < 1 || total > 40 || fed < 0 || fed > total || durationSeconds < 10 || durationSeconds > 600 || terminalTick < 0 || terminalTick > durationSeconds*TicksPerSecond {
		return 0, errors.New("score inputs are out of range")
	}
	maxTick := durationSeconds * TicksPerSecond
	denominator := int64(total+1)*int64(maxTick+1) - 1
	numerator := int64(fed)*int64(maxTick+1) + int64(maxTick-terminalTick)
	return 100000000 * numerator / denominator, nil
}

func Stars(thresholds [3]int, fed int, bowls []Bowl, counts []BowlState) (int, bool) {
	if fed < thresholds[0] {
		return 0, false
	}
	for _, bowl := range bowls {
		found := false
		for _, count := range counts {
			if count.ID == bowl.ID && count.Count >= bowl.Required {
				found = true
				break
			}
		}
		if !found {
			return 0, false
		}
	}
	for index := len(thresholds) - 1; index >= 0; index-- {
		if fed >= thresholds[index] {
			return index + 1, true
		}
	}
	return 0, false
}

func (input InputTuple) MarshalJSON() ([]byte, error) {
	if input.Op == "place" {
		return json.Marshal([]any{input.Tick, input.Seq, input.Op, input.ToolID, input.X, input.Y})
	}
	return json.Marshal([]any{input.Tick, input.Seq, input.Op, input.ToolID})
}

func (input *InputTuple) UnmarshalJSON(raw []byte) error {
	if err := checkJSON(raw, 3); err != nil {
		return err
	}
	var fields []json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if len(fields) != 4 && len(fields) != 6 {
		return errors.New("input tuple length is invalid")
	}
	values := []*int64{new(int64), new(int64), new(int64)}
	for index, field := range []json.RawMessage{fields[0], fields[1], fields[3]} {
		value, err := parseTupleInteger(field)
		if err != nil || value < 0 || value > maxSafeInteger {
			return fmt.Errorf("input integer[%d] is invalid", index)
		}
		*values[index] = value
	}
	var op string
	if err := json.Unmarshal(fields[2], &op); err != nil {
		return errors.New("input op is invalid")
	}
	if (op == "place") != (len(fields) == 6) || (op != "place" && op != "return" && op != "finish" && op != "abandon") {
		return errors.New("input op and tuple length disagree")
	}
	*input = InputTuple{Tick: int(*values[0]), Seq: int(*values[1]), Op: op, ToolID: int(*values[2])}
	if op == "place" {
		var err error
		input.X, err = parseTupleInteger(fields[4])
		if err != nil {
			return err
		}
		input.Y, err = parseTupleInteger(fields[5])
		if err != nil {
			return err
		}
	}
	return nil
}

func parseTupleInteger(raw []byte) (int64, error) {
	literal := string(raw)
	if literal == "-0" || bytes.ContainsAny(raw, ".eE") {
		return 0, errors.New("tuple number is not a canonical integer")
	}
	value, err := strconv.ParseInt(literal, 10, 64)
	if err != nil || value < -maxSafeInteger || value > maxSafeInteger {
		return 0, errors.New("tuple integer exceeds safe range")
	}
	return value, nil
}

func ParseInputs(raw []byte, level Level) ([]InputTuple, error) {
	if len(raw) > MaxInputBytes || len(raw) == 0 {
		return nil, errors.New("input payload exceeds bounds")
	}
	if err := checkJSON(raw, 3); err != nil {
		return nil, err
	}
	var inputs []InputTuple
	if err := json.Unmarshal(raw, &inputs); err != nil {
		return nil, err
	}
	if inputs == nil {
		return nil, errors.New("input payload must be an array")
	}
	if err := ValidateInputs(inputs, level); err != nil {
		return nil, err
	}
	return inputs, nil
}

func ValidateInputs(inputs []InputTuple, level Level) error {
	if len(inputs) > MaxInputs {
		return errors.New("too many input events")
	}
	tools := make(map[int]struct{}, len(level.Tools))
	for _, tool := range level.Tools {
		tools[tool.ID] = struct{}{}
	}
	lastTick, lastSeq := -1, -1
	effective, terminal := 0, false
	lastPlaceTool := -1
	for index, input := range inputs {
		if terminal {
			return fmt.Errorf("input[%d] occurs after terminal operation", index)
		}
		if input.Tick < 0 || input.Tick >= level.DurationSeconds*TicksPerSecond || input.Seq < 0 || input.Tick < lastTick || input.Tick == lastTick && input.Seq <= lastSeq {
			return fmt.Errorf("input[%d] tick or sequence is invalid", index)
		}
		if input.Tick != lastTick {
			effective, lastPlaceTool = 0, -1
		}
		switch input.Op {
		case "place":
			if _, ok := tools[input.ToolID]; !ok || input.X < -DragBuffer || input.X > FieldWidth+DragBuffer || input.Y < -DragBuffer || input.Y > FieldHeight+DragBuffer {
				return fmt.Errorf("input[%d] tool or position is invalid", index)
			}
			if lastPlaceTool != input.ToolID {
				effective++
			}
			lastPlaceTool = input.ToolID
		case "return":
			if _, ok := tools[input.ToolID]; !ok {
				return fmt.Errorf("input[%d] tool is unknown", index)
			}
			effective++
			lastPlaceTool = -1
		case "finish", "abandon":
			if input.ToolID != 0 {
				return fmt.Errorf("input[%d] terminal tool ID must be zero", index)
			}
			effective++
			terminal = true
			lastPlaceTool = -1
		default:
			return fmt.Errorf("input[%d] operation is invalid", index)
		}
		if effective > 2 {
			return fmt.Errorf("input[%d] exceeds the two-event tick limit", index)
		}
		lastTick, lastSeq = input.Tick, input.Seq
	}
	return nil
}
