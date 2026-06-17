# Layer blacklisting for wall recognition

Context: `--emit-wall-segments` detects walls by pairing parallel line faces. On real
architectural exports it also pairs furniture, openings, dimensions, hatch fill, and
drawing symbols into false "walls". A built-in layer blacklist removes most of that
noise without losing real walls, because CAD apps tag leaf geometry on semantic layers.

This document records the analysis that justifies the default blacklist and the proposed
pattern list. It is a living document — cross-app findings (Chief Architect, Revit,
SketchUp, Archicad USA, etc.) are appended as files are checked.

## Method

For each file: `--emit-wall-segments out.xml`, then aggregate detected `<walls>` and raw
`<segments>` by **base layer name** (strip the ArchiCAD `_Pen_No__N` pen suffix). Signals
used to classify a layer: detected-wall count, raw-segment count, median segment length
(tiny ⇒ hatch fill), median detected thickness, and source mix (LINE/LWPOLYLINE/POLYLINE).

## Finding 1 — walls live on a few semantic layers; junk is separable

Across the 4 Hungarian ArchiCAD floor plans (`munkasszallo`, meters, ~57×33 m):

| Base layer (HU → EN) | walls | what it is | verdict |
|---|---:|---|---|
| `0` | 245 | layer-0 generic — real wall faces land here | **KEEP** |
| `Vázszerkezet - tartószerkezet` (load-bearing structure) | 208 | structural walls/columns | **KEEP** |
| `TX-11` | 41 | hatch fill (median seg = 2.7 cm) | special, see Finding 3 |
| `Helyiség` (room/zone) | 165 | zone-boundary polylines → thin 0.1 m "walls" | drop |
| `Beltér - berendezés` (interior furnishing) | 109 | **furniture** | drop |
| `Archicad Door/Window Markers` | 92 | swing/marker symbols | drop |
| `Archicad Windows` / `Doors` | 77 | opening leaf geometry (not walls) | drop |
| `Rajz és ábra` (drawing & figure) | 57 | detail symbols | drop |
| `Jel - metszet` (section symbol) | 12 | section marks | drop |
| `Méretezés` (dimensioning) | 0 | dimensions (already filtered out) | drop |

Real walls concentrate on `0` + the structural layer. Everything else is annotation,
furniture, openings, or hatch — i.e. separable by layer name.

## Finding 2 — blacklist impact (measured, all 4 files)

A case-insensitive substring blacklist drops **512 of 1006 detected walls (~51%)**, and
every dropped layer is genuine junk; **no real wall is lost** (survivors are exactly `0`
+ structural, plus the hatch-noise `TX-11`). Tiered by aggressiveness:

- **Tier 1 — annotation** (markers, section/drawing symbols, dimensions, labels): ~161 dropped, zero risk.
- **Tier 2 — + furniture/site** (furnishing, garden, landscape, parking): ~270 total.
- **Tier 3 — + openings & room zones** (windows/doors, room zones): 512 total, 494 kept.

## Finding 3 — what a name blacklist CANNOT fix

- `TX-11` is a non-semantic code and its 41 false walls are **hatch fill**: 96.6% of its
  8,468 segments are < 0.6 m. **Decision (2026-06): the anchored substrings `tx-` / `tx_`
  (plus generic `hatch` / `poché`) are now in the default blacklist** — the anchoring
  keeps collateral risk low, and it spares users from hand-deselecting the layer. The
  length/thickness filter (`--wall-min-thickness`, `--wall-min-length`) remains the
  general backstop for hatch on un-named layers; a "skip dense hatch" statistical rule is
  still the more generalizable future fix.
- Parking spaces (~2.5 m apart) are already rejected by the thickness band, not by layer.
- Broad English substrings (`area`, `tag`, `grid`, and short `door`/`window`) carry
  collateral-damage risk on other files. Keep the blacklist conservative + overridable.

## Proposed default blacklist

Case-insensitive **substring** match against the full layer name (so `_Pen_No__N`
suffixes are handled automatically). Hungarian terms (for these ArchiCAD files) +
generic English equivalents (for other CAD apps):

```
furniture:   berendezés, bútor / furniture, furnishing, equipment, appliance, interior, casework, fixture
rooms/zones: helyiség / zone, room stamp, area, space
openings:    ablak, ajtó / window, door            (Tier 3 — real geometry, just not walls)
annotation:  méretez, felirat, címke, jel, metszet, rajz és ábra
             / dimension, text, label, tag, note, title, marker, stamp, symbol, section, grid, leader, callout
garden/site: kert, növény, terep, zöldfelület, parkoló
             / garden, landscape, planting, vegetation, terrain, tree, shrub, lawn, parking, site
textures:    tx-, tx_, hatch, poché, poche          (dense short-segment fills, never walls)
```

