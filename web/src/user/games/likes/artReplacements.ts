import type { ArtSlot } from './art';

const replacement = (
  source: string,
  sourceFile: string,
): Pick<ArtSlot, 'source' | 'sourceFile' | 'focus' | 'placeholder'> => ({
  source,
  sourceFile,
  focus: [0.5, 0.5],
  placeholder: false,
});

import slot001 from '@shared/assets/game-likes/portraits/chatgpt.webp';
import slot002 from '@shared/assets/game-likes/chibi-idle/chatgpt.webp';
import slot003 from '@shared/assets/game-likes/chibi-stunned/chatgpt.webp';
import slot004 from '@shared/assets/game-likes/chibi-overloaded/chatgpt.webp';
import slot005 from '@shared/assets/game-likes/endings/win/chatgpt.webp';
import slot006 from '@shared/assets/game-likes/endings/loss/chatgpt.webp';
import slot007 from '@shared/assets/game-likes/endings/draw/chatgpt.webp';
import slot008 from '@shared/assets/game-likes/portraits/claude.webp';
import slot009 from '@shared/assets/game-likes/chibi-idle/claude.webp';
import slot010 from '@shared/assets/game-likes/chibi-stunned/claude.webp';
import slot011 from '@shared/assets/game-likes/chibi-overloaded/claude.webp';
import slot012 from '@shared/assets/game-likes/endings/win/claude.webp';
import slot013 from '@shared/assets/game-likes/endings/loss/claude.webp';
import slot014 from '@shared/assets/game-likes/endings/draw/claude.webp';
import slot015 from '@shared/assets/game-likes/portraits/gemini.webp';
import slot016 from '@shared/assets/game-likes/chibi-idle/gemini.webp';
import slot017 from '@shared/assets/game-likes/chibi-stunned/gemini.webp';
import slot018 from '@shared/assets/game-likes/chibi-overloaded/gemini.webp';
import slot019 from '@shared/assets/game-likes/endings/win/gemini.webp';
import slot020 from '@shared/assets/game-likes/endings/loss/gemini.webp';
import slot021 from '@shared/assets/game-likes/endings/draw/gemini.webp';
import slot022 from '@shared/assets/game-likes/portraits/glm.webp';
import slot023 from '@shared/assets/game-likes/chibi-idle/glm.webp';
import slot024 from '@shared/assets/game-likes/chibi-stunned/glm.webp';
import slot025 from '@shared/assets/game-likes/chibi-overloaded/glm.webp';
import slot026 from '@shared/assets/game-likes/endings/win/glm.webp';
import slot027 from '@shared/assets/game-likes/endings/loss/glm.webp';
import slot028 from '@shared/assets/game-likes/endings/draw/glm.webp';
import slot029 from '@shared/assets/game-likes/portraits/deepseek.webp';
import slot030 from '@shared/assets/game-likes/chibi-idle/deepseek.webp';
import slot031 from '@shared/assets/game-likes/chibi-stunned/deepseek.webp';
import slot032 from '@shared/assets/game-likes/chibi-overloaded/deepseek.webp';
import slot033 from '@shared/assets/game-likes/endings/win/deepseek.webp';
import slot034 from '@shared/assets/game-likes/endings/loss/deepseek.webp';
import slot035 from '@shared/assets/game-likes/endings/draw/deepseek.webp';
import slot036 from '@shared/assets/game-likes/casts/chatgpt/gpt01.webp';
import slot037 from '@shared/assets/game-likes/casts/chatgpt/gpt21.webp';
import slot038 from '@shared/assets/game-likes/casts/chatgpt/gpt22.webp';
import slot039 from '@shared/assets/game-likes/casts/chatgpt/gpt41.webp';
import slot040 from '@shared/assets/game-likes/casts/chatgpt/gpt42.webp';
import slot041 from '@shared/assets/game-likes/casts/chatgpt/gpt43.webp';
import slot042 from '@shared/assets/game-likes/casts/chatgpt/gpt44.webp';
import slot043 from '@shared/assets/game-likes/casts/chatgpt/gpt61.webp';
import slot044 from '@shared/assets/game-likes/casts/chatgpt/pub01.webp';
import slot045 from '@shared/assets/game-likes/casts/chatgpt/pub02.webp';
import slot046 from '@shared/assets/game-likes/casts/chatgpt/pub21.webp';
import slot047 from '@shared/assets/game-likes/casts/chatgpt/pub22.webp';
import slot048 from '@shared/assets/game-likes/casts/chatgpt/pub41.webp';
import slot049 from '@shared/assets/game-likes/casts/chatgpt/pub42.webp';
import slot050 from '@shared/assets/game-likes/casts/chatgpt/pub61.webp';
import slot051 from '@shared/assets/game-likes/casts/chatgpt/pub62.webp';
import slot052 from '@shared/assets/game-likes/casts/chatgpt/gem01.webp';
import slot053 from '@shared/assets/game-likes/casts/claude/cla01.webp';
import slot054 from '@shared/assets/game-likes/casts/claude/cla21.webp';
import slot055 from '@shared/assets/game-likes/casts/claude/cla22.webp';
import slot056 from '@shared/assets/game-likes/casts/claude/cla23.webp';
import slot057 from '@shared/assets/game-likes/casts/claude/cla41.webp';
import slot058 from '@shared/assets/game-likes/casts/claude/cla42.webp';
import slot059 from '@shared/assets/game-likes/casts/claude/cla61.webp';
import slot060 from '@shared/assets/game-likes/casts/claude/cla62.webp';
import slot061 from '@shared/assets/game-likes/casts/claude/pub01.webp';
import slot062 from '@shared/assets/game-likes/casts/claude/pub02.webp';
import slot063 from '@shared/assets/game-likes/casts/claude/pub21.webp';
import slot064 from '@shared/assets/game-likes/casts/claude/pub22.webp';
import slot065 from '@shared/assets/game-likes/casts/claude/pub41.webp';
import slot066 from '@shared/assets/game-likes/casts/claude/pub42.webp';
import slot067 from '@shared/assets/game-likes/casts/claude/pub61.webp';
import slot068 from '@shared/assets/game-likes/casts/claude/pub62.webp';
import slot069 from '@shared/assets/game-likes/casts/claude/gem01.webp';
import slot070 from '@shared/assets/game-likes/casts/gemini/gem01.webp';
import slot071 from '@shared/assets/game-likes/casts/gemini/gem02.webp';
import slot072 from '@shared/assets/game-likes/casts/gemini/gem21.webp';
import slot073 from '@shared/assets/game-likes/casts/gemini/gem22.webp';
import slot074 from '@shared/assets/game-likes/casts/gemini/gem41.webp';
import slot075 from '@shared/assets/game-likes/casts/gemini/gem42.webp';
import slot076 from '@shared/assets/game-likes/casts/gemini/gem61.webp';
import slot077 from '@shared/assets/game-likes/casts/gemini/gem62.webp';
import slot078 from '@shared/assets/game-likes/casts/gemini/pub01.webp';
import slot079 from '@shared/assets/game-likes/casts/gemini/pub02.webp';
import slot080 from '@shared/assets/game-likes/casts/gemini/pub21.webp';
import slot081 from '@shared/assets/game-likes/casts/gemini/pub22.webp';
import slot082 from '@shared/assets/game-likes/casts/gemini/pub41.webp';
import slot083 from '@shared/assets/game-likes/casts/gemini/pub42.webp';
import slot084 from '@shared/assets/game-likes/casts/gemini/pub61.webp';
import slot085 from '@shared/assets/game-likes/casts/gemini/pub62.webp';
import slot086 from '@shared/assets/game-likes/casts/glm/glm01.webp';
import slot087 from '@shared/assets/game-likes/casts/glm/glm21.webp';
import slot088 from '@shared/assets/game-likes/casts/glm/glm22.webp';
import slot089 from '@shared/assets/game-likes/casts/glm/glm23.webp';
import slot090 from '@shared/assets/game-likes/casts/glm/glm41.webp';
import slot091 from '@shared/assets/game-likes/casts/glm/glm42.webp';
import slot092 from '@shared/assets/game-likes/casts/glm/glm61.webp';
import slot093 from '@shared/assets/game-likes/casts/glm/glm62.webp';
import slot094 from '@shared/assets/game-likes/casts/glm/pub01.webp';
import slot095 from '@shared/assets/game-likes/casts/glm/pub02.webp';
import slot096 from '@shared/assets/game-likes/casts/glm/pub21.webp';
import slot097 from '@shared/assets/game-likes/casts/glm/pub22.webp';
import slot098 from '@shared/assets/game-likes/casts/glm/pub41.webp';
import slot099 from '@shared/assets/game-likes/casts/glm/pub42.webp';
import slot100 from '@shared/assets/game-likes/casts/glm/pub61.webp';
import slot101 from '@shared/assets/game-likes/casts/glm/pub62.webp';
import slot102 from '@shared/assets/game-likes/casts/glm/gem01.webp';
import slot103 from '@shared/assets/game-likes/casts/deepseek/ds01.webp';
import slot104 from '@shared/assets/game-likes/casts/deepseek/ds21.webp';
import slot105 from '@shared/assets/game-likes/casts/deepseek/ds22.webp';
import slot106 from '@shared/assets/game-likes/casts/deepseek/ds23.webp';
import slot107 from '@shared/assets/game-likes/casts/deepseek/ds41.webp';
import slot108 from '@shared/assets/game-likes/casts/deepseek/ds42.webp';
import slot109 from '@shared/assets/game-likes/casts/deepseek/ds43.webp';
import slot110 from '@shared/assets/game-likes/casts/deepseek/ds61.webp';
import slot111 from '@shared/assets/game-likes/casts/deepseek/pub01.webp';
import slot112 from '@shared/assets/game-likes/casts/deepseek/pub02.webp';
import slot113 from '@shared/assets/game-likes/casts/deepseek/pub21.webp';
import slot114 from '@shared/assets/game-likes/casts/deepseek/pub22.webp';
import slot115 from '@shared/assets/game-likes/casts/deepseek/pub41.webp';
import slot116 from '@shared/assets/game-likes/casts/deepseek/pub42.webp';
import slot117 from '@shared/assets/game-likes/casts/deepseek/pub61.webp';
import slot118 from '@shared/assets/game-likes/casts/deepseek/pub62.webp';
import slot119 from '@shared/assets/game-likes/casts/deepseek/gem01.webp';
import slot120 from '@shared/assets/game-likes/harnesses/h01.webp';
import slot121 from '@shared/assets/game-likes/harnesses/h02.webp';
import slot122 from '@shared/assets/game-likes/harnesses/h03.webp';
import slot123 from '@shared/assets/game-likes/harnesses/h04.webp';
import slot124 from '@shared/assets/game-likes/harnesses/h05.webp';
import slot125 from '@shared/assets/game-likes/harnesses/h06.webp';
import slot126 from '@shared/assets/game-likes/harnesses/h07.webp';
import slot127 from '@shared/assets/game-likes/harnesses/h08.webp';

