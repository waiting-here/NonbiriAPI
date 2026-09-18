# Local visual assets

`nonbiri-mark.svg`, `state-empty.svg`, `state-error.svg`,
`state-maintenance.svg`, the inline SVG paths in
`web/src/shared/components/Icon.tsx`, and the six WebP illustrations in
`game-heroes/` are original, project-created visual assets for NonbiriAPI.
The game illustrations were generated with assistance from ChatGPT, then
selected and prepared for this project. They contain no embedded third-party
artwork, fonts, or runtime network dependency and are distributed under the
repository's AGPL-3.0 license. The files are bundled by Vite and may be used
offline.

The blue fat fish easter-egg illustration adapts the local chibi character into
a white-rice-themed scene with a transparent background. Like the game illustrations
above, it contains no bundled third-party image, font, or runtime network
dependency and is distributed under the repository's AGPL-3.0 license.

Visual research for the game illustrations included the community projects
[DeepSeek Whale-chan](https://github.com/Neko3000/deepseek-whalechan) and
[Every Token You Spend Comes Back as a Waifu](https://github.com/guihui2538/Every-token-you-spend-comes-back-as-a-waifu.).
No source image from either reference repository is bundled here.

Configured site logos remain operator-supplied public HTTPS URLs and are
loaded with anonymous image requests; they are not part of this asset set.

Likes Battle bundles 127 approved character, skill, harness, cast, ending, and
status slots as local WebP derivatives. Each derivative preserves the complete
transparent silhouette and aspect ratio; slot metadata retains a logical source
identifier and crop focus. Fishing artwork bundles 35 catch illustrations,
including the unknown catch, as local WebP derivatives and keeps a code-native
SVG fallback for image load failures and high-contrast display.