Never blacklist terms that appear in structural layers: `wall`, `structure`, `bearing`,
`tartószerkezet`, `vázszerkezet`, `fal`, `partition`, `column`, `pillér`.

## Recommendation

Ship a default-on blacklist applied at **collection time** (skip the entity when its
layer matches) so both `<walls>` and raw `<segments>` come out clean and files stay
small. Make it overridable:

- `--no-default-blacklist` — disable the built-in list.
- `--exclude-layers <substr,...>` — extend it with custom patterns.
- `--layers <names>` (existing whitelist) — precise override that wins when set.

Tier 3 (openings/zones) belongs in the default for now (target is walls), but it's the
tier most worth a toggle — opening geometry will be wanted later for door/window placement.

---

## Cross-app findings (input-examples/)

Same "Backcountry" house exported from several CAD apps, plus the Hungarian ArchiCAD
floors. Tested with default thresholds unless noted.

| File | App | DWG ver | Units | Walls (default) | Recognizable? |
|---|---|---|---|---:|---|
| Backcountry - Chief Architect | Chief Architect | AC1032 (2018) | — | **FAIL** | ✗ converter can't read it |
| Backcountry - Archicad USA | ArchiCAD US | AC1018 | mm | 0 → 27 tuned | ⚠ needs tuning (thin walls) |
| Backcountry - SketchUp | SketchUp | AC1014 (R14) | unitless | 0 | ✗ mesh soup |
| Global_Backcountry-1st Floor | AIA/NCS export | AC1027 (2013) | inches | 68 | ✓ good |
| Global_Backcountry-2nd Floor | AIA/NCS export | AC1027 | inches | 124 | ✓ good |
| Global_Backcountry (combined) | AIA/NCS export | AC1027 | inches | 471 | ✓ good |
| e-01 / e-02 / e-03 (Hungarian) | ArchiCAD HU | AC1018 / dxf | meters | 253 / 284 / 283 | ✓ good |

### Finding 4 — DWG version matters; AC1032 fails

LibreDWG's `dwg2dxf` reads AC1014/AC1018/AC1027 fine but fails on **AC1032 (AutoCAD 2018)**
from Chief Architect ("Invalid zero_18 size / Template section not found"). For newer DWG
we'd need the (free) ODA File Converter as a pre-step, or accept DXF input.

### Finding 5 — units vary wildly; unitless breaks detection

mm (ArchiCAD US), meters (HU), inches (AIA), unitless (SketchUp). Inches/mm/meters all
map correctly (scene size ~13–27 m as expected). **Unitless (SketchUp) maps to 0.4 m** —
wrong — so the thickness band never matches. Detection needs a `--source-unit` /
`--unit-scale` override for unitless files.

### Finding 6 — layer naming taxonomy per app (the junk patterns)

The whole house's walls sit on **one obvious wall layer** in every app except HU ArchiCAD:

- **AIA/NCS** (Global_Backcountry): `A-WALL` (walls), `A-COLS` (columns) = KEEP.
  Junk: `A-DOOR`, `A-DOOR-FRAM`, `A-GLAZ` (openings); `A-FLOR`, `A-FLOR-HRAL`, `A-CLNG`,
  `A-ROOF` (floor/ceiling/roof); `A-GENM` (misc); `Q-CASE`, `Q-SPCQ` (casework/equipment);
  `P-SANR-FIXT` (plumbing); `E-LITE-EQPM`, `E-ELEC-FIXT` (electrical); `M-HVAC-CDFF`
  (mechanical); `S-STRS` (stairs); `G-ANNO-TEXT` (annotation); `A-DETL-DEMO` (detail/demo).
- **ArchiCAD US**: walls on `Twindo - Walls` / `New_Twindo - Walls`, geometry in
  `Wall_<UUID>` blocks. Contains the word **"Walls"** → whitelistable.
- **SketchUp**: `1_WALL_L1`, `2_DOOR_L1`, `2_GLASS_L1`, `2_DOOR_FRAME_L1`, `3_FIREPLACE_L1`,
  `3_FEATURES_L1`, `3_COUNTERS_L1`, `4_ELECTRICAL_L1`, `1_FLOOR_L1`, `1_CEILING_L1`.
  Clean names, but unusable geometry (see Finding 8).
- **HU ArchiCAD**: walls on `0` + `Vázszerkezet - tartószerkezet`; no "wall"/"fal" word.

Key takeaway: **"wall" (and "fal") as a layer substring is a strong POSITIVE signal** in 3
of 4 app families (`A-WALL`, `1_WALL_L1`, `Twindo - Walls`). HU ArchiCAD is the exception
(walls on `0`/structural). So a positive "wall-layers-only" mode is a useful option, but
the blacklist must remain the default since it's the only thing that works for HU files.

### Finding 7 — thin walls expose two threshold problems

