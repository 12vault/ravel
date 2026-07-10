Create one horizontal four-cardinal anchor strip for Codex pet `ravel`.

Use the attached canonical base, completed standard contact sheet, and layout guide for identity only. Use the layout guide for four invisible slot centers and spacing only; do not fill the safe area.

Output exactly four centered complete full-body poses in this exact left-to-right order: `000 up`, `090 screen-right`, `180 down`, `270 screen-left`. Screen-left and screen-right always mean viewer/image coordinates.

Scale and padding are the top priority:
- Draw Ravel deliberately compact: the visible pet should be about 120 pixels tall inside each 192x208 slot.
- Keep all body parts, nubs, feet, hands, top node, and cursor tail inside an imaginary 132x150 box centered in each slot.
- Leave large plain magenta margins: at least 30 pixels left, 30 pixels right, 24 pixels top, and 24 pixels bottom in every slot.
- Tuck the cursor tail behind or beside the body; it must stay close and attached, never long or reaching outward.
- Keep side nubs close to the body and reduce their spread so they cannot approach slot edges.
- If a direction would make the body wider, reduce the lean and show the direction mostly through eyes, eyelids, face surface, and top node.

Ravel identity to preserve: rounded teal graph-node body, top and side node nubs, simple clock belly without numbers, tiny cursor-shaped tail, expressive eyes, coral/charcoal/warm-white accents, polished soft vinyl 3D toy style. Keep the same face and material as the canonical base.

Direction semantics:
- `000 up`: frontal, eyes up, top node slightly lifted.
- `090 screen-right`: eyes and face surface aim screen-right; tiny upper-body lean right; right nub slightly fuller but close to body.
- `180 down`: eyes down, eyelids lowered, body droops slightly.
- `270 screen-left`: eyes and face surface aim screen-left; tiny upper-body lean left; left nub slightly fuller but close to body.

Do not rotate, skew, or tilt the whole sprite. Do not add replacement eyes, labels, degree text, arrows, boxes, guide marks, shadows, scenery, detached effects, or chroma-key colors inside the pet.
