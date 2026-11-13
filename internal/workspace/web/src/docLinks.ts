// A lab's documentation links (run.docLinks), as the badges above the task
// and the tabs they open show them. A name reads "Product: page" by
// convention: the product is a small label, the page is the title.

import type { DocLink } from './api'

export interface ShownDocLink {
  /** Its position in run.docLinks: names its tab. */
  index: number
  url: string
  /** The name as the lab gives it, for the tab and tooltips. */
  name: string
  product: string
  title: string
  host: string
}

/** The https links of links, parsed; anything else is left out. */
export function shownDocLinks(links: DocLink[]): ShownDocLink[] {
  return links.flatMap((link, index) => {
    let url: URL
    try {
      url = new URL(link.url)
    } catch {
      return []
    }
    if (url.protocol !== 'https:') return []
    const at = link.name.indexOf(': ')
    const product = at > 0 ? link.name.slice(0, at).trim() : ''
    const title = (at > 0 ? link.name.slice(at + 2).trim() : '') || link.name
    // The original string, not url.href: the server checks the link as listed.
    return [{ index, url: link.url, name: link.name, product, title, host: url.hostname.replace(/^www\./, '') }]
  })
}

/** The name of the tab that shows a documentation link. */
export function resourceTabName(link: ShownDocLink): string {
  return `@resource-${link.index + 1}`
}
