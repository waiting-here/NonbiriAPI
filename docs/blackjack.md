# 二十一点 / Blackjack

## 中文

全站共用一张八人牌桌，一人即可开局。每个服务器整分钟的前 15 秒落座，随后 30 秒同时决策，最后 15 秒展示结果。全桌提前结束可提前展示，下一局仍在整分钟开始；无人时不保存空局。

点击“加入队列”会立即预留基础投入，先花游戏积分，再由通用积分补足，并冻结本次金额和费率。按服务器首次有效受理的顺序落座前八人，候补最多 4,096 人。刷新、重复请求和多标签页不会重复排队或改变位置。落座窗口内退出原币退款并递补；发牌后不能退出退款。未落座候补跨轮保留，可以随时离开退款。参与者在整桌结算后须主动重新加入队尾，不保座、不自动续投。

每局重新洗完整六副牌。A 按 1 或 11，J/Q/K 按 10。庄家软 17 停牌，美式底牌预查；底牌在全桌完成决策前隐藏。原始两张 A 加十点牌是自然二十一点，优先于普通 21。所有玩家同时操作，每秒每席最多处理一次，同批按轮换后的座位顺序发牌。未结束的手在截止时自动停牌。

落座阶段即可保存本局随机承诺；底牌和未来牌序只在服务端保存。全桌完成决策并提交结果后可核验种子与洗牌抽样，详见[随机验证说明](game-randomness.md)。

- 要牌：为该手补一张牌；超过 21 点爆牌。
- 停牌：结束该手操作。
- 加倍：额外支付一份基础投入，只补一张即停。余额不足不会改变手牌。
- 分牌：同点值的两张牌可再支付一份基础投入，分为两手；最多分一次。普通分牌后可加倍。分 A 每手只补一张即停，分牌所得 21 按普通胜局处理。

没有保险、投降、五龙或旁注。个人最大总投入为基础金额四倍。默认基础投入为 1,000～50,000，步长 1,000，默认选中 5,000；管理员可调整，新游戏默认关闭。

每手应返总额：失败为零、平局为本金、普通获胜为本金两倍、自然二十一点为本金 2.5 倍。应返总额分别扣平台、低保池和周四池费用，默认各 1%，每项按整数毫积分向下取整。剩余全部发为通用积分，包含返还中的本金；零返还不另外收费。

| 单手基础投入 1,000 | 通用积分到账 |
|---|---:|
| 失败 | 0 |
| 平局 | 970 |
| 普通获胜 | 1,940 |
| 自然二十一点 | 2,425 |

默认规则的长期预期收益为负。牌序由安全随机洗牌决定，不根据玩家输赢调整。旁观者可看公开牌局；在座玩家可发送限频的预设表情，没有自由聊天。页面展示发牌、点数、爆牌及到账变化，减少动态模式保留全部数值。

断线和关页不暂停。重启取消尚未正常结算的整桌，所有投入原币退还且不抽水；候补继续排队，已提交结果不回滚。维护或关闭游戏释放候补及未发牌席位，已发牌局继续完成。被封禁的席位自动停牌；删号去除身份并继续收尾，不影响其他玩家，不能再入钱包的款项按对应积分种类计入外部账户。

本人近期 30 天历史可在页面查阅。之后仅管理员保留去身份的牌局事实；个人导出第八版增加本人安全的排队、付款和牌局记录。导出不含其他人的付款来源、身份或未揭示底牌。游戏使用专属入口插画和发牌、翻牌、加倍、分牌、自然二十一点及爆牌音效；音效默认关闭，在当前浏览器记住选择，进入后台暂停。

## English

One shared table seats up to eight players and starts with one. Each server minute has 15 seconds for seating, 30 seconds for simultaneous decisions and 15 seconds for results. An early finish extends the result display without changing the next minute. Empty rounds are not saved.

Joining reserves the base stake immediately, using game credits before general credits, and freezes the stake and three fee rates. The first eight accepted entries take seats; up to 4,096 wait in a persistent FIFO queue. Retries and multiple tabs cannot duplicate or reorder an entry. Before dealing, seated players may leave for an original-asset refund and the next waiter is promoted. Waiting entries never expire automatically and may leave at any time. After settlement, players explicitly join the tail again; seats and stakes never renew automatically.

Every round shuffles six complete decks. Aces count as 1 or 11 and face cards as 10. The dealer stands on soft 17 and peeks for natural blackjack. The hole card stays hidden until decisions finish. An original ace plus a ten-value card beats an ordinary 21. Each seat submits at most one action per second; batches draw in rotating seat order, and unfinished hands stand at the deadline.

Save the random commitment during seating. The hole card and undealt shoe stay server-side until decisions end and the result commits; the revealed seed and shuffle draws can then be checked using the [verification guide](game-randomness.md).

Hit, stand, double or split equal-value initial cards once. Doubling reserves one additional base stake and draws one final card. Doubling after ordinary splits is allowed. Split aces receive one card each and stand; split 21 is an ordinary win. Unfunded additions leave the hand unchanged. No insurance, surrender, five-card bonus or side bets. Maximum personal exposure is four base stakes. Defaults are 1,000–50,000 in steps of 1,000, with 5,000 selected. Administrators can configure these values; the game starts disabled.

Gross returns per hand are zero for a loss, one stake for a push, two for a win and 2.5 for a natural. Platform, welfare and Thursday fees default to 1% each, independently rounded down to integer milli-credits. The complete net return, including principal, is general credits. A 1,000 stake returns 0 / 970 / 1,940 / 2,425 respectively. Zero returns have no extra fee. Default long-term expected returns are negative; secure shuffling never responds to players' wins or losses.

Spectators see public cards. Seated players can send rate-limited preset emotes; there is no free chat. Dealing, scores, busts and payouts have motion feedback, with all values preserved in reduced-motion mode.

Disconnecting does not pause play. Restart cancels an unsettled table and refunds all original assets without fees, preserves waiting entries and never rolls back committed results. Maintenance or closure refunds waiters and undealt seats while dealt tables finish. Banned seats stand automatically; deleted identities are detached without cancelling others, and unavailable payouts go to the appropriate external asset account.

Personal history remains available for 30 days, followed by administrator-only anonymous game facts. Account export version 8 adds the user's safe queue, payment and game records without other players' identities, funding sources or hidden dealer cards. The game has a dedicated cover and dealing, reveal, double, split, natural and bust effects. Sound starts off, remembers this browser's choice and pauses in the background.
