# Wall / opening export — TODO (Go side)

Companion of `../BuildingDesigner/DXF_WALLS_TODO.md` (C++ side). The two
files share the roadmap numbers **R1–R5**; an item here is `G<R>.<n>`, there
`C<R>.<n>`. Written 2026-10-28 from an analysis of BuildingDesigner's IFC and
gbXML importers, this tool's `--emit-wall-segments`, and the literature.

Tests use synthetic in-code DXF only — never the sample drawings.

## Why

BuildingDesigner's IFC and gbXML imports produce walls **and openings**
(`WallPlan.openings`, placed by `wallplanplacer`). The DXF/DWG import
produces walls only: every door and window gap is bridged shut, here
(`BridgeGap` 1.2 m in `mergeGroup`, `wallsegments.go`) and again in C++.
Yet the drawings carry exact opening data this tool doesn't export:

- door/window INSERTs **nested** in ArchiCAD `Wall_N` blocks (`<elements>`
  lists top-level INSERTs only) — ~100 per HU floor;
- door swing ARCs (dropped unless `--wall-include-curves`);
- opening layers (A-DOOR, A-GLAZ, "Archicad Doors/Windows") — blacklisted
  away instead of used as evidence;
- MLINE (centreline + offsets = a wall) and DIMENSION entities — skipped.

Reproduced failure modes of `detectWalls` (synthetic DXF):

- a wall with a 1.0 m door gap in both faces → one continuous wall;
- two unrelated collinear walls 1.1 m apart → merged into one;
- a long face split by a T partition pairs with only one half (greedy,
  each face consumed once) — 7–17 % of wall-layer face length uncovered;
- gaps > 1.2 m (double doors, garage) split the wall with no record;
- layer `0` hatch patterns → false walls hugging max thickness (0.60 m).

## R1 — Openings end to end (highest impact)

Verified on the samples (2026-10-28): ArchiCAD nests `ajtó 24*` / `ablak
24*` / `Simple Door Opening` INSERTs (layers "Archicad Doors/Windows") and
the `… konszignáció 22*` marker stamps inside `Wall_N_M`, all sharing the
wall's `elem`; door blocks hold ONE arc, two window blocks hold one too;
Revit single doors 1 arc, `Door_2` (double) 2, bifold 0, windows none;
`padló nyílásszimbólum` is a floor symbol (no bare "nyílás" keyword).

- [x] **G1.1 Nested inserts.** `<opening id block layer kind elem depth x y
  corners nameWidth nameHeight>` for every opening-like INSERT at any
  depth: `corners` = block-local bbox (TEXT/ATTDEF/DIMENSION skipped) mapped
  through the instance affine, ordered so c0→c1 is the block +X axis
  (rotation/scale/mirror implied). Kind from name first (door words beat
  window words), then layer (A-DOOR/A-GLAZ/"Archicad Doors"); exclusions:
  konszign/marker/címke/label/tag/text/anno/jel/padló/"floor opening"
  (a bare "floor" would drop every Revit block: "…-1st Floor Plan"), `Wall_*`.
- [x] **G1.2 Opening arcs.** `<arc opening=id|-1 layer elem cx cy r x0 y0 xm
  ym x1 y1>` (three scene points: no angle convention to get wrong under
  mirror + Y-flip) for ARCs inside opening blocks and loose ARCs on opening
  layers, regardless of `--wall-include-curves`.
- [x] **G1.3 Gap records.** `<wall id=…>` and `<gap wall=id t0 t1 x1 y1 x2
  y2>` for every span `mergeGroup` bridges (> 2 cm, ≤ BridgeGap). Wall
  output unchanged. (Bridging only on evidence stays a follow-up.)
- [x] **G1.4 Name sizes.** `nameWidth/nameHeight` from `2_-4_ X 6_-8_`
  (ft-in) and `90x210` / `900x2100` names; "ajtó 24" gives none.
- [x] **G1.5 XML schema bump** to `version="3"` (+ openingCount/arcCount/
  gapCount in `<meta>`); additive only; C++ keeps reading v2.

