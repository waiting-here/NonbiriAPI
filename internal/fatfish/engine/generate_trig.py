"""Generate the one pinned sine source and its TypeScript consumer.

Run only when changing the engine protocol. Runtime code never evaluates trig.
"""

from hashlib import sha256
from math import pi, sin
from pathlib import Path


ROOT = Path(__file__).resolve().parents[3]
HERE = Path(__file__).resolve().parent
TS = ROOT / "web/src/shared/fatfish/engine/trig.ts"
SCALE = 1 << 20
values = [round(sin(2 * pi * index / 4096) * SCALE) for index in range(4096)]
source = ("\n".join(str(value) for value in values) + "\n").encode("ascii")
digest = sha256(source).hexdigest()
HERE.joinpath("trig.dat").write_bytes(source)
TS.parent.mkdir(parents=True, exist_ok=True)
TS.write_text(
    "// Generated from internal/fatfish/engine/trig.dat by generate_trig.py.\n"
    f'export const TRIG_SOURCE_SHA256 = "{digest}";\n'
    "export const SIN_TABLE: readonly number[] = [\n"
    + "".join("  " + ", ".join(str(value) for value in values[offset : offset + 16]) + ",\n" for offset in range(0, 4096, 16))
    + "];\n",
    encoding="utf-8",
    newline="\n",
)
print(digest)
