# Two-player games

[简体中文](#双人游戏)

Bidding Duel and Turn-based Battle Minigame (Test) use separate two-player queues. The game, mode,
entry price and three fee rates shown before joining are fixed for that entry.
You can play one of each game at the same time, but cannot join two modes of the
same game. Fresh installations start both games disabled. Once-only newcomer tasks award 10,000 General Credits across four Bidding tasks and 18,000 across four battle tasks; completion and victory awards can stack. System cancellation gives no award. The game page shows each task and its authoritative completion state.

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

Each player receives a shuffled hand of ranks 1–13. The deal uses fixed A–K
13-card slots for 13 rounds, and each player has a separate shuffled reward
deck. Both hands remain visible with fixed positions, and played cards stay grey
and unavailable. The red side uses hearts (♥) in hand and diamonds (♦) for rewards; the black side uses spades (♠) in hand and clubs (♣) for rewards. Seats, rather than the viewer's position or theme, determine the suit. The first 12
rounds alternate the dealer; each player is dealer six times. A dealer holding
a joker has 10 seconds to use or save it. A used joker doubles only that
player's reward for the current round, and does not double carried rewards.

Both players then have 20 seconds to lock one card. Cards remain hidden and in
the hand until both choices are locked or the deadline expires. Timeout selects
the smallest remaining card. Higher rank wins the current rewards and carry;
a tie carries rewards into the next round. Final-round ties discard the carry.
The joker phase is skipped when unavailable and in round 13. Highest total
points wins; equal totals draw. Active matches never disclose the unused
reward-deck order. Players may inspect the
remaining reward-card set sorted by point value, but the future draw order is
never exposed during play. After the match, the disclosed randomness proof
allows participants to reconstruct the full draw order.

Reward draws, both bid reveals and collection of the whole pool play in order.
The presentation stays in view while scrolling; reduced motion keeps the cards
and outcome visible without movement. It never exposes unrevealed cards.

## Turn-based Battle Minigame (Test)

Your character's speed mode adds full-page energetic light trails; overload
adds overload warnings. Overload takes priority, and the opponent's status
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
in this browser. Turn-based Battle Minigame (Test) music follows your own overload, speed and battle states;
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

## Character passives and resistance

Every character has one always-active passive, separate from equipped skills. ChatGPT retains its image quota and DeepSeek its subscription-free resources. Claude gains one base like on an executed main skill with a positive nominal base when strictly ahead at the start of that step. Extra skills and follow-ups do not trigger it. Gemini gains one base like on executed normal attacks, including Flash follow-ups; distilled PUB41 remains a special skill. Existing decay and subsequent modifiers still apply.

GLM has 25% resistance, rising to 50% while strictly behind. DeepSeek has 25% effect hit, rising to 50% while strictly ahead. Each main step, paired extra-skill slot and Flash batch uses a shared score snapshot. Each attempted enemy debuff layer succeeds with probability `min(1,(100+hit)/(100+resistance))`. Overload, speed mode and self-inflicted effects are outside this check. Partial resistance applies only successful layers; full resistance adds nothing and does not refresh duration. SOTA's additional debuff follows successful application and has its own resistance check, without recursion. Saved older matches retain their original catalog and rules.

Normal application, partial resistance and full resistance have distinct feedback. Follow-up effects grow through three capped levels within a round. All six games synchronize sound with server events, coalesce batch results and avoid replay after reconnect. Music ducks and resumes at its original position. Muting and reduced motion preserve the same result and values.

## Learning, availability and reminders

Closed games and modes disable matching. Learning, the field guide, history and
already-started matches remain reachable. Visible pages refresh admission settings
periodically, on focus and after a rejected entry; the message distinguishes a
closed game, a closed mode and changed matching conditions.

Effect summaries appear on equipment, action and status cards. The field guide
separates original and distilled versions, links relevant terms and keeps flavor
quotes apart from mechanics. Desktop readers can see related explanations side by
side; phone readers can follow links and return to the previous entry.

An optional local tutorial equips ChatGPT with Codex, Usage reset, Hello, world!,
Regenerate, Pedal faster and Words and pictures. A scripted Claude opponent with
Codex plays ten quick-mode rounds ending 66–61. Teaching waits for your actions,
can be skipped at any time, and never queues, spends credits, awards prizes or
creates a real match. The browser remembers completion or skipping; replay starts
from the beginning. Applying the loadout only fills the normal form. A real queue
or match discovered from another tab immediately takes precedence.

An unconfirmed live plan receives extra visual emphasis below five seconds, with
at most one short sound per second when effects are enabled. Locking, timeout and
automatic overload recovery stop the warning. Hidden pages do not play or replay
missed ticks. Reduced motion retains the static emphasis.

Overload highlights the resource shortage recorded at the failed payment: shared
battery, mixed burst/API, API-only, or the deficient subscription quotas. A total
quota bottleneck is also marked. API remains a numeric balance. Highlights follow
the settlement timeline; old records without details use a generic explanation.
Gold and image shortages remain invalid plans, rather than becoming overload.

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

每个角色的固有被动持续生效，不占配装。ChatGPT 保留图像额度，DeepSeek 保留无订阅资源。Claude 在步骤开始严格领先、主技能自身标称基础得赞大于零且实际施放时，基础得赞加 1；额外技能和连答不触发。Gemini 的普攻类技能基础加 1，包含 Flash 连答，蒸馏 PUB41 仍属特殊技能。原有衰减及后续增减、倍率继续生效。

GLM 常驻抵抗 25%，严格落后时为 50%；DeepSeek 常驻效果命中 25%，严格领先时为 50%。主技能、各额外槽位与每批连答分别取双方共同的步骤得赞快照。向敌方施加的每层减益独立按 `min(1,(100+命中)/(100+抵抗))` 判定；过载、倍速等状态和自身副作用不参与。部分抵抗只施加成功层，全部抵抗不加层也不刷新。SOTA 追加减益须由成功施加触发，自身另行抵抗，不递归。旧局沿用保存的旧图鉴与规则。

正常施加、部分抵抗、全部抵抗各有反馈，单回合连答按三档递进并封顶。六游戏音画跟随服务端事件，批量结果合并，刷新重连不补播；音乐让位后从原位置恢复。静音及减少动态仍显示相同结果与数值。

《竞标对决》和《回合制对战小游戏（测试）》各自匹配两名玩家，可以同时进行，但同一款游戏只能排队或参加一局。入队时冻结模式、票价和三项抽成；全新安装时两款游戏初始关闭。竞标的四项一次性新人任务合计奖励 10,000 通用积分，对战四项合计 18,000；同局的完成与获胜奖励可以叠加，系统取消不奖励。具体任务和服务端完成状态见游戏页面。

门票优先使用游戏积分，不足部分用通用积分；一只钱包欠款不抵扣另一只钱包的正余额。取消排队、120 秒未匹配、平局或系统取消均按原币种退票。胜负局退还胜者原门票，再把败者门票扣除平台、福利池和星期四池各自向下取整的抽成后，以通用积分奖励胜者。认输算负，断线不停表；服务重启、封禁或删号取消未完成对局，删号后的钱包不会被迟到结算重建。局内分数不是可消费积分。

竞标固定使用双方各 A～K 十三个牌位，共 13 轮，前 12 轮双方各当 6 次庄家。持有 Joker 的庄家有 10 秒决定使用或保留；Joker 只加倍本轮自己的奖励牌，不加倍此前累积奖励。双方的暗选只对本人可见；已揭示出牌置灰。红方手牌使用红桃 ♥、奖励使用方块 ♦，黑方手牌使用黑桃 ♠、奖励使用梅花 ♣；花色由阵营决定，不随观看视角或主题翻转，牌背堆可查看按点数排序的剩余集合，但不代表未来顺序。随后双方在 20 秒内暗中锁定一张手牌，全部锁定或超时才同时揭牌；超时使用最小剩余牌。大牌获得本轮奖励和累积奖励，同点数累积至下轮，最后一轮仍平则丢弃。总分高者获胜，同分平局。进行中不公开未抽取奖励牌的顺序；终局公开的随机性凭证允许参与者重建完整抽取顺序。

回合制对战小游戏（测试）在入队前选择角色、可选 Harness 和合法技能组。快速模式目标 60 赞、最多 25 轮；标准模式目标 300 赞、最多 75 轮，完整技能数值以游戏内图鉴为准。每轮有完整 20 秒选择购物和出招；未眩晕必须选主招才能确认。拟购且买得起的物品仍不能解除眩晕时，唯一按钮为“跳过出招”；若能解除眩晕，则必须选招，按钮恢复“确认方案”。系统超时仍依照自动规则处理。

竞标按顺序展示奖励抽取、双方亮牌、整个奖池的归属，滚动页面时演出仍保持可见；减少动态模式保留静态牌面和结果。回合制对战中，本人的倍速模式使用全页加速光线，过载使用独立警示，过载优先；对手状态不影响本人的全页效果，减少动态模式保留静态提示。

双方锁定或超时后，服务端立即结算，并按内容依次展示方案、购物充电、费用过载、净化与 Buff、实得赞及轮末变化。每一步单独确定时长，连续技能逐个展示，总时长不限；全部结束后才开始下一轮完整 20 秒。资源由服务端提供前后值，界面播放数字和有上限的资源条；API 余量、金币和试用余量只用数字。轮初补充也有动效，不缩短下轮选招。成功施放才展示动作图；眩晕 Buff 和过载状态各有独立 Q 版图。角色、技能、终局和 Harness 使用随程序打包的透明插画。

音乐和音效分别开关，默认关闭，按游戏记住当前浏览器的选择。回合制对战小游戏（测试）音乐按本人过载、倍速、普通对战的优先级播放，共用时间轴，下一拍开始、一拍完成交接。最后一轮完整演出结束后进入结果音乐；修改模式、配装或开始排队时回大厅音乐。失败切断背景，若音效开启则播放一次短片段后静音。账户页本机偏好提供轻量版和无损版音乐，下次开启或进入游戏时生效。页面进入后台暂停声音，离开游戏释放资源。

共享电池以双方购物后的电量和冻结方案的声明报价判断，恰好耗尽不算过载。总需求超限时电池清零，只让正耗电方过载并取消其技能付款与效果，购物保留；零耗电方按自身资源正常执行，不被连带取消或清理状态。双方报价都为正仍沿用双方过载规则，Flash 连答另行检查。

对战中的“结算日志”可分页查看已结算轮次，不暂停计时。重连按服务端时间继续，不补播过期动画；减少动态效果模式保留完整数值和原因。最终胜负与账务立即提交，界面播完本轮剩余演出再显示胜负图。

关闭的游戏和模式禁用匹配，学习、词条、历史及已开始的对局仍可访问。页面可见时定期刷新配置，重新聚焦及入队失败后立即同步，并区分游戏关闭、模式关闭与匹配条件变化。

配装、选招和状态卡片直接显示效果摘要。词条详情区分原版／蒸馏版本，相关术语可以点击，桌面并排阅读关联解释，手机支持跳转与返回；玩梗独立作为引用展示。

新手引导在浏览器本地运行，可随时跳过并重看，浏览器记住完成或跳过状态。引导配装为 ChatGPT＋Codex＋用量重置＋Hello, world!＋重新生成＋加速猛蹬＋图文并茂，对手为携带 Codex 的 Claude，完整十轮快速对战以 66∶61 险胜结束。讲解及操作等待玩家，不真实匹配、不扣积分、不发奖励、不创建真实记录。“使用教学配装”只填写大厅表单，中途刷新后从头开始；其他标签页出现真实排队或对局时立即恢复真实状态。

正式对局本人仍需确认时，不足五秒会强调倒计时和操作区；开启音效后每秒最多一次短提示。提交、锁定、超时或自动过载跳过后停止；后台不播放，返回不补播，减少动态模式保留静态强调。

过载按失败付款时记录的事实高亮共享电能、混合支付的瞬发与 API、仅 API 或实际不足的订阅额度；总量构成瓶颈时也标出总量。API 保持数值显示，提示随结算时间轴出现并保留到本次演出结束。旧记录缺少细分信息时只显示通用说明；金币和图像不足仍是非法方案，不改为过载。

本人和管理员可查看近 30 天完整结果；回合制对战小游戏（测试）局中对手未用配装保持隐藏，终局开放。到期后仅管理员可访问去身份的长期存档，保留完整规则与过程，移除用户、原始局／账务标识、绝对时间、付款来源和跨局身份关联。协管没有该管理权限。管理员下载可跨页继续，页面到期记录会计入跳过数，只有完整结束才显示成功。
