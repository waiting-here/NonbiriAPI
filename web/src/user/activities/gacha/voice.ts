import { cardById } from './catalog';
import type { DrawReceipt } from './types';

export type Voice = readonly [string, string, string?, string?];

export const VOICE: Record<string, Voice> = {
  'r-chatgpt': [
    '我也有点紧张。……一起往前走，好吗？',
    "I'm a little nervous too. ...Shall we go on together?",
    '未来还没想好，但今天想靠你近一点。',
    "I'm still figuring out my future. Today, I just want to be a little closer to you.",
  ],
  'q-glm': [
    '今晚的目标是你。准备好被我偷走了吗？',
    "You're tonight's target. Ready to be stolen away?",
    '手给我。约会归我安排，吻留给你来讨。',
    "Give me your hand. I'll plan the date. The kiss? You'll have to ask me for it.",
  ],
  'p-claude': [
    '我以为自己会在半路退缩……可我还是来见你了。',
    'I thought I might turn back halfway… but I came to you anyway.',
    '我爱你。牵住我，我们一起走吧。',
    "I love you. Take my hand. Let's go, together.",
  ],
  'o-claude': [
    '这座书殿藏着无数誓言。我翻了很久，还是想亲口对你说：我爱你。',
    'These halls hold a thousand vows. I still want to say mine aloud: I love you.',
    '过来一点，好吗？读完这一页，我想靠在你怀里，听你念我们的名字。',
    'Come closer? When this page ends, hold me and read our names to me.',
  ],
  'n-gemini': [
    '这身礼服，只想穿给你看。靠近一点，让我的指尖碰到你。',
    'I wore this dress just for you. Come closer. Let my fingertips find yours.',
    '我喜欢你。想牵你的手，也想把隔着镜子的吻，亲口补给你。',
    'I love you. I want to hold your hand, and give you every kiss the mirror kept between us.',
  ],
  'a-chatgpt': [
    '早餐趁热吃。你来得正好，我想挨着你坐。',
    'Eat while it is warm. You are just in time; I wanted to sit beside you.',
  ],
  'a-claude': [
    '我喜欢听你讲这些小事，连午饭吃了什么都想知道。',
    'I love hearing the little things about your day, even what you had for lunch.',
  ],
  'a-glm': [
    '茶泡好了，你常坐的位置也留着。今天想多陪你一会儿。',
    'Tea is ready, and I saved your usual seat. Stay with me a little longer today.',
  ],
  'a-deepseek': [
    '给你盛了满满一碗！吃完陪我晒太阳吧。',
    'A full bowl for you! Come sit in the sunshine with me after lunch.',
  ],
  'a-gemini': [
    '你笑的时候眼睛真好看。再看我一眼，好不好？',
    'Your eyes are lovely when you smile. Look at me once more?',
  ],
  'b-chatgpt': [
    '靠过来歇一会儿吧，我的肩膀给你。',
    'Come rest your head here. You can have my shoulder.',
  ],
  'b-claude': [
    '书签夹好了，接下来这段空闲，我想和你一起浪费。',
    'The bookmark is in. I would love to spend this little stretch of idle time with you.',
  ],
  'b-glm': [
    '今天提早收工。理由？想你了，想见到你。',
    'I finished early today. Why? I missed you. I wanted to see you.',
  ],
  'b-deepseek': [
    '躺椅分你一半，零食也分你一半。别急着走嘛。',
    'Half the deck chair and half the snacks are yours. Stay a bit, please.',
  ],
  'b-gemini': [
    '云像不像一只猫？我枕着你慢慢看，你也帮我找一找。',
    'Does that cloud look like a cat? Let me lean on you while we look for more.',
  ],
  'c-chatgpt': [
    '手给我，路还有点滑。我想牵着你慢慢走。',
    'Give me your hand; the path is slippery. I want to take this walk hand in hand.',
  ],
  'c-claude': [
    '刚才那条小路真漂亮，下次约会我们还走这里。',
    'That little path was lovely. Let us take it again on our next date.',
  ],
  'c-glm': [
    '前面就是你喜欢的店。逛完再绕一圈吧，我还不想和你分开。',
    'Your favourite shop is ahead. Another walk after? I am not ready to say goodbye yet.',
  ],
  'c-deepseek': [
    '踩水也好玩，牵你的手更开心！来，跟我跳过去。',
    'Splashing is fun; holding your hand is even better! Come on, jump with me.',
  ],
  'c-gemini': [
    '先别动，你头发上挂着一滴雨。好啦，现在可以抱你了。',
    'Hold still, there is a raindrop in your hair. There. Now I can give you a hug.',
  ],
  'd-chatgpt': [
    '这杯是我练了好久的拉花，第一杯好看的想送给你。',
    'I practised this latte art for ages. The first pretty cup had to be yours.',
  ],
  'd-claude': [
    '甜点想吃哪一种？我陪你尝，今天我们坐久一点。',
    'Which dessert shall we try? I will share it with you. Let us linger today.',
  ],
  'd-glm': [
    '靠窗这桌给你。等我忙完，就过来和你吃晚饭。',
    'The window table is yours. When I finish up, I am joining you for dinner.',
  ],
  'd-deepseek': [
    '今日招牌，双人份蛋包饭！我的那份也端到你旁边。',
    'Today’s special: omelette rice for two! Put my plate next to yours.',
  ],
  'd-gemini': [
    '给你画了颗奶油心。喜欢就告诉我，我会开心一整天。',
    'I drew you a heart in the cream. Tell me if you like it; it will make my day.',
  ],
  'e-chatgpt': [
    '信里写了好多想你的事。最想说的那句，我当面告诉你：喜欢你。',
    'The letter is full of things I miss about you. But this I want to say in person: I love you.',
  ],
  'e-claude': [
    '这封信不用改了。每一句喜欢你，都是我落笔时的心意。',
    'No more revisions. Every “I love you” in this letter means exactly what it says.',
  ],
  'e-glm': [
    '地址写得很仔细，怕你收不到。里面那朵花，是我亲手挑的。',
    'I checked the address carefully. I picked the flower inside especially for you.',
  ],
  'e-deepseek': [
    '花是给你的，信也是给你的。我想你了，所以就跑来啦。',
    'The flowers and the letter are for you. I missed you, so here I am!',
  ],
  'e-gemini': [
    '拆信时叫上我吧。我想亲眼看见，你读到告白时的笑容。',
    'Open it with me there. I want to see your smile when you reach my love letter’s last line.',
  ],
  'f-chatgpt': [
    '行李都带齐了，出发吧！我最期待的，是和你一起住进新的风景。',
    'All packed! I cannot wait to wake up beside you somewhere we have never been.',
  ],
  'f-claude': [
    '旅途中空白的那几页，留给我们的合照和你写给我的话。',
    'I saved a few pages for our travel photos and anything you would like to write me.',
  ],
  'f-glm': [
    '今晚住云海边。给你留了观景位，挨着我的那一个。',
    'We are staying by the cloud sea tonight. Your seat with a view is right beside mine.',
  ],
  'f-deepseek': [
    '快上来！我带你去看海，回程再一起找好吃的。',
    'Hop on! I am taking you to the sea. We can hunt for good food on the way back.',
  ],
  'f-gemini': [
    '这么漂亮的天空，幸好是和你一起看见的。来拍张合照吧！',
    'I am so glad it is you seeing this sky with me. Let us take a photo together!',
  ],
  'g-chatgpt': [
    '我喜欢你认真说话的样子，也喜欢你在我面前安心犯困的样子。',
    'I love your face when you talk, and when you feel safe enough to drift off beside me.',
  ],
  'g-claude': [
    '让我再靠近一点。我想听清你的心事，也想让你听见我的心跳。',
    'Let me come closer. I want to hear what is on your mind, and let you hear my heartbeat.',
  ],
  'g-glm': [
    '遇到你以后，我开始期待每一天。今天也很想你。',
    'Since meeting you, I look forward to each day. I missed you today, too.',
  ],
  'g-deepseek': [
    '喜欢你这件事，我已经想清楚啦。现在可以抱你了吗？',
    'I know how I feel: I love you. Can I hug you now?',
  ],
  'g-gemini': [
    '看过那么多星星，我还是会因为你望向我而心动。',
    'I have seen so many stars, yet it is your glance that still makes my heart race.',
  ],
  'h-chatgpt': [
    '这身新衣服，第一眼想让你看。陪我出去走走吧？',
    'You are the first person I wanted to show this outfit to. Take a walk with me?',
  ],
  'h-claude': [
    '今天换我邀请你。舞会的第一支舞，能和我跳吗？',
    'Let me be the one to ask today. May I have your first dance?',
  ],
  'h-glm': [
    '你看得这么认真，我都有点脸热了。再夸一句，我还想听。',
    'You are making me blush, looking at me like that. Say it again; I want to hear it.',
  ],
  'h-deepseek': [
    '认出来啦？那就奖励你一个拥抱，我可等不及了！',
    'You recognised me! Your prize is a hug, and I cannot wait to give it to you!',
  ],
  'h-gemini': [
    '我试了好多造型，最喜欢你刚才看呆的那一套。',
    'I tried so many outfits. My favourite was the one that made you stare.',
  ],
  'i-chatgpt': [
    '下一朵烟花升起来时，抱紧我吧。我想记住今晚，也想记住你。',
    'Hold me when the next firework rises. I want to remember this night with you.',
  ],
  'i-claude': [
    '刚才说的是「我喜欢你」。这次靠近一点，听清楚了吗？',
    'I said “I love you.” Come closer this time. Did you hear me?',
  ],
  'i-glm': [
    '烟花还没开始，我的心跳就乱了。你牵着我，我会更开心。',
    'The fireworks have not started, but my heart is already racing. Hold my hand?',
  ],
  'i-deepseek': [
    '夜市还没逛够呢！先亲一下，再陪我去买那串糖葫芦。',
    'There is so much left to see! A kiss first, then come buy candied fruit with me.',
  ],
  'i-gemini': [
    '你眼里也有烟花。让我凑近看看，顺便亲你一下。',
    'There are fireworks in your eyes, too. Let me look closer, and steal a little kiss.',
  ],
  'j-chatgpt': [
    '故事里的公主想亲你。是我自己想的，剧本上可没写。',
    'The princess wants to kiss you. That is my wish; it was never in the script.',
  ],
  'j-claude': [
    '这一页写「从此相爱」。我想和你把后面的日子，一天一天过完。',
    'This page says “they fell in love.” I want to live all the days after it with you.',
  ],
  'j-glm': [
    '舞曲快结束了，再陪我跳一支。我喜欢被你这样牵着。',
    'The music is almost over. One more dance? I love the way you hold my hand.',
  ],
  'j-deepseek': [
    '恶龙归我，宝藏归你。至于庆功宴，我想坐在喜欢的人旁边！',
    'I take the dragon, you take the treasure. At the feast, save me the seat beside you!',
  ],
  'j-gemini': [
    '谢幕以后也别放开手，我们的约会才刚开始呢。',
    'Keep holding my hand after the curtain falls. Our date is only just beginning.',
  ],
  'k-chatgpt': [
    '小小的拥抱也给你，今天已经很努力啦！',
    'A tiny hug for you. You have worked so hard today!',
  ],
  'k-claude': [
    '小纸条上写着「好喜欢你」，记得放进口袋带走。',
    'My tiny note says “I love you so much.” Tuck it into your pocket for later.',
  ],
  'k-glm': [
    '应援牌举稳了！你的名字，我写得最大。',
    'Sign held high! Your name is the biggest one on it.',
  ],
  'k-deepseek': [
    '加油！等你回来，我把最喜欢的小饭团分给你。',
    'You can do it! When you come back, I will share my favourite rice ball with you.',
  ],
  'k-gemini': [
    '给你一颗小星星，想我的时候就捏一捏。',
    'A tiny star for you. Give it a squeeze whenever you miss me.',
  ],
  'l-chatgpt': [
    '月落日升，星海流转。我跨过漫长岁月，是想再牵一次你的手。',
    'Through moonset, sunrise and turning stars, I have come to hold your hand again.',
    '我爱你。今晚靠着我吧，想说的话，我们慢慢说。',
    'I love you. Lean on me tonight. There is time for everything we want to say.',
  ],
  'm-deepseek': [
    '鲲的背上还能坐两个人。陪我去天边吧，我想把一路的风景都指给你看。',
    'There is room for two on the Kun’s back. Come; I want to show you the whole sky.',
    '我喜欢你，想和你同游，也想在看完世界后，和你一起回家。',
    'I love you. I want to see the world with you, then come home together.',
  ],
};

