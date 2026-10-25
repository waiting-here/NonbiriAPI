# Card artwork and presentation standard

This standard applies to every existing and future Gacha Simulator card. Rarity determines presentation requirements, not the quality of the underlying illustration. The [activity rules](gacha.md) define acquisition and collection behavior.

## Content classification

`internal/artwork/catalog.json` is the shared Go and browser classification
registry. Every card, illustration, cover and appearance has an explicit
`general` or `r18` rating. Sweet Hide-and-Seek's two cards, theme illustration
and pool cover are `r18`, including all their derivatives. Asset ownership
rules cover the corresponding previews, full-size images, layers and variants.
Shared textures have one owner, and artwork that reuses them declares `uses`
dependencies so its effective rating includes those sources. Card `scene`
references additionally govern heartfelt-moment backgrounds.

New artwork must register its files or literal path prefixes and pass catalog
coverage validation. Keep default appearance and fallback cover general. The
administrator's R-18 switch controls both rendering and direct asset delivery;
candidate labels are separate from the artwork, so allowed detail views and
applied appearances remain unobstructed.

Sweet Hide-and-Seek retains two animated SSR character portraits over static
backgrounds. Its group illustration and independent pool cover stay static;
the theme grants no SP site appearance. The character-free backgrounds and
retained foregrounds share the theme's R-18 classification, including standard,
thumbnail and high-resolution WebP exports. Its adult artwork is included in
the public source distribution and remains disabled by default in the product.
Heartfelt moments retain each card's aligned static scenery behind the animated
character so the hand-to-door and foreground contact points remain supported.

## Artwork and identity

- Use the established character references. Preserve identifying hair, eyes, anatomy, accessories and signature motifs across outfits; create an original scene for each card.
- Keep a clean, textless, frameless 2:3 portrait master, at least 1024 × 1536 native pixels. Retain a higher-resolution original when its detail is reliable. Upscaling does not count as added source detail.
- Display names use “Theme · Character” (Chinese: “主题·角色”). Render names, rarity, counters and borders in the interface.
- Use the full-size girl designs for ordinary themes. Explicitly designated chibi themes may use the corresponding chibi references.
- All rarities require finished composition, expressive faces, sound anatomy, clean edges, coherent lighting and distinct outfits. Keep important features readable in thumbnails.
- Trace every visible limb from shoulder through elbow and wrist before approving its hand. Check handedness from the character's anatomy and the visible palm/back orientation, then count one thumb and four fingers. Verify foreshortening, joint direction and occluded digits at full size. Match palm width, finger thickness and finger spread to the character's build, while retaining plausible perspective enlargement. Apply the same review to cards, companion illustrations, both wallpaper variants and every animation pose. A hand correction must preserve correct limbs and propagate to animation layers and static fallbacks.
- Inspect enlarged crops of distant hands, fingertips and partially hidden limbs before reviewing the full composition. Trace each digit from its own root through its joints; five visible tips alone do not prove correct anatomy. After a correction, review the registered composite again before exporting derivatives. If a hand still reads as unnatural, revise its pose or use coherent clothing/foreground occlusion within the approved scope; a technical explanation of the anatomy cannot substitute for a convincing image.
- Retain references, source images, layered animation inputs, authoring parameters, licensing and adoption status in the maintained asset library. Ship optimized derivatives with applicable notices.

## Layered production

Create the character-free theme background first. Author transparent character foregrounds against that background, then compose the card, companion illustration and pool cover from the retained layers. Independent compositions may retain connected props in their foreground layer to preserve contact and occlusion. Keep registration, alpha, source references and composition parameters with the artwork; flattened output is a delivery derivative.

Inspect transparent corrections on light and dark neutral backgrounds and on the actual scene. Check corners, finger gaps, fine hair and translucent fabric for unwanted opacity, dark haze, patch seams and duplicated outlines. A transparent preview alone does not establish clean alpha. Keep accepted and rejected revisions distinct so a later export cannot restore an earlier defect.

Existing N/R/SR cards use separate theme backgrounds and character layers. Existing group illustrations and covers retain their original cast compositions as registered foreground layers. Existing SSR/SP character, eyelid and scene layers remain reusable. The asset composition manifest records the inputs and placement. Keep the former flattened masters in the local archive.

