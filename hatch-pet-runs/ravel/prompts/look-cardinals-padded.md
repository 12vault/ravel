Create one horizontal four-cardinal anchor strip for Codex pet `ravel`.

Use the attached canonical base, completed standard contact sheet, and layout guide for exact identity, style, scale, baseline, face construction, materials, palette, markings, props, and spacing. Read `qa/look-mechanics.md` and use Ravel's natural gaze mechanism.

Output exactly four centered complete full-body poses in this exact left-to-right order: `000 up`, `090 screen-right`, `180 down`, `270 screen-left`. Screen-left and screen-right always mean the viewer's image edges, never the character's own left or right.

Critical geometry:
- Keep Ravel smaller than the layout guide's safe area, about 80 percent of the cell height.
- Leave at least 24 pixels of pure magenta padding on the left and right side of every invisible slot.
- Leave at least 18 pixels of pure magenta padding above the top node and below the feet in every slot.
- Keep the cursor tail tucked close to the body so it never approaches a slot boundary.
- No pose, node nub, foot, hand, tail, highlight, or antialiasing may touch or approach a slot edge.

Direction semantics:
- `000 up`: broadly frontal face, pupils and eyelids aim toward the top edge, top node lifts slightly.
- `090 screen-right`: pupils, face surface, and upper body aim toward screen-right; screen-right side nub reads fuller; cursor tail lags slightly left but remains tucked and attached.
- `180 down`: pupils and eyelids aim down, head/body droops subtly, top node compresses downward.
- `270 screen-left`: pupils, face surface, and upper body aim toward screen-left; screen-left side nub reads fuller; cursor tail lags slightly right but remains tucked and attached.

Keep scale, feet/base, lower body, and registration consistent across all four slots. Do not rotate, skew, or tilt the whole sprite to fake gaze. Do not add replacement eyes, labels, degree text, arrows, boxes, guide marks, shadows, scenery, detached effects, or chroma-key colors inside the pet.
