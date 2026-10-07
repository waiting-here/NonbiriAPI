# Steady Catch rules

The game code and character artwork were supplied with permission to distribute
them with NonbiriAPI under AGPL-3.0. The selected phrase catalog contains short
community expressions and original additions; its categories do not assert an
exclusive author or origin for an expression.

The rules preserve the original 90-second round, health, scoring, combos,
collectibles, hazards and shield. The platform implementation uses a fixed
600 × 560 field, integer positions in thousandths and 60 steps per second.
Collision sizes are independent of text and screen size. Gameplay randomness
is independent of visual effects; gold drops keep the original 5/96 proportion
as the phrase catalog grows.

The browser predicts these rules for immediate input feedback. Submitted input
contains controls; the server recomputes outcomes. The compressed checkpoint
fixture covers idle play, keyboard movement and pointer movement with shields,
including both failure and completion. Go and browser tests read the same
checkpoints.