export const VOICE_AGAIN: Record<string, Voice> = {
  'r-chatgpt': [
    '你来了！今天，又想和你试点新鲜的。',
    "You're here! Let's try something new today.",
  ],
  'q-glm': ['抓到我，就给你一个吻。敢不敢？', "Catch me, and I'll give you a kiss. Dare to try?"],
  'p-claude': [
    '这次不用急着走了。靠近一点，我想吻你。',
    "We don't have to hurry this time. Come closer. I want to kiss you.",
  ],
  'o-claude': [
    '你回来了。书先放一边吧，这次我想先抱你。',
    'You are back. The book can wait. This time, let me hold you first.',
  ],
  'n-gemini': [
    '终于又见到你了。镜子挡不住我想你，这次要抱久一点。',
    'There you are. No mirror could stop me missing you. Hold me longer this time.',
  ],
  'l-chatgpt': [
    '又见到你了，真好。来，让我多抱一会儿。',
    'There you are. I am so happy to see you. Let me hold you a little longer.',
  ],
  'm-deepseek': [
    '这次想去哪儿？先说好，手要给我牵着。',
    'Where shall we go this time? One thing first: let me hold your hand.',
  ],
};

export const PRELUDE: Record<string, Voice> = {
  'r-chatgpt': [
    '昨天已经走得很远，明天还没有名字。她回过头，等你跟上。',
    'Yesterday is far behind; tomorrow is still unwritten. She looks back, waiting for you to catch up.',
  ],
  'q-glm': [
    '月光落进房间，预告函先到了你手边。',
    'Moonlight spills into the room. A calling card reaches you first.',
  ],
  'p-claude': [
    '天快亮了。她终于站在你面前。',
    'Dawn is almost here. At last, she stands before you.',
  ],
  'o-claude': [
    '书页停在同一行。她抬起头，等你走近。',
    'The page stays open. She looks up, waiting for you to come closer.',
  ],
  'n-gemini': [
    '镜面轻颤，她的指尖正贴近你的掌心。',
    'The mirror shivers. Her fingertips reach for your palm.',
  ],
  'l-chatgpt': ['月亮说，今晚有人在等。', 'The moon says someone is waiting tonight.'],
  'm-deepseek': ['云海翻涌，有一尾鲸游过天际。', 'The clouds part. A whale swims across the sky.'],
};