SSR card backgrounds stay static while their characters animate. SP also requires coordinated background animation and the complete dedicated site appearance. Heartfelt moments uses a static theme background for every selected card, including SP, and only animates SSR/SP characters.

## High-resolution delivery

Target 2560 × 3840 portrait derivatives and 3840 × 2160 landscape derivatives for desktop appreciation, including animation layers. Keep native masters and enhancement settings separately. Enhanced derivatives improve display clarity but must not be described as new native detail. Inspect faces, fine jewelry, patterned cloth, transparent edges and painted textures before adoption; reject oversharpening and invented features.

Use responsive image candidates for full illustrations and pool covers. Continue using small static thumbnails in lists. Size animation canvases to the displayed physical pixels, with a bounded resolution budget, so playback does not reduce a sharp still to a low-resolution canvas. Load the larger textures only when the display needs them. Review both static and animated artwork in a 3840 × 2160 viewport.

## Rarity requirements

| Rarity | Artwork | Draw presentation |
| --- | --- | --- |
| N / R | Complete static illustration | Brief automatic reveal with clearly readable rarity |
| SR | Complete static illustration | Stronger reveal; wait for interaction |
| SSR | Layered animated illustration, static fallback and pause | Full-viewport spectacle and individual close-up; wait for interaction |
| SP | Individually composed character scene, continuously animated background and coordinated character motion; static fallback and pause | More prominent spectacle than SSR, distinct rarity treatment and close-up; wait for interaction |

N and R continue automatically. SR and higher wait for a click on the presentation surface or Continue, while their artwork keeps moving. First-acquisition SSR/SP scenes keep Skip, close, Escape and Continue unavailable until their full dialogue is visible. After all required encounters finish, Skip reaches the final results. Owned SSR/SP cards offer both encounter replays, which remain freely closable. Ten-pulls end with all ten cards visible; duplicate cards additionally animate their conversion into shards. Music, sound effects, supported-device vibration and reduced motion remain controllable.

## Animation quality

Use continuous, restrained motion: breathing, natural blinking, slight head/shoulder movement and flowing hair or fabric. Motion must be noticeable promptly at actual phone display size. Increase existing motion amplitudes when needed; do not add unrelated gestures merely to signal rarity.

SSR character animation uses a smooth, seamless loop over a static background with a stable face and intact occlusion edges. Match the approved visual quality on each illustration; a single multiplier is not a substitute for reviewing individual artwork.

SP also animates the scene during both the draw close-up and catalogue appreciation. Choose movement from its imagery: celestial light and star drift, for example, or a gliding creature and moving cloud currents. Coordinate direction, phase and amplitude with the character. Preserve architectural lines, facial geometry and readable silhouettes; avoid whole-image wobble, edge gaps and abrupt resets. Background light should support the portrait.

Keep loops continuous in both position and velocity. Preserve a complete static composition when reduced motion is selected, rendering is unavailable or loading fails. Pause freezes the whole scene, and hidden pages suspend animation. Stop and release animation resources when the artwork closes. Lists use static thumbnails.

## Themes and companion illustrations

Ordinary themes contain the established five-character set with a common rarity and art direction. Every SP theme contains one character card; obtaining that card completes its theme. Published membership is immutable; additions belong to new themes.

Every theme has a separate 16:9 appreciation illustration. Ordinary themes use a coherent group scene based on the five approved portraits; SP uses a dedicated scene for its single character. Do not substitute a row of portrait crops. Show a blurred preview and collection progress before completion; show the full clear illustration immediately on completion. Ticket, credit and donation-credit rewards are claimed separately.

Pool covers use independent compositions and safe desktop/mobile crops. They do not expose the complete locked appreciation illustration or bake configurable pool names into the image.

## SP site appearances

Each SP card includes one unlockable site appearance with coordinated light and dark variants, two finished animated background compositions, responsive cropping and modest UI styling. Preserve layout, text and form readability, keyboard focus and state contrast. Game scenes retain their own visual identity. Non-default appearances provide a top-bar appreciation control that hides the interface without discarding page state. Keep an accessible return control, support Escape, restore focus and respect reduced-motion preferences.

Build large wallpaper scenes from complete background plates and separately composited characters, open/closed eyes, clouds and moving objects. Reconstruct scenery hidden behind a moving object so it can travel across the viewport without dragging or revealing a hole. Celestial scenes use layered clouds, orbiting celestial bodies, drifting stars and water currents; wandering scenes also include a complete whale travelling across the screen with coordinated fin and tail movement.

