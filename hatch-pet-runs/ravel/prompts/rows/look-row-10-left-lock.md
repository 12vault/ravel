Create one horizontal look-direction strip for Codex pet `ravel`, atlas row 10.

Use the attached canonical base, approved standard contact sheet, layout guide, approved cardinal strip, and completed row 9. Read `qa/look-mechanics.md`.

Output exactly 8 complete Ravel poses on flat pure magenta #FF00FF, left to right:
`180 down`, `202.5 down-left`, `225 down-left`, `247.5 down-left`, `270 left`, `292.5 up-left`, `315 up-left`, `337.5 up-left`.

Hard screen-coordinate lock:
- Screen-left means the LEFT edge of the image/cell, not Ravel's own left.
- The approved cardinal strip's fourth pose is the model for `270 left`. Match that orientation: Ravel's face stays visible and aims toward the viewer's LEFT edge.
- In every left-direction pose (`202.5` through `337.5`), Ravel's pupils, eye focus, face surface, nose/mouth emphasis, and top-node lean must sit on or aim toward the LEFT half of the head/cell.
- No left-direction pose may have Ravel's pupils, face, or body aim toward the viewer's RIGHT edge.
- Do not create a right-facing side profile. Do not mirror row 9. Do not show Ravel's back.

Direction details:
- `180 down`: frontal/down; eyes and eyelids aim down, face and clock belly visible.
- `202.5`: mostly down with a small screen-left cue; pupils lower-left.
- `225`: clear down-left; pupils and face surface lower-left.
- `247.5`: almost left but still slightly down; pupils left-lower.
- `270`: unmistakable screen-left; face/front visible, pupils and face surface aimed left, matching the approved fourth cardinal anchor.
- `292.5`: left with slight up; pupils left-upper.
- `315`: clear up-left; pupils and face surface upper-left.
- `337.5`: mostly up with a small screen-left cue; one smooth step before row 9's `000 up`.

Draw all eight poses together as one coherent family. Keep the same polished soft-vinyl 3D toy Ravel identity, clock belly, node nubs, attached cursor tail, palette, face, scale, baseline, and lower-body anchor across every pose. The clock belly must remain front-readable in every cell.

Ravel look mechanics: feet/lower body and clock belly stay planted and front-readable. Eyes lead the gaze, eyelids reshape subtly, the head/face surface and top node follow slightly, side nubs stay attached, and the cursor tail stays attached and tucked close. Do not rotate, skew, or tilt the whole sprite.

Layout: eight separated pose groups, one centered in each invisible slot, no overlap, no clipping, no slot-edge contact, no guide marks. Keep generous magenta gaps between neighboring poses so deterministic cropping can recover all eight groups.

Avoid: right-facing side profiles for left labels, back view, hidden face, hidden clock belly, replacement/googly eyes, labels, degree text, arrows, clocks, grids, shadows, glows, scenery, detached effects, detached tail fragments, or chroma-key colors inside the pet.