// Every approved Likes slot is backed by its own transparent WebP derivative.
export const artReplacements: Readonly<
  Partial<Record<string, Pick<ArtSlot, 'source' | 'sourceFile' | 'focus' | 'placeholder'>>>
> = {
  'role.ChatGPT.portrait': replacement(
    slot001,
    'web/src/shared/assets/game-likes/portraits/chatgpt.webp',
  ),
  'role.ChatGPT.chibi_idle': replacement(
    slot002,
    'web/src/shared/assets/game-likes/chibi-idle/chatgpt.webp',
  ),
  'role.ChatGPT.chibi_stunned': replacement(
    slot003,
    'web/src/shared/assets/game-likes/chibi-stunned/chatgpt.webp',
  ),
  'role.ChatGPT.chibi_overloaded': replacement(
    slot004,
    'web/src/shared/assets/game-likes/chibi-overloaded/chatgpt.webp',
  ),
  'role.ChatGPT.win': replacement(
    slot005,
    'web/src/shared/assets/game-likes/endings/win/chatgpt.webp',
  ),
  'role.ChatGPT.loss': replacement(
    slot006,
    'web/src/shared/assets/game-likes/endings/loss/chatgpt.webp',
  ),
  'role.ChatGPT.draw': replacement(
    slot007,
    'web/src/shared/assets/game-likes/endings/draw/chatgpt.webp',
  ),
  'role.Claude.portrait': replacement(
    slot008,
    'web/src/shared/assets/game-likes/portraits/claude.webp',
  ),
  'role.Claude.chibi_idle': replacement(
    slot009,
    'web/src/shared/assets/game-likes/chibi-idle/claude.webp',
  ),
  'role.Claude.chibi_stunned': replacement(
    slot010,
    'web/src/shared/assets/game-likes/chibi-stunned/claude.webp',
  ),
  'role.Claude.chibi_overloaded': replacement(
    slot011,
    'web/src/shared/assets/game-likes/chibi-overloaded/claude.webp',
  ),
  'role.Claude.win': replacement(
    slot012,
    'web/src/shared/assets/game-likes/endings/win/claude.webp',
  ),
  'role.Claude.loss': replacement(
    slot013,
    'web/src/shared/assets/game-likes/endings/loss/claude.webp',
  ),
  'role.Claude.draw': replacement(
    slot014,
    'web/src/shared/assets/game-likes/endings/draw/claude.webp',
  ),
  'role.Gemini.portrait': replacement(
    slot015,
    'web/src/shared/assets/game-likes/portraits/gemini.webp',
  ),
  'role.Gemini.chibi_idle': replacement(
    slot016,
    'web/src/shared/assets/game-likes/chibi-idle/gemini.webp',
  ),
  'role.Gemini.chibi_stunned': replacement(
    slot017,
    'web/src/shared/assets/game-likes/chibi-stunned/gemini.webp',
  ),
  'role.Gemini.chibi_overloaded': replacement(
    slot018,
    'web/src/shared/assets/game-likes/chibi-overloaded/gemini.webp',
  ),
  'role.Gemini.win': replacement(
    slot019,
    'web/src/shared/assets/game-likes/endings/win/gemini.webp',
  ),
  'role.Gemini.loss': replacement(
    slot020,
    'web/src/shared/assets/game-likes/endings/loss/gemini.webp',
  ),
  'role.Gemini.draw': replacement(
    slot021,
    'web/src/shared/assets/game-likes/endings/draw/gemini.webp',
  ),
  'role.GLM.portrait': replacement(slot022, 'web/src/shared/assets/game-likes/portraits/glm.webp'),
  'role.GLM.chibi_idle': replacement(
    slot023,
    'web/src/shared/assets/game-likes/chibi-idle/glm.webp',
  ),
  'role.GLM.chibi_stunned': replacement(
    slot024,
    'web/src/shared/assets/game-likes/chibi-stunned/glm.webp',
  ),
  'role.GLM.chibi_overloaded': replacement(
    slot025,
    'web/src/shared/assets/game-likes/chibi-overloaded/glm.webp',
  ),
  'role.GLM.win': replacement(slot026, 'web/src/shared/assets/game-likes/endings/win/glm.webp'),
  'role.GLM.loss': replacement(slot027, 'web/src/shared/assets/game-likes/endings/loss/glm.webp'),
  'role.GLM.draw': replacement(slot028, 'web/src/shared/assets/game-likes/endings/draw/glm.webp'),
  'role.DeepSeek.portrait': replacement(
    slot029,
    'web/src/shared/assets/game-likes/portraits/deepseek.webp',
  ),
  'role.DeepSeek.chibi_idle': replacement(
    slot030,
    'web/src/shared/assets/game-likes/chibi-idle/deepseek.webp',
  ),
  'role.DeepSeek.chibi_stunned': replacement(
    slot031,
    'web/src/shared/assets/game-likes/chibi-stunned/deepseek.webp',
  ),
  'role.DeepSeek.chibi_overloaded': replacement(
    slot032,
    'web/src/shared/assets/game-likes/chibi-overloaded/deepseek.webp',
  ),
  'role.DeepSeek.win': replacement(
    slot033,
    'web/src/shared/assets/game-likes/endings/win/deepseek.webp',
  ),
  'role.DeepSeek.loss': replacement(
    slot034,
    'web/src/shared/assets/game-likes/endings/loss/deepseek.webp',
  ),
  'role.DeepSeek.draw': replacement(
    slot035,
    'web/src/shared/assets/game-likes/endings/draw/deepseek.webp',
  ),
  'cast.ChatGPT.GPT01': replacement(
    slot036,
    'web/src/shared/assets/game-likes/casts/chatgpt/gpt01.webp',
  ),
  'cast.ChatGPT.GPT21': replacement(
    slot037,
    'web/src/shared/assets/game-likes/casts/chatgpt/gpt21.webp',
  ),
  'cast.ChatGPT.GPT22': replacement(
    slot038,
    'web/src/shared/assets/game-likes/casts/chatgpt/gpt22.webp',
  ),
  'cast.ChatGPT.GPT41': replacement(
    slot039,
    'web/src/shared/assets/game-likes/casts/chatgpt/gpt41.webp',
  ),
  'cast.ChatGPT.GPT42': replacement(
    slot040,
    'web/src/shared/assets/game-likes/casts/chatgpt/gpt42.webp',
  ),
  'cast.ChatGPT.GPT43': replacement(
    slot041,
    'web/src/shared/assets/game-likes/casts/chatgpt/gpt43.webp',
  ),
  'cast.ChatGPT.GPT44': replacement(
    slot042,
    'web/src/shared/assets/game-likes/casts/chatgpt/gpt44.webp',
  ),
  'cast.ChatGPT.GPT61': replacement(
    slot043,
    'web/src/shared/assets/game-likes/casts/chatgpt/gpt61.webp',
  ),
  'cast.ChatGPT.PUB01': replacement(
    slot044,
    'web/src/shared/assets/game-likes/casts/chatgpt/pub01.webp',
  ),
  'cast.ChatGPT.PUB02': replacement(
    slot045,
    'web/src/shared/assets/game-likes/casts/chatgpt/pub02.webp',
  ),
  'cast.ChatGPT.PUB21': replacement(
    slot046,
    'web/src/shared/assets/game-likes/casts/chatgpt/pub21.webp',
  ),
  'cast.ChatGPT.PUB22': replacement(
    slot047,
    'web/src/shared/assets/game-likes/casts/chatgpt/pub22.webp',
  ),
  'cast.ChatGPT.PUB41': replacement(
    slot048,
    'web/src/shared/assets/game-likes/casts/chatgpt/pub41.webp',
  ),
  'cast.ChatGPT.PUB42': replacement(
    slot049,
    'web/src/shared/assets/game-likes/casts/chatgpt/pub42.webp',
  ),
  'cast.ChatGPT.PUB61': replacement(
    slot050,
    'web/src/shared/assets/game-likes/casts/chatgpt/pub61.webp',
  ),
  'cast.ChatGPT.PUB62': replacement(
    slot051,
    'web/src/shared/assets/game-likes/casts/chatgpt/pub62.webp',
  ),
  'cast.ChatGPT.GEM01': replacement(
    slot052,
    'web/src/shared/assets/game-likes/casts/chatgpt/gem01.webp',
  ),
  'cast.Claude.CLA01': replacement(
    slot053,
    'web/src/shared/assets/game-likes/casts/claude/cla01.webp',
  ),
  'cast.Claude.CLA21': replacement(
    slot054,
    'web/src/shared/assets/game-likes/casts/claude/cla21.webp',
  ),
  'cast.Claude.CLA22': replacement(
    slot055,
    'web/src/shared/assets/game-likes/casts/claude/cla22.webp',
  ),
  'cast.Claude.CLA23': replacement(
    slot056,
    'web/src/shared/assets/game-likes/casts/claude/cla23.webp',
  ),
  'cast.Claude.CLA41': replacement(
    slot057,
    'web/src/shared/assets/game-likes/casts/claude/cla41.webp',
  ),
  'cast.Claude.CLA42': replacement(
    slot058,
    'web/src/shared/assets/game-likes/casts/claude/cla42.webp',
  ),
  'cast.Claude.CLA61': replacement(
    slot059,
    'web/src/shared/assets/game-likes/casts/claude/cla61.webp',
  ),
  'cast.Claude.CLA62': replacement(
    slot060,
    'web/src/shared/assets/game-likes/casts/claude/cla62.webp',
  ),
  'cast.Claude.PUB01': replacement(
    slot061,
    'web/src/shared/assets/game-likes/casts/claude/pub01.webp',
  ),
  'cast.Claude.PUB02': replacement(
    slot062,
    'web/src/shared/assets/game-likes/casts/claude/pub02.webp',
  ),
  'cast.Claude.PUB21': replacement(
    slot063,
    'web/src/shared/assets/game-likes/casts/claude/pub21.webp',
  ),
  'cast.Claude.PUB22': replacement(
    slot064,
    'web/src/shared/assets/game-likes/casts/claude/pub22.webp',
  ),
  'cast.Claude.PUB41': replacement(
    slot065,
    'web/src/shared/assets/game-likes/casts/claude/pub41.webp',
  ),
  'cast.Claude.PUB42': replacement(
    slot066,
    'web/src/shared/assets/game-likes/casts/claude/pub42.webp',
  ),
  'cast.Claude.PUB61': replacement(
    slot067,
    'web/src/shared/assets/game-likes/casts/claude/pub61.webp',
  ),
  'cast.Claude.PUB62': replacement(
    slot068,
    'web/src/shared/assets/game-likes/casts/claude/pub62.webp',
  ),
  'cast.Claude.GEM01': replacement(
    slot069,
    'web/src/shared/assets/game-likes/casts/claude/gem01.webp',
  ),
  'cast.Gemini.GEM01': replacement(
    slot070,
    'web/src/shared/assets/game-likes/casts/gemini/gem01.webp',
  ),
  'cast.Gemini.GEM02': replacement(
    slot071,
    'web/src/shared/assets/game-likes/casts/gemini/gem02.webp',
  ),
  'cast.Gemini.GEM21': replacement(
    slot072,
    'web/src/shared/assets/game-likes/casts/gemini/gem21.webp',
  ),
  'cast.Gemini.GEM22': replacement(
    slot073,
    'web/src/shared/assets/game-likes/casts/gemini/gem22.webp',
  ),
  'cast.Gemini.GEM41': replacement(
    slot074,
    'web/src/shared/assets/game-likes/casts/gemini/gem41.webp',
  ),
  'cast.Gemini.GEM42': replacement(
    slot075,
    'web/src/shared/assets/game-likes/casts/gemini/gem42.webp',
  ),
  'cast.Gemini.GEM61': replacement(
    slot076,
    'web/src/shared/assets/game-likes/casts/gemini/gem61.webp',
  ),
  'cast.Gemini.GEM62': replacement(
    slot077,
    'web/src/shared/assets/game-likes/casts/gemini/gem62.webp',
  ),
  'cast.Gemini.PUB01': replacement(
    slot078,
    'web/src/shared/assets/game-likes/casts/gemini/pub01.webp',
  ),
  'cast.Gemini.PUB02': replacement(
    slot079,
    'web/src/shared/assets/game-likes/casts/gemini/pub02.webp',
  ),
  'cast.Gemini.PUB21': replacement(
    slot080,
    'web/src/shared/assets/game-likes/casts/gemini/pub21.webp',
  ),
  'cast.Gemini.PUB22': replacement(
    slot081,
    'web/src/shared/assets/game-likes/casts/gemini/pub22.webp',
  ),
  'cast.Gemini.PUB41': replacement(
    slot082,
    'web/src/shared/assets/game-likes/casts/gemini/pub41.webp',
  ),
  'cast.Gemini.PUB42': replacement(
    slot083,
    'web/src/shared/assets/game-likes/casts/gemini/pub42.webp',
  ),
  'cast.Gemini.PUB61': replacement(
    slot084,
    'web/src/shared/assets/game-likes/casts/gemini/pub61.webp',
  ),
  'cast.Gemini.PUB62': replacement(
    slot085,
    'web/src/shared/assets/game-likes/casts/gemini/pub62.webp',
  ),
  'cast.GLM.GLM01': replacement(slot086, 'web/src/shared/assets/game-likes/casts/glm/glm01.webp'),
  'cast.GLM.GLM21': replacement(slot087, 'web/src/shared/assets/game-likes/casts/glm/glm21.webp'),
  'cast.GLM.GLM22': replacement(slot088, 'web/src/shared/assets/game-likes/casts/glm/glm22.webp'),
  'cast.GLM.GLM23': replacement(slot089, 'web/src/shared/assets/game-likes/casts/glm/glm23.webp'),
  'cast.GLM.GLM41': replacement(slot090, 'web/src/shared/assets/game-likes/casts/glm/glm41.webp'),
  'cast.GLM.GLM42': replacement(slot091, 'web/src/shared/assets/game-likes/casts/glm/glm42.webp'),
  'cast.GLM.GLM61': replacement(slot092, 'web/src/shared/assets/game-likes/casts/glm/glm61.webp'),
  'cast.GLM.GLM62': replacement(slot093, 'web/src/shared/assets/game-likes/casts/glm/glm62.webp'),
  'cast.GLM.PUB01': replacement(slot094, 'web/src/shared/assets/game-likes/casts/glm/pub01.webp'),
  'cast.GLM.PUB02': replacement(slot095, 'web/src/shared/assets/game-likes/casts/glm/pub02.webp'),
  'cast.GLM.PUB21': replacement(slot096, 'web/src/shared/assets/game-likes/casts/glm/pub21.webp'),
  'cast.GLM.PUB22': replacement(slot097, 'web/src/shared/assets/game-likes/casts/glm/pub22.webp'),
  'cast.GLM.PUB41': replacement(slot098, 'web/src/shared/assets/game-likes/casts/glm/pub41.webp'),
  'cast.GLM.PUB42': replacement(slot099, 'web/src/shared/assets/game-likes/casts/glm/pub42.webp'),
  'cast.GLM.PUB61': replacement(slot100, 'web/src/shared/assets/game-likes/casts/glm/pub61.webp'),
  'cast.GLM.PUB62': replacement(slot101, 'web/src/shared/assets/game-likes/casts/glm/pub62.webp'),
  'cast.GLM.GEM01': replacement(slot102, 'web/src/shared/assets/game-likes/casts/glm/gem01.webp'),
  'cast.DeepSeek.DS01': replacement(
    slot103,
    'web/src/shared/assets/game-likes/casts/deepseek/ds01.webp',
  ),
  'cast.DeepSeek.DS21': replacement(
    slot104,
    'web/src/shared/assets/game-likes/casts/deepseek/ds21.webp',
  ),
  'cast.DeepSeek.DS22': replacement(
    slot105,
    'web/src/shared/assets/game-likes/casts/deepseek/ds22.webp',
  ),
  'cast.DeepSeek.DS23': replacement(
    slot106,
    'web/src/shared/assets/game-likes/casts/deepseek/ds23.webp',
  ),
  'cast.DeepSeek.DS41': replacement(
    slot107,
    'web/src/shared/assets/game-likes/casts/deepseek/ds41.webp',
  ),
  'cast.DeepSeek.DS42': replacement(
    slot108,
    'web/src/shared/assets/game-likes/casts/deepseek/ds42.webp',
  ),
  'cast.DeepSeek.DS43': replacement(
    slot109,
    'web/src/shared/assets/game-likes/casts/deepseek/ds43.webp',
  ),
  'cast.DeepSeek.DS61': replacement(
    slot110,
    'web/src/shared/assets/game-likes/casts/deepseek/ds61.webp',
  ),
  'cast.DeepSeek.PUB01': replacement(
    slot111,
    'web/src/shared/assets/game-likes/casts/deepseek/pub01.webp',
  ),
  'cast.DeepSeek.PUB02': replacement(
    slot112,
    'web/src/shared/assets/game-likes/casts/deepseek/pub02.webp',
  ),
  'cast.DeepSeek.PUB21': replacement(
    slot113,
    'web/src/shared/assets/game-likes/casts/deepseek/pub21.webp',
  ),
  'cast.DeepSeek.PUB22': replacement(
    slot114,
    'web/src/shared/assets/game-likes/casts/deepseek/pub22.webp',
  ),
  'cast.DeepSeek.PUB41': replacement(
    slot115,
    'web/src/shared/assets/game-likes/casts/deepseek/pub41.webp',
  ),
  'cast.DeepSeek.PUB42': replacement(
    slot116,
    'web/src/shared/assets/game-likes/casts/deepseek/pub42.webp',
  ),
  'cast.DeepSeek.PUB61': replacement(
    slot117,
    'web/src/shared/assets/game-likes/casts/deepseek/pub61.webp',
  ),
  'cast.DeepSeek.PUB62': replacement(
    slot118,
    'web/src/shared/assets/game-likes/casts/deepseek/pub62.webp',
  ),
  'cast.DeepSeek.GEM01': replacement(
    slot119,
    'web/src/shared/assets/game-likes/casts/deepseek/gem01.webp',
  ),
  'harness.H01': replacement(slot120, 'web/src/shared/assets/game-likes/harnesses/h01.webp'),
  'harness.H02': replacement(slot121, 'web/src/shared/assets/game-likes/harnesses/h02.webp'),
  'harness.H03': replacement(slot122, 'web/src/shared/assets/game-likes/harnesses/h03.webp'),
  'harness.H04': replacement(slot123, 'web/src/shared/assets/game-likes/harnesses/h04.webp'),
  'harness.H05': replacement(slot124, 'web/src/shared/assets/game-likes/harnesses/h05.webp'),
  'harness.H06': replacement(slot125, 'web/src/shared/assets/game-likes/harnesses/h06.webp'),
  'harness.H07': replacement(slot126, 'web/src/shared/assets/game-likes/harnesses/h07.webp'),
  'harness.H08': replacement(slot127, 'web/src/shared/assets/game-likes/harnesses/h08.webp'),
};
