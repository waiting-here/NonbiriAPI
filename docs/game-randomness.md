# Game randomness and hidden information / 游戏随机性与隐藏信息

## 中文

六款游戏的新局均使用服务器独立生成的 256 位秘密种子。进入一局后，在“随机性核验”中可以保存 SHA-256 开局承诺；整局结束后公开种子和抽样记录，并提供本机核验和 JSON 下载。源代码和浏览器中都没有进行中对局的种子。暗牌、未来牌、对手未揭示动作由服务端投影过滤，修改前端变量或请求中的阶段值不能提前取得它们。

二十一点在全桌结束决策、结果已经提交后披露；竞标对决、回合制对战小游戏（测试）、石头剪刀布和连连看在整局终结后披露。回合制对战小游戏（测试）也有随机目标选择，因此纳入。钓鱼在整个批次结算后披露，与页面播放动画无关。即时钓鱼的承诺和种子可能一起出现在首次结果中，不能将其视为投入前已经独立保存的承诺。系统取消后不会继续使用这局种子，可随取消结果披露。旧局没有种子，显示不可追溯核验。

普通用户可以读取本人当前和最近 30 天对局的凭证。二十一点旁观者仅能读取当前场次的公开承诺和终局凭证，之后的历史限实际参与者。维护期间沿用各游戏原有的继续读取权限，未满足条件时需等维护结束。所有响应禁止缓存。个人导出第九版包含 `randomness`：进行中仅含承诺，结束后含完整凭证。凭证随对应历史清理；去身份长期历史不含种子、承诺或凭证。

终局（含取消）公开种子后，可重建本局完整的竞标奖励牌堆或二十一点牌鞋，包括没有使用的牌。普通游戏历史和游戏导出字段不直接列出这些牌，但独立凭证及导出中的 `randomness` 足以进行重建。进行中的牌序仍保密，其他玩家的身份、支付来源和其他局的秘密不会随之开放。

随机种子不用于会话、加密 nonce、身份、防作弊令牌、游标、匿名化或其他对局。知道一局的公开种子不能推导其他局的种子。开局承诺使已经保存承诺的玩家能发现终局更换种子；它不证明可信运营者从未事先筛选种子，也不证明所有日志完整或单局必定获利。

## English

New games use independent 256-bit secret server seeds. The game page offers an opening SHA-256 commitment, local verification after disclosure, and a downloadable JSON proof. Active seeds never enter browser responses. Server projections also withhold dealer holes, undealt cards and opponents' unrevealed choices; client state and phase parameters cannot authorize disclosure.

Blackjack reveals after the whole table's result commits. Bidding, Likes, RPS and LinkLink reveal only after the entire match ends, including cancellation. Likes includes random target selection. Fishing reveals after the whole batch settles, independently of frontend animations; an instant batch can return commitment and seed together, so it offers no independently observed pre-stake commitment. Legacy games are explicitly unverifiable.

Proofs are owner-scoped and retained for up to 30 days. Blackjack spectators can read only the current round's table; later history requires actual participation. Existing maintenance continuation restrictions apply. Responses are `no-store`. Account export v9 includes `randomness`, with only public commitments for active games. Proofs disappear with the corresponding retained parent; long-term anonymous archives omit seeds and commitments.

Once a terminal seed is disclosed, including after cancellation, the complete Bidding reward decks or Blackjack shoe can be reconstructed, including unused cards. Ordinary game history and game export fields do not list those cards, but the separate proof and exported `randomness` allow reconstruction. Active deck order remains secret; disclosure does not expose other players' identities, funding sources or other games' secrets.

Seeds never derive login tokens, encryption nonces, other games, cursors or anonymization keys. Saving the opening commitment lets a player detect a later seed substitution. This is not a proof that a trusted operator never selected among seeds, that the transcript is complete, or that a player will profit.

## Wire protocol

`GET /api/games/{game}/randomness/{id}` accepts no query parameters or body, requires a live user session, and returns `{"proof": object-or-null}`. `null` means no historical proof exists. Unknown/unowned/expired resources are 404. Game names are `fishing`, `linklink`, `rps`, `bidding`, `likes`, `blackjack`. Responses are bounded by 2 MiB plus the envelope. There is no CallerKey or administrator automation scope for this endpoint.

The active object has exactly `algorithm`, `game`, `resource_id`, `rules`, and `commitment`. Terminal objects additionally contain the lowercase 64-character hex `seed`, and optionally `streams:[{label,samples}]`. Empty transcripts may omit `streams`. A stream contains canonical standard Base64 of consecutive 16-byte records: unsigned big-endian 64-bit bound, followed by unsigned big-endian 64-bit result. At most 65,664 samples and 1,024 stream labels are allowed. This covers the existing 65,536-draw Fishing decoration budget plus economic draws, and the maximum current LinkLink generation and hint budget.

