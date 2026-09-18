# Two-player games

[简体中文](#双人游戏)

Bidding Duel and Turn-based Battle Minigame (Test) use separate two-player queues. The game, mode,
entry price and three fee rates shown before joining are fixed for that entry.
You can play one of each game at the same time, but cannot join two modes of the
same game. Both new games start disabled and have no newcomer award.

Game credits pay first, then general credits. A negative wallet does not reduce
the positive funds in the other wallet. Cancelling a queue, waiting 120 seconds
without a match, a draw, or a system cancellation returns each entry to its
original wallet. On a decided match, the winner receives their original entry
back plus the loser's entry after fees as general credits. Each platform,
welfare and Thursday fee is rounded down separately. Surrender is a loss.
Disconnecting leaves the server timer running. A server restart, account ban or
account deletion cancels an unfinished match; deleting a user cannot recreate
that user's wallet. Displayed game points are separate from spendable credits.

## Bidding Duel

Each player receives a shuffled hand of ranks 1–13. All 13 rounds reveal one
reward rank from each player's separate shuffled reward deck. The first 12
rounds alternate the dealer; each player is dealer six times. A dealer holding
a joker has 10 seconds to use or save it. A used joker doubles only that
player's reward for the current round, and does not double carried rewards.

Both players then have 20 seconds to lock one card. Cards remain hidden and in
the hand until both choices are locked or the deadline expires. Timeout selects
the smallest remaining card. Higher rank wins the current rewards and carry;
a tie carries rewards into the next round. Final-round ties discard the carry.
The joker phase is skipped when unavailable and in round 13. Highest total
points wins; equal totals draw. Unused reward-deck order is never disclosed to
players, including in their history or personal export.

Reward draws, both bid reveals and collection of the whole pool play in order.
The presentation stays in view while scrolling; reduced motion keeps the cards
and outcome visible without movement. It never exposes unrevealed cards.

## Turn-based Battle Minigame (Test)

Your character's speed mode adds full-page energetic light trails; overload
adds low-power warnings. Overload takes priority, and the opponent's status
does not control your page effect. Reduced motion keeps a static indication.

Choose one of five characters, an optional harness and a legal skill set before
joining. Quick mode has a 60-like target and at most 25 rounds; standard mode
has a 300-like target and at most 75 rounds. The field guide gives the complete
mode-specific skill, buff, harness, resource and upgrade values.

Each round has a full 20-second planning period. Purchases and the cast are one
frozen plan. Without stun, a main cast is required before confirmation. While
stun remains after the proposed affordable purchases, the only submission
button is **Skip casting**. If those purchases remove stun, select a main cast
and use **Lock in plan**. A timeout still follows the server's automatic rules.

When both plans lock or time expires, the server commits the complete round
and starts a step-by-step presentation. Every cast receives its own reading
time, with no fixed total duration. Both sides reveal together through
shopping and charging, payment and overload, cleansing and buffs, awarded
likes, additional effects and round-end changes. Numbers and bounded resource
bars animate from the server's before/after facts. API reserve, gold and trial
reserve have numeric feedback without capacity bars. Round-start refills also
animate during the next planning period without shortening its 20 seconds.

Only successful casts show a character action illustration. Failed or
cancelled casts show their reason. Stun is a buff and overload is a separate
state, each with its own chibi illustration slot. The shared battery and
overload receive extra visual emphasis. The character, action, outcome and
harness illustrations are bundled as transparent local images.

Music and effects have separate switches, off by default, remembered per game
in this browser. Likes music follows your own overload, speed and battle states;
changes crossfade over one beat starting at the next beat on a shared timeline.
Result music starts after the final settlement animation. Changing mode or
loadout, or joining a queue, returns to lobby music. Defeat cuts the background
and, when effects are enabled, plays a single short sting before silence.
Account local preferences offers lightweight or lossless music for the next
activation or game entry. Audio pauses in the background and releases on exit.

The shared-energy check uses the battery after both players' purchases and
each frozen plan's declared energy quote. An exact fit is allowed. If total
demand exceeds the battery, it empties and only players with a positive quote
overload. Their cast payment and effects are cancelled; their purchases stay.
A zero-energy opponent keeps their own statuses and resolves normally, subject
to their own resource checks. Two positive quotes still overload both players
and retain the existing double-overload result rule. Flash follow-up casts
perform their separate energy checks.

Use **Round log** during a match to inspect already settled rounds. Logs do not
pause the timer. Reconnecting resumes the current server timeline, without
replaying expired animation; repeated polls do not restart the same round.
Reduced-motion mode retains every final number, before/after change and reason.
Final results and wallet settlement commit immediately; the outcome illustration
appears after the last round's remaining presentation time.

## History and privacy

Players can read their own complete results for 30 days after settlement.
Unused opposing Turn-based Battle Minigame (Test) equipment remains hidden during a match and is
revealed at its end. Current profile preferences control live player names;
personal exports do not contain the opponent's identity or a saved name copy.

Administrators can filter and export recent complete records or long-term
anonymous archives. Stewards cannot use these administration endpoints.
At 30 days, normal access ends even if cleanup has not run. The archive retains
rules, equipment, rounds, random draws and results, but removes users, original
match and operation IDs, timestamps, payment sources and cross-match identity
links. Account deletion removes or de-identifies associated data immediately.

The administration page downloads complete UTF-8 NDJSON parts of at most
16 MiB. Its server pages have a stable boundary, at most 100 records, 8 MiB and
a five-second read budget. A match can span pages. Keep the selected dataset
and filters while continuing; expired recent records are counted as skipped.
Completion is reported only after the final page. Cancelled or failed downloads
can resume from the last completed page, while the signed cursor is valid.

## 双人游戏

《竞标对决》和《回合制对战小游戏（测试）》各自匹配两名玩家，可以同时进行，但同一款游戏只能排队或参加一局。入队时冻结模式、票价和三项抽成；两款新游戏初始关闭，不设新人奖励。

门票优先使用游戏积分，不足部分用通用积分；一只钱包欠款不抵扣另一只钱包的正余额。取消排队、120 秒未匹配、平局或系统取消均按原币种退票。胜负局退还胜者原门票，再把败者门票扣除平台、福利池和星期四池各自向下取整的抽成后，以通用积分奖励胜者。认输算负，断线不停表；服务重启、封禁或删号取消未完成对局，删号后的钱包不会被迟到结算重建。局内分数不是可消费积分。

竞标共 13 轮，前 12 轮双方各当 6 次庄家。持王的庄家有 10 秒决定使用或保留；王只加倍本轮自己的奖励牌，不加倍此前累积奖励。随后双方在 20 秒内暗中锁定一张手牌，全部锁定或超时才同时揭牌；超时使用最小剩余牌。大牌获得本轮奖励和累积奖励，同点数累积至下轮，最后一轮仍平则丢弃。总分高者获胜，同分平局。未揭示奖励牌顺序不会通过玩家历史或个人导出泄露。

点赞在入队前选择角色、可选 Harness 和合法技能组。快速模式目标 60 赞、最多 25 轮；标准模式目标 300 赞、最多 75 轮，完整技能数值以游戏内图鉴为准。每轮有完整 20 秒选择购物和出招；未眩晕必须选主招才能确认。拟购且买得起的物品仍不能解除眩晕时，唯一按钮为“跳过出招”；若能解除眩晕，则必须选招，按钮恢复“确认方案”。系统超时仍依照自动规则处理。

竞标按顺序展示奖励抽取、双方亮牌、整个奖池的归属，滚动页面时演出仍保持可见；减少动态模式保留静态牌面和结果。回合制对战中，本人的倍速模式使用全页加速光线，过载使用低电量警示，过载优先；对手状态不影响本人的全页效果，减少动态模式保留静态提示。

双方锁定或超时后，服务端立即结算，并按内容依次展示方案、购物充电、费用过载、净化与 Buff、实得赞及轮末变化。每一步单独确定时长，连续技能逐个展示，总时长不限；全部结束后才开始下一轮完整 20 秒。资源由服务端提供前后值，界面播放数字和有上限的资源条；API 余量、金币和试用余量只用数字。轮初补充也有动效，不缩短下轮选招。成功施放才展示动作图；眩晕 Buff 和过载状态各有独立 Q 版图。角色、技能、终局和 Harness 使用随程序打包的透明插画。

音乐和音效分别开关，默认关闭，按游戏记住当前浏览器的选择。点赞音乐按本人过载、倍速、普通对战的优先级播放，共用时间轴，下一拍开始、一拍完成交接。最后一轮完整演出结束后进入结果音乐；修改模式、配装或开始排队时回大厅音乐。失败切断背景，若音效开启则播放一次短片段后静音。账户页本机偏好提供轻量版和无损版音乐，下次开启或进入游戏时生效。页面进入后台暂停声音，离开游戏释放资源。

共享电池以双方购物后的电量和冻结方案的声明报价判断，恰好耗尽不算过载。总需求超限时电池清零，只让正耗电方过载并取消其技能付款与效果，购物保留；零耗电方按自身资源正常执行，不被连带取消或清理状态。双方报价都为正仍沿用双方过载规则，Flash 连答另行检查。

对战中的“结算日志”可分页查看已结算轮次，不暂停计时。重连按服务端时间继续，不补播过期动画；减少动态效果模式保留完整数值和原因。最终胜负与账务立即提交，界面播完本轮剩余演出再显示胜负图。

本人和管理员可查看近 30 天完整结果；点赞局中对手未用配装保持隐藏，终局开放。到期后仅管理员可访问去身份的长期存档，保留完整规则与过程，移除用户、原始局／账务标识、绝对时间、付款来源和跨局身份关联。协管没有该管理权限。管理员下载可跨页继续，页面到期记录会计入跳过数，只有完整结束才显示成功。