ArchiCAD US "Twindo" wall faces are **0.04 m apart** — below the default
`--wall-min-thickness 0.05`, so 0 walls by default. Lowering to 0.02 helps, but reveals a
**coupling bug**: the candidate minimum wall length is hardcoded to `MaxThickness`, so
raising `--wall-max-thickness` shrinks the candidate pool and *loses* walls. With
`--wall-min-thickness 0.02 --wall-max-thickness 0.30` we get 27 walls; the min-length must
be decoupled into its own knob (suggest a fixed ~0.30 m default, independent of max
thickness). AIA walls also run thin (clusters at 0.05/0.11/0.17 m) — a 0.03 m default min
thickness generalizes better than 0.05.

### Finding 8 — SketchUp DWG is not recognizable by vector pairing

74,880 LINE segments, **median length 0.3 mm** — a triangulated 3D mesh flattened to 2D.
`1_WALL_L1` holds a single mesh block. No clean double-line faces to pair. This is the
case for the raster/CV fallback (or re-exporting from SketchUp as 2D), not vector
heuristics.

## Expanded blacklist (covers AIA/NCS + SketchUp + ArchiCAD)

Add to the patterns above. Case-insensitive substring on full layer name.

```
AIA/NCS minor codes (drop): door, glaz, flor, clng, roof, furn, case, eqpm, fixt, sanr,
  plumb, lite, powr, hvac, duct, mech, elec, anno, detl, demo, dims, keyn, iden, strs, hral
AIA disciplines that are never walls (drop whole prefix): P- , E- , M- , G-
SketchUp words (drop): glass, door, frame, fireplace, features, counters, electrical,
  floor, ceiling, plumbing
Keep (never blacklist): wall, fal, a-cols, cols, column, oszlop, pillér, structure,
  structural, bearing, tartószerkezet, vázszerkezet, partition
```

Caution: keep matches win over drop matches (checked first), so `A-WALL` survives even if a
drop code appears elsewhere in the name. Short codes are anchored to avoid collisions —
shipped as `cols` (not `col`), `e-elec`/`e-lite` (not bare `elec`/`lite`), `q-cas` (not `case`).

## Shipped (was: tool-improvement backlog)

Implemented in `converter/wallsegments.go` + `cmd/dxf-to-pdf/main.go`:

1. ✅ **Min-wall-length decoupled** from `MaxThickness` → own flag `--wall-min-length` (default 0.30 m).
2. ✅ **Default `--wall-min-thickness` lowered** 0.05 → 0.03 m (US/thin exports).
3. ✅ **Unit override** `--source-unit mm|cm|m|in|ft` and `--unit-scale <factor>` (scale wins),
   plus a stderr warning when a file is unitless and neither is given.
4. ✅ **Default blacklist** is on by default, applied to **wall detection only** (raw `<segments>`
   keeps every layer). Override with `--no-default-blacklist` / `--exclude-layers`.
5. ✅ **Positive whitelist** `--wall-layers <substr>` fast-path (replaces the blacklist when set).
6. ✅ **Run summary** printed to stdout (scene size, units, segment/wall counts, thickness clusters).

Source of truth: the Go vars `defaultWallBlacklist` and `wallKeepList` in `wallsegments.go` mirror
the pattern lists in this file — keep them in sync. `wallLayerEligible()` enforces keep-wins-over-drop.

Measured effect of the shipped defaults (vs noisy baseline):
- ArchiCAD US: 0 → **27 walls** (thin 0.04 m faces now pass `--wall-min-thickness 0.03` + the decoupled length).
- AIA 2nd floor: 164 → **58 walls** (A-WALL 53 + A-GENM 5; openings/MEP/casework/annotation/stairs dropped).
- HU e-01: junk layers (furniture/zones/markers/windows) gone from `<walls>`; remaining noise is the
  unnamed hatch layer `TX-11` (see Finding 3 — not name-addressable).
- SketchUp: still 0 walls; `--unit-scale` fixes the scene size (0.4 m → ~20 m) but the mesh geometry
  stays unpairable (Finding 8).

### Not addressed here (separate / out of scope)
- **Chief Architect AC1032**: this is **not** a clean "fails to convert". `dwg2dxf` exits 0 but emits a
  degraded HEADER (`Invalid zero_18 size` / `Template section not found`) that dxf-go's strict generated
  header parser rejects (`expected code 70`). `dwg2dxf -m` (minimal header) bypasses it, but the file is a
  **3D model** (205k `3DFACE`, zero 2D LINE/LWPOLYLINE) — there is no 2D plan to recognize. Possible
  future tool change: a `-m` minimal-header retry in `converter/dwg.go ConvertDWGtoDXF`. ODA File Converter
  is off the table (licensing).
- `TX-11`-style hatch noise: kill via length/thickness filters or a future "skip dense hatch" rule, not
  the name list.
- `A-GENM` (general/misc) still yields a few false walls — left in deliberately (a "general" layer can
  legitimately hold wall lines; dropping it risks losing real walls).