Algorithm version: `hmac-sha256-reject64-v1`. All text below is UTF-8, restricted to printable ASCII without spaces; `NUL` means one zero byte. The rules identity is also bound into the commitment.

```text
domain(kind) = "nonbiri/game-random/hmac-sha256-reject64-v1" + NUL
             + kind + NUL + game + NUL + resource_id + NUL + rules + NUL
commitment   = hex(SHA256(domain("commit") || raw_seed))
word         = BE64(first_8_bytes(HMAC_SHA256(raw_seed,
                 domain("draw") || label || NUL || BE64(counter))))
```

Each stream starts at counter zero. For a bound `n > 0`, reject words below `2^64 mod n`, advancing the counter for every attempt; the accepted index is `word mod n`. This removes modulo bias. At most 128 rejection attempts are allowed per sample, failing the transaction if exhausted. Streams resume from all previously recorded draws, including rejected-word consumption. A draw limit or entropy failure never silently falls back to another source. Seed/commitment identity and all game facts commit atomically; retries cannot reroll a committed opening.

Production seeds use Go's [cryptographically secure system source](https://pkg.go.dev/crypto/rand). The primitives are [HMAC](https://www.rfc-editor.org/rfc/rfc2104) and [SHA-256](https://csrc.nist.gov/pubs/fips/180-4/upd1/final); the domain/counter and draw transcript above are this application's versioned protocol.

## Independent replay

With Node.js and this repository, no credentials or network are needed:

```sh
node scripts/verify-game-randomness.mjs final-proof.json SAVED_OPENING_SHA256
```

The optional final argument is the commitment saved while the game was active. Without it, the tool checks internal seed/commitment consistency only. Exit 0 reports the verified sample count and decoded bounds/results; any mismatch exits nonzero. The browser's WebCrypto verifier is a separate implementation with the same pinned test vector. For independent audit, use a trusted local checkout rather than relying solely on JavaScript delivered by the server being audited.

The transcript verifier checks the supplied samples. To verify the complete game outcome, also reconstruct the expected stream labels, draw counts, bounds, frozen rules/configuration and revealed player actions using the corresponding engine. An omitted sample or a changed bound is not inherently authenticated by a seed commitment; it must be rejected by game replay. No verifier treats a client-generated win as authority to change a wallet.

| Game | Stream and replay inputs |
|---|---|
| Blackjack | Rules `six-decks-s17-v1`, stream `shoe`; six decks in the engine's canonical order, then descending Fisher–Yates bounds 312 through 2. Reuse the shuffled shoe with the recorded rotating seat order and accepted action batches. |
| Bidding | Rules `mode/content_hash`; `seat-order` selects the initial color assignment, `initial` drives the engine's two reward-deck shuffles. Replay revealed bids/joker choices for the thirteen rounds. |
| Likes | Rules `mode/content_hash`; `seat-order` and `round/<number>` for random target selection. Replay the frozen catalog, both loadouts and each revealed plan; server round records include target-selection facts. |
| RPS | Rules `rules_version/mode`; `seats-and-dealer` shuffles seats and chooses dealer. Timed-out gestures use an indexed draw described below, without growing a transcript. Player choices are separate authenticated game inputs; encryption nonces never use game seeds. |
| LinkLink | Rules `rules_version/spec`; `initial` drives geometric board generation. `hint/<revision>` drives the current one-pass reshuffle; `automatic/<revision>` is reserved for older automatic reshuffles. The engine verifies the generated solution and all actual moves independently. |
| Fishing | Rules `rules_version/bait/count`; `catch` first selects all economic outcomes using frozen batch prices/RTP, then decorative Easter-egg outcomes. Net returns additionally require the batch's frozen fee terms. |

Fixed compatibility vector: game `blackjack`, resource `bjt_AAAAAAAAAAAAAAAAAAAAAA`, rules `six-decks-s17-v1`, seed consisting of 32 bytes `0x2a`. Commitment is `5b14b84868c9d5d5c346ad5ee4bea248526db7e6fa1ac8916a40c6c3f28f4260`. The first `shoe` draw with bound 312 yields 262; the sample record is `AAAAAAAAATgAAAAAAAABBg==`.

RPS automatic gestures use the same rejection procedure with `domain("indexed")`, label `automatic/<seat>/<phase_seq>`, bound 3, and a fresh counter starting at zero for each label. Seat is 0–2 and phase is its canonical unsigned 128-bit decimal value. Indices 0/1/2 mean rock/scissors/paper. The phase is the gesture-lock phase, before dealer/follower phases. This stateless derivation keeps long deathmatches bounded without a new gameplay turn limit. Use the saved public phase and terminal proof:

```sh
node scripts/verify-game-randomness.mjs final-proof.json SAVED_OPENING_SHA256 --rps-action 0 1
```

三人猜拳的自动出招按公开的座位与手势锁定阶段序号独立推导，不累计无限长的抽样轨迹。使用上述命令可核对指定自动出招；玩家手动选择不由种子决定。
