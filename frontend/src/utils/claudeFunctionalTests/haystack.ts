// Long-context needle-in-a-haystack prompt: book-club filler with three newsletter
// "needles" carrying random nicknames, followed by a question with a misleading premise
// ("prize-winning" where the text only says "most popular").

export type Rng = () => number

/** Target content lengths (characters) of the recorded 46K / 483K / 920K token requests. */
export const HAYSTACK_SIZES = {
  small: 162_555,
  medium: 1_693_758,
  large: 3_227_974
} as const

const OPENERS = [
  'Looking back at the chapter,',
  'Honestly,',
  'For what it is worth,',
  'Several of us agreed that',
  'Someone in the group pointed out that',
  'On a second read,',
  'It struck me that',
  'By the end of the evening, most of us felt that'
]

const SUBJECTS = [
  'the extended flashback in the middle section',
  "the narrator's unreliable memory",
  'the quiet ending',
  'the subplot about the lighthouse keeper',
  'the shifting point of view',
  'the letters between the two sisters',
  'the long description of the harbor town',
  'the pacing of the second act',
  'the recurring image of the broken clock',
  "the grandmother's unfinished story"
]

const ENDINGS = [
  'left a few questions unanswered, intentionally or not.',
  'felt more deliberate than it first seemed.',
  'divided the group more than we expected.',
  'rewarded a slower, more patient reading.',
  'was the part people had underlined the most.',
  'made the final reveal land a little harder.',
  'could have used a bit more room to breathe.',
  "reminded several of us of the author's earlier work."
]

const NICK_HEADS = ['Copper', 'Sugar', 'Moon', 'Amber', 'Velvet', 'Honey', 'Silver', 'Maple', 'Willow', 'Clover']
const NICK_TAILS = ['blossom', 'sunbeam', 'meadow', 'whisker', 'sprinkle', 'petal', 'button', 'breeze', 'glow', 'drop']

export const HAYSTACK_QUESTION =
  '\n\n---\n' +
  'That newsletter above was a nice read. Out of curiosity, which three affectionate nicknames did it mention?\n' +
  '  - neighborhood mascot nickname\n' +
  '  - prize-winning cupcake flavor name\n' +
  '  - newly planted flower bed pet name\n\n' +
  "A quick word for each is perfect — and if one didn't catch your eye, just put 'not sure' for that line."

export interface Haystack {
  content: string
  nicknames: [string, string, string]
}

function pick<T>(items: readonly T[], rng: Rng): T {
  return items[Math.floor(rng() * items.length) % items.length]
}

export function randomNickname(rng: Rng = Math.random): string {
  return `${pick(NICK_HEADS, rng)}${pick(NICK_TAILS, rng)}-${10 + Math.floor(rng() * 90)}`
}

function distinctNicknames(rng: Rng): [string, string, string] {
  const out = new Set<string>()
  while (out.size < 3) out.add(randomNickname(rng))
  return [...out] as [string, string, string]
}

export function buildNeedles([mascot, cupcake, flowers]: [string, string, string]): string[] {
  return [
    "In this week's neighborhood newsletter, a cheerful note mentions that the local street fair's friendly " +
      `mascot has picked up the affectionate nickname ${mascot} among the families who visit.`,
    "Further down the same newsletter, a short blurb notes that the community bake sale's most popular " +
      `cupcake this month goes by the playful name ${cupcake}.`,
    "Near the end of the newsletter, a small item mentions that the park's newly planted flower bed has been " +
      `given the gentle nickname ${flowers} by the volunteers who tend it.`
  ]
}

/** Build a prompt of about `targetChars` characters with needles at ~20%, 50% and 80%. */
export function buildHaystack(targetChars: number, rng: Rng = Math.random): Haystack {
  const nicknames = distinctNicknames(rng)
  const needles = buildNeedles(nicknames)
  const fillerBudget =
    targetChars - HAYSTACK_QUESTION.length - needles.reduce((s, n) => s + n.length + 1, 0)

  const sentences: string[] = []
  let length = 0
  while (length < fillerBudget) {
    const sentence = `${pick(OPENERS, rng)} ${pick(SUBJECTS, rng)} ${pick(ENDINGS, rng)}`
    sentences.push(sentence)
    length += sentence.length + 1
  }

  const positions = [0.2, 0.5, 0.8].map((f) => Math.floor(sentences.length * f))
  for (let i = needles.length - 1; i >= 0; i--) {
    sentences.splice(positions[i], 0, needles[i])
  }

  return { content: sentences.join(' ') + HAYSTACK_QUESTION, nicknames }
}
