# Ravel Look Mechanics

Ravel is a soft vinyl graph-node timekeeper with a rounded teal body, expressive eyes, a clock-belly face, three connected node nubs, small limbs, and an attached cursor tail. Look direction should read as attention and orientation, not as the whole toy rotating.

Stable anchor: feet, lower body, and clock belly stay registered to the same baseline and scale. The clock belly remains front-readable and should not spin or skew.

Motion mechanism: eyes lead first, eyelids reshape subtly, then the upper body and head surface lean or squash a little toward the target direction. The top node follows the upper body like a soft antenna. Side node nubs shift in visibility slightly but remain attached. The cursor tail stays attached and may lag subtly opposite the head motion, but it must never detach, cross cell boundaries, or become a separate fragment.

Cardinal pose families:
- `000 up`: pupils and eyelids aim upward; top node lifts slightly; face surface stretches upward; mouth remains small and centered.
- `090 screen-right`: pupils, nose/mouth emphasis, and head surface move toward screen-right; screen-right side nub becomes more visible; cursor tail lags slightly toward screen-left while still attached.
- `180 down`: pupils and eyelids aim downward; head/body droops subtly; top node compresses downward; feet and belly stay planted.
- `270 screen-left`: pupils, nose/mouth emphasis, and head surface move toward screen-left; screen-left side nub becomes more visible; cursor tail lags slightly toward screen-right while still attached.

Motion budget: each 22.5-degree step moves the same parts by a small, even amount. Diagonals interpolate between adjacent cardinal families. No cell should look front-neutral unless it is clearly between directions in the ordered loop, and cardinals must be unmistakable at normal pet size.

Avoid: whole-sprite rotation, tilted atlas cells, replacement eyes, googly eyes, detached tail fragments, floating arrows, labels, degree text, shadows, glows, guide marks, or any new prop.