Keep scene depth explicit: distant stars and meteors, opaque celestial bodies, atmospheric clouds, architecture and hanging ornaments, characters, then near plants where the composition calls for overlap. Painted clouds need the same foreground occlusion as moving cloud layers. Derive registered masks when a complete plate contains several depths; reuse its original colour detail so extraction does not add doubled edges. Inspect the full travel path behind arches, fine chains and translucent cloud edges.

Keep water highlights inside their owning scenery material and depth mask. Do not repaint opaque rectangles of the background after moving objects. Verify overlap while creatures cross every water region and while celestial bodies cross clouds and ornaments, including wide desktop and tall phone crops.

Use brief, infrequent meteors and low-intensity light travelling along actual star decorations. Confine a light breeze to nearby foliage tips, with stems and masonry anchored. Keep these accents quieter than the characters. Creature translation should feel unhurried on both desktop and phone, with a coherent, independent swimming stroke.

Keep the wallpaper portrait rigid except for explicitly authored free hair and trailing fabric. Use finite regions with zero weight outside, feathered edges and pinned roots; review the weight overlay on every illustration. Faces, hands, limbs and fitted clothes must stay fixed. Broad body fields and travelling sine waves create a rubbery silhouette even at low amplitude. Breathing or head turns require separate anatomical constraints before adoption.

Large-scene motion must follow the depicted object. Keep scenery, architecture and celestial silhouettes rigid. Move cloud layers independently; confine flowing highlights to authored water regions. Articulate a swimming creature through its tail stock, flukes and fins with a shared stroke and delayed follow-through. Wrap travelling objects only after their entire silhouette, including the motion envelope, has left the viewport.

Blinking needs continuous intermediate eyelid shapes, not an opacity dissolve between open and closed eyes. Fit separate lid and lash contours to each eye, including its angle and perspective. Restrict texture remapping to the lash ribbon; eyebrows, nose, cheeks, iris and catchlights stay at their painted coordinates as the lids cover the opening. Check fixed-time open, half-closed and closed frames: skin outside the authored eye opening must not move or change. Ease both ends of the blink to avoid a one-frame jump. Use an even rendering cadence with fractional time carried between refreshes; review normal playback and intermediate poses on different refresh rates. A high average frame rate alone does not demonstrate smooth motion.

Draw moons as complete opaque spheres with a coherent light direction and a soft day/night terminator. Ordinary phases describe the visible sunlit portion, not a transparent cutout or an eclipse. Background stars and farther bodies cannot show through the unlit hemisphere. Preserve the intended fantasy scale: oversized bodies and dramatic depth are welcome when silhouettes, lighting and occlusion remain coherent. Use slow, continuous paths. Separate painted glow from the solid surface: never map a baked halo onto the sphere as a thick shell. Let silver and gold light bloom softly from the illuminated edge into the sky, with no bright outline around the dark hemisphere. Keep luminous surface detail and the intended fantasy grandeur. Update the static fallback whenever the composition, lighting or occlusion changes.

Animate both light and dark wallpaper variants with coordinated free hair and fabric motion plus movement native to the scene. Add breathing only with the anatomical constraints described above. Keep faces, hands, architecture and the interface stable. Use a seamless loop, preserve the original responsive crop, and avoid whole-image wobble. Cap rendering work, suspend hidden tabs and release resources when switching appearance or leaving the account. System reduced motion and rendering failures show the complete static painting. Review a full loop at desktop and phone sizes in both color modes. This requirement applies to future SP appearances as well.

Keep the same component structure in light and dark appearances. Use softly tinted, blurred surfaces with diffuse edges and a more opaque reading area; inputs, statuses, dialogs and keyboard focus must remain distinct over every part of the moving scene. Review forms, tables and dialogs while the brightest and darkest background regions pass behind them.

Acquisition unlocks the appearance immediately, including shop redemption. Selection remains voluntary in Appearance and follows the existing light/dark/system preference. SP keeps the SSR draw tier and shards; rarity styling must not imply a separate initial probability tier.

## Acceptance for additions

Review the original, thumbnail, close-up and complete theme together. Inspect faces, hands, accessories, character identity, layer edges and crop safety. Watch a complete animation loop and its seam at normal speed; inspect motion on a phone-sized card, including pause and static fallback.

