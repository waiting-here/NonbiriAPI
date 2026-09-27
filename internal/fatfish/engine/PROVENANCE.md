# Deterministic engine algorithm provenance

The game engine code in this directory and its TypeScript counterpart are original implementations for this project and follow the repository's AGPL-3.0 license.

- The per-fish generator follows David Blackman and Sebastiano Vigna's [xoshiro128** 1.1 reference](https://prng.di.unimi.it/xoshiro128starstar.c). The authors dedicate that reference implementation to the public domain. The state transition is implemented independently in Go and TypeScript; no reference source file is copied.
- Polygon clipping uses the published Sutherland–Hodgman method; simple polygons are decomposed by deterministic ear clipping. The rational arithmetic and union-difference code here are original implementations. No third-party geometry library or copied code is included.
- SHA-256 follows [FIPS 180-4](https://doi.org/10.6028/NIST.FIPS.180-4). The browser implementation is original code; Go uses its standard library.
- `trig.dat` is the sole checked-in sine source. `generate_trig.py` generated it once with Python standard-library `math.sin`, rounded to integer scale 2^20. `trig.ts` is mechanically derived from those source bytes. Both consumers pin SHA-256 `5cd7e2c5b685af5bb52f87bb6f78e25b50619f47a63df1ab20a13e26de16a19b`.