Done 2026-10-28 (`converter/wallopenings.go`, `wallopenings_test.go`; the
C++ side is `BuildingDesigner/import/dxfopenings`). Measured: an ArchiCAD floor: walls
218 (unchanged), 51 openings (28 doors, 23 windows = the 51 markers), 32
arcs, 45 gaps; a Revit floor: walls 42 (unchanged), 17 openings (11 doors, 6
windows), 11 arcs, 4 gaps.

R1 follow-ups:

- [ ] G1.6 Swings drawn as bulged LWPOLYLINE segments (`ajtó
  oldalvilágítóval`): emit them as `<arc>`s too.
- [ ] G1.7 Bridge on evidence only: skip the blind 1.2 m bridge where no
  opening sits in the gap (C++ now joins wider gaps under an opening itself).

## R2 — Split-pairing (wall detection accuracy)

- [ ] **G2.1 WAG split-pairing** (Domínguez et al., CAD 44(5) 2012; Jaén
  thesis 2014 ch. 6): for each wall-prone pair (parallel, overlapping,
  distance in band) cut both faces at the other's projected endpoints,
  pair the overlapping pieces, return the leftovers to the pool. Replaces
  the greedy "consume both faces" rule. Fixes T-junction loss.
- [ ] **G2.2 Concentric arcs** pair the same way (same centre, Δr in band,
  overlapping angle range) → curved walls (`<wall … arc=…>`).
- [ ] **G2.3 3+ parallel lines** (finish + core): outermost pair = wall,
  inner lines = layer boundaries (emit offsets).
- [ ] **G2.4 Typed leftovers**, à la ArcGIS "Collapse Dual Lines To
  Centerline": unpaired wall-layer faces and unresolved ends emitted with a
  type so C++ can show / use them.

## R3 — Use the structure the file gives

- [ ] **G3.1 Wall bodies per element:** one ArchiCAD `Wall_N` block or one
  closed wall HATCH/LWPOLYLINE = one wall body → midline + thickness from
  its OBB (straight skeleton if not rectangular). Openings nested in the
  block must not stretch the extent (known issue).
- [ ] **G3.2 MLINE → wall** (`confidence="mline"`): style offsets give the
  thickness, the element's justification the midline.
- [ ] **G3.3 Hatch-noise density rule**: dense families of short parallel
  lines (hatch exploded into lines, layer `0`) are pattern, not walls.

## R4 — Thickness per file

- [ ] **G4.1** Mode of the distance from door/window insert points along
  the insert normal to the nearest wall face (Domínguez §5.2.1) → adapt the
  `[min,max]` band per file; emit `<thicknessHint>`s.
- [ ] **G4.2** Real thickness clusters (not just the CLI top-5 print):
  emit them; optionally snap walls within ±25 mm.

## R5 — Smaller items

- [ ] **G5.1** DIMENSION definition points + measurement as `<dim>`.
- [ ] **G5.2** Single-line walls: heavy-lineweight long lines on a wall
  layer → `confidence="single"` with no thickness (C++ decides).
- [ ] **G5.3** Extend centrelines by t/2 at L corners (C++ repairs today).
- [ ] **G5.4** Align defaults/blacklist with C++ (`isNoiseLayer`, 0.08 /
  0.50 / 0.5 there vs 0.03 / 0.60 / 0.30 here) — one documented source.

## References

- Domínguez, García, Feito — CAD 44(5) 2012, doi:10.1016/j.cad.2011.12.009;
  thesis https://hdl.handle.net/10953/6402 (WAG, block OBB anchoring,
  endpoint clustering + line growing).
- Wu, Meng, Liu — Traitement du Signal 35 (2018): opening-type gap repair,
  loop self-check.
- BeamUp patent US10997325B2: layer statistics, offset-union rooms,
  arc + leaf doors.
- ArcGIS Collapse Dual Lines To Centerline (typed leftovers contract).
- Chief Architect CAD-to-Walls (±25 mm wall-type matching).