Check the production components at desktop and phone sizes, in light and dark modes. Review readable rarity, SR-or-higher hold, skip, duplicate conversion, theme unlock and SP appearance selection. Asset availability, automated checks and a still screenshot do not replace watching the actual motion.


## Copy

Every card has its own bilingual character voice. Use an established personality and at most one gentle joke. Express affection plainly and make room for relaxed, intimate everyday moments. Keep warmth specific to the scene: sharing food, holding hands, a date or an invitation. Familiar character motifs may appear sparingly; do not turn every voice into a joke about AI or computing. Never belittle users or other models.

N/R lines are light and everyday; SR grows more personal; SSR and SP can express love directly. SP connects its imagery to a concrete wish to be close to the player, with an intimate second paragraph. Avoid repeated ellipses, em dashes, coy denials, slogan-like contrasts and generic promises. Vary sentence lengths, openings and punctuation across the entire catalogue. Keep every line suitable for all ages. Each paragraph is at most 40 Chinese characters or 110 English characters and occupies no more than three lines in the phone close-up. Review both languages and the actual narrow-screen wrapping before adoption.

Keep voices in the presentation catalogue, separate from theme taglines. Show individual lines in SR focus, SSR/SP close-ups and owned-card appreciation; use theme taglines for theme memories. New SP encounters include a short prelude and a delayed second paragraph. Reunions use a distinct line without replaying the prelude. New voices require editorial approval before final adoption.

Ten-pulls share one card board and reveal in receipt order. SR focuses within the board; SSR/SP charge before their full-screen close-up. Optional card-back hints apply only to SR and higher. Turning hints off keeps the portal neutral and all unrevealed backs identical. Pausing or hiding the page retains scene time and freezes artwork; skipping shows the unchanged complete receipt without another draw once first encounters finish. Reduced motion presents the complete static first-encounter dialogue before the receipt.

### Eyelids and animation timing

SSR and SP card eyes have separate contours calibrated to their open and closed layers. Animate the lash ribbon while keeping the iris and surrounding face at fixed source coordinates; do not blend an oval face patch. Keep glasses, hair roots and nearby ornaments outside the moving eyelid. Validate the open, partial and closed poses for every new card, at normal playback speed as well as enlarged.

Cards and appearance wallpapers share the blink envelope and phase-preserving frame cadence. Use elapsed time, preserve fractional frame budgets, pause while hidden and avoid a jump on resume. Check the actual painted frame rate in a regular browser. A smooth timing curve does not compensate for a low refresh rate or misaligned artwork.

For layered scenery, establish the far-to-near order before adding motion. Apply water highlights inside their own scenery layers, preserve rigid architecture and celestial silhouettes, and inspect overlap at both extremes of the movement. Static fallback and high-resolution variants must use the same accepted composition.

## Additional SP content

Across the Mirror Sea uses mirror-edge light and reflected scenery; Amber Vow uses refined portrait and eyelid registration; Shared Daylight keeps its offered hand settled; A Notice to Steal Your Heart keeps its head and arms steady while breathing, hair and cloth continue. Each includes standard and enhanced stills, matching dedicated wallpapers and separate heartfelt-moment scenery/character derivatives. These scenes contain no encounter videos or additional background music. See the [asset notice](../web/public/assets/gacha/NOTICE.md) for specific clothing and content guidance.

Amber Vow uses separate hall, banner and chandelier plates, with anchored cloth motion, rigid suspended fixtures, moving window light, layered gold dust and foreground books. Its existing 14-bone character and 17.6-second motion remain. A localized footwear texture and separate sole shadows establish floor contact without replacing the face, hands or eye plates. The desktop appearance places the face in the right margin independently of the card composition; compact screens use a separate placement below navigation. Static scenery and character layers share that placement, including when motion is reduced or unavailable. Hall enhancement and transparent-layer resampling are identified separately in the asset notice.

Shared Daylight pairs warm dawn with deep-blue predawn light on the existing platform. A Notice to Steal Your Heart pairs moonlight with candlelight, with its balance and floor shadows anchored to the supporting foot while retaining the raised-foot pose. Across the Mirror Sea adds localized motion and reflected light within the existing mirrors and stone scenery. Each has separate responsive wallpaper layers and matching static fallbacks. Standard and enhanced output share registered character inputs; resampled backgrounds are identified in the asset notice. The moonlit-room background canvas scales with the display between the base and source resolutions.
