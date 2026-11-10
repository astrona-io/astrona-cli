import type { ITheme } from '@xterm/xterm'

// The terminal's colours come from the --term-* and --ansi-* properties in
// styles/tokens.css. They are the dark theme's in both themes: the tools a
// lab runs colour their output for a dark terminal.

const ansiNames = [
  'black',
  'red',
  'green',
  'yellow',
  'blue',
  'magenta',
  'cyan',
  'white',
  'brightBlack',
  'brightRed',
  'brightGreen',
  'brightYellow',
  'brightBlue',
  'brightMagenta',
  'brightCyan',
  'brightWhite',
] as const

export function readTheme(el: Element = document.documentElement): ITheme {
  const css = getComputedStyle(el)
  const v = (name: string) => css.getPropertyValue(name).trim() || undefined
  const theme: ITheme = {
    background: v('--term-bg'),
    foreground: v('--term-fg'),
    cursor: v('--term-cursor'),
    cursorAccent: v('--term-bg'),
    selectionBackground: v('--term-selection'),
    scrollbarSliderBackground: v('--term-scrollbar'),
    scrollbarSliderHoverBackground: v('--term-scrollbar-hover'),
    scrollbarSliderActiveBackground: v('--term-scrollbar-hover'),
  }
  ansiNames.forEach((name, i) => {
    theme[name] = v(`--ansi-${i}`)
  })
  return theme
}

export const fontFamily = "'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"

/** Waits for the bundled monospace font so xterm.js measures real glyphs. */
export async function fontsReady(): Promise<void> {
  try {
    await Promise.all([
      document.fonts.load("400 14px 'JetBrains Mono'"),
      document.fonts.load("700 14px 'JetBrains Mono'"),
    ])
  } catch {
    // Falls back to the system monospace font.
  }
}