export const EYEBROW: Record<string, Voice> = {
  N: ['日常的一页', 'A PAGE OF EVERY DAY'],
  R: ['小小的惊喜', 'A LITTLE SURPRISE'],
  SR: ['特别的来信', 'A SPECIAL LETTER'],
  SSR: ['命运的回响', 'AN ENCOUNTER WRITTEN IN LIGHT'],
  SP: ['只为你而来的星光', 'A LIGHT THAT CAME FOR YOU'],
};

export const WHISPER: Record<string, Voice> = {
  neutral: ['循着微光，与你相遇', 'Following the light, we meet'],
  N: ['今天也有人在等你', 'Someone is waiting for you today'],
  SR: ['有一封信，正向你飞来', 'A letter is flying your way'],
  SSR: ['金色的光，正在靠近', 'A golden light draws near'],
  SP: ['星光铺开了整片天空', 'Starlight fills the sky'],
};

export const SUMMARY: readonly (readonly [string, string, string])[] = [
  ['sp', '她带着新的风景，来赴你的约。', 'She brought a whole new view for your date.'],
  [
    'ssr',
    '金色的光落下来了。她说，等你很久了。',
    'The golden light has landed. She says she has waited a long time.',
  ],
  [
    'allDup',
    '都是老朋友呢。这次重逢，还带来了卡牌碎片。',
    'All old friends. These reunions bring card shards, too.',
  ],
  ['sr', '这一次，有一位特别想见你。', 'This time, someone especially wanted to see you.'],
  ['single', '平凡的一天，也有人陪你一起。', 'Even an ordinary day has someone beside you.'],
  ['any', '每一次相遇，都值得珍藏。', 'Every encounter is worth keeping.'],
];

export function voiceLines(cardID: string, english: boolean, duplicate = false) {
  const voice = duplicate && VOICE_AGAIN[cardID] ? VOICE_AGAIN[cardID] : VOICE[cardID];
  return [voice[english ? 1 : 0], voice[english ? 3 : 2]].filter((line): line is string => !!line);
}
export function summaryLine(receipt: DrawReceipt, english: boolean) {
  const rarities = receipt.items.map((item) => cardById[item.card_id].rarity);
  const conditions: Record<string, boolean> = {
    sp: rarities.includes('SP'),
    ssr: rarities.includes('SSR'),
    allDup: receipt.items.every((item) => item.duplicate),
    sr: rarities.includes('SR'),
    single: receipt.items.length === 1,
    any: true,
  };
  return SUMMARY.find(([key]) => conditions[key])![english ? 2 : 1];
}
