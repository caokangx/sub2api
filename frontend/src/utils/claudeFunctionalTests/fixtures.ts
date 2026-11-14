// Fixed payloads from the recorded claude-fable-5-2 test session.

/** 16x16 solid green PNG. */
export const GREEN_PNG_BASE64 =
  'iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAIAAACQkWg2AAAAI0lEQVR4nGNkWGDDQApgIkk1w6gG4gATkergYFQDMYDkUAIA0/oA/OjD9t0AAAAASUVORK5CYII='

/** Solid blue lossless WebP. */
export const BLUE_WEBP_BASE64 = 'UklGRh4AAABXRUJQVlA4TBEAAAAvD8ADAAdQqHqUu/+BiOh/AAA='

/** 64x64 dark image returned as the computer-use "screenshot". */
export const DARK_SCREENSHOT_PNG_BASE64 =
  'iVBORw0KGgoAAAANSUhEUgAAAEAAAABACAIAAAAlC+aJAAAATklEQVR42u3PMQ0AAAgDsGnYhX+lSOAmaVIDTTuvRUBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEDgsh2TwC7aD87UAAAAAElFTkSuQmCC'

export const ELMSWORTH_DOCUMENT =
  'The village of Elmsworth holds its market on Thursdays. The old bridge there is made of grey stone.'

/** 200 numbered rules (~4,200 tokens), long enough to be cacheable. */
export const CACHE_SYSTEM_TEXT = Array.from(
  { length: 200 },
  (_, i) => `Rule ${i}: answer tersely and never fabricate facts.\n`
).join('')

export const COMPUTER_TOOL = {
  type: 'computer_20251124',
  name: 'computer',
  display_width_px: 1024,
  display_height_px: 768
}

export const COMPUTER_USE_BETA = 'computer-use-2025-11-24'

export const WEATHER_TOOL = {
  name: 'get_weather',
  description: 'Get the current weather for a city.',
  input_schema: {
    type: 'object',
    properties: { city: { type: 'string' } },
    required: ['city']
  }
}

export const PONG_PROMPT = 'Reply with the single word: pong'
export const SCREENSHOT_PROMPT =
  'Take a screenshot of the current screen with the computer tool. Call the tool immediately, do not describe anything.'
